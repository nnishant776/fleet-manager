package flows

import (
	"archive/tar"
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
)

func caseFor(t *testing.T, answer string, fail bool) string {
	t.Helper()
	x, _, _ := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		if fail {
			return engine.Result{Code: 1}, errors.New("exited with code 1")
		}
		return engine.Result{Stdout: answer}, nil
	})
	x.Values["host"], x.Values["flake"] = "laptop3", "/inv"
	return adoptCase(x)
}

func TestAPersonalMachineHasItsOwnerAdoptedAndAnyOtherIsTakenInWhole(t *testing.T) {
	for _, c := range []struct {
		answer string
		fail   bool
		want   string
	}{
		{"true", false, "owner"},
		{"false", false, "machine"},
		{"null", false, "machine"},
		{"", true, "machine"},
	} {
		if got := caseFor(t, c.answer, c.fail); got != c.want {
			t.Errorf("an inventory that says %q (failing: %v) gives %q, want %q", c.answer, c.fail, got, c.want)
		}
	}
}

func tarOf(t *testing.T, files map[string]string) string {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for name, body := range files {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	_ = w.WriteHeader(&tar.Header{Name: "./result", Linkname: "/nix/store/x", Typeflag: tar.TypeSymlink})
	_ = w.Close()
	return b.String()
}

// machine answers like laptop3: a NixOS laptop with UEFI and systemd-boot,
// NetworkManager, alice an administrator with a password and a key, and bob
// with neither; facts overrides the facts it prints.
func machine(t *testing.T, facts map[string]string) func(engine.Spec) (engine.Result, error) {
	base := map[string]string{
		"arch": "x86_64", "nixos": "true", "boot": "uefi", "loader": "systemd-boot", "backend": "networkmanager",
		"timezone": "Europe/Berlin", "locale": "en_US.UTF-8", "hostname": "laptop3", "graphical": "true", "virt": "none",
		"chassis": "laptop", "etc": "configuration",
	}
	for k, v := range facts {
		base[k] = v
	}
	var out strings.Builder
	for k, v := range base {
		out.WriteString(k + "=" + v + "\n")
	}
	out.WriteString("account=alice:Alice Liddell:/home/alice:wheel,users,audio,networkmanager\n")
	out.WriteString("account=bob:Bob:/home/bob:bob,video\n")
	out.WriteString("account=Bad Name:x:/home/x:users\n")
	etc := tarOf(t, map[string]string{"./configuration.nix": "{ ... }: { system.stateVersion = \"24.11\"; }\n", "./hardware-configuration.nix": "{ }\n"})
	return func(s engine.Spec) (engine.Result, error) {
		script := s.Args[len(s.Args)-1]
		switch {
		case strings.Contains(script, "safe.directory=/etc/nixos"):
			return engine.Result{}, nil
		case strings.Contains(script, "nix-instantiate"):
			return engine.Result{Stdout: "\"24.11\""}, nil
		case strings.Contains(script, "machineFactsScript"), strings.Contains(script, "echo \"arch=$(uname -m)\""):
			return engine.Result{Stdout: out.String()}, nil
		case strings.Contains(script, "getent shadow alice"):
			return engine.Result{Stdout: "alice:$y$j9T$abc$def:19000:0:99999:7:::\n"}, nil
		case strings.Contains(script, "getent shadow bob"):
			return engine.Result{Stdout: "bob:!:19000:0:99999:7:::\n"}, nil
		case strings.Contains(script, "/home/alice/.ssh/authorized_keys"):
			return engine.Result{Stdout: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAliceKey000000000000000000000000000000000 alice@laptop3\n"}, nil
		case strings.Contains(script, "nixos-generate-config"):
			return engine.Result{Stdout: "{ fileSystems.\"/\".device = \"/dev/vda1\"; }\n"}, nil
		case strings.Contains(script, "-C /etc/nixos --exclude"):
			return engine.Result{Stdout: etc}, nil
		}
		return engine.Result{}, nil
	}
}

func readFrom(t *testing.T, respond func(engine.Spec) (engine.Result, error)) (*engine.Ctx, error) {
	t.Helper()
	x, _, _ := newFakeCtx(respond)
	for k, v := range map[string]string{"host": "laptop3", "address": "laptop3", "admin": "alice", "agent": "/tmp/a.sock",
		"admin_prelude": "", "admin_password": ""} {
		x.Values[k] = v
	}
	adoptMachine = machineFacts{}
	return x, readMachine(x)
}

func TestReadingAMachineTakesWhatItsFolderIsWrittenFrom(t *testing.T) {
	x, err := readFrom(t, machine(t, nil))
	if err != nil {
		t.Fatal(err)
	}
	f := adoptMachine
	if f.Platform != "x86_64-linux" || f.Boot != "uefi" || f.Loader != "systemd-boot" || f.Backend != "networkmanager" ||
		f.Hostname != "laptop3" || f.Role != "workstation" || f.MachineType != "laptop" {
		t.Fatalf("read %+v", f)
	}
	if len(f.Accounts) != 2 {
		t.Fatalf("accounts %+v", f.Accounts)
	}
	alice, bob := f.Accounts[0], f.Accounts[1]
	if !alice.Privileged || !alice.Password || !alice.Keys || strings.Join(alice.Groups, ",") != "audio" || alice.Full != "Alice Liddell" {
		t.Errorf("alice is %+v", alice)
	}
	if bob.Privileged || bob.Password || bob.Keys || strings.Join(bob.Groups, ",") != "video" {
		t.Errorf("bob is %+v", bob)
	}
	if x.Value("adopt_hash_alice") != "$y$j9T$abc$def" || x.Value("adopt_hash_bob") != "" {
		t.Errorf("the hashes kept are %q and %q", x.Value("adopt_hash_alice"), x.Value("adopt_hash_bob"))
	}
	if _, ok := f.Previous["configuration.nix"]; !ok || len(f.Previous) != 2 {
		t.Errorf("previous/ would hold %v", sortedPaths(f.Previous))
	}
}

func TestOnlyANixOSMachineIsAdopted(t *testing.T) {
	if _, err := readFrom(t, machine(t, map[string]string{"nixos": "false"})); err == nil || !strings.Contains(err.Error(), "does not run NixOS") {
		t.Fatalf("got %v", err)
	}
	if _, err := readFrom(t, machine(t, map[string]string{"arch": "riscv64"})); err == nil {
		t.Fatal("took a platform the fleet does not build for")
	}
}

func TestGrubWithBiosNeedsItsDiskForASkeleton(t *testing.T) {
	if _, err := readFrom(t, machine(t, map[string]string{"boot": "bios", "loader": "grub"})); err != nil {
		t.Fatal(err)
	}
	if err := skeletonReady("laptop3", adoptMachine); err == nil || !strings.Contains(err.Error(), "--grub-device") {
		t.Fatalf("got %v", err)
	}
	if _, err := readFrom(t, machine(t, map[string]string{"boot": "bios", "loader": "grub", "grub_device": "/dev/disk/by-id/ata-X"})); err != nil {
		t.Fatal(err)
	}
	if adoptMachine.GrubDevice != "/dev/disk/by-id/ata-X" {
		t.Fatalf("device %q", adoptMachine.GrubDevice)
	}
}

func TestAMachineWhoseFactsCannotBeTrustedIsNotWrittenFromThem(t *testing.T) {
	for _, c := range []struct {
		facts map[string]string
		got   func(machineFacts) string
		name  string
	}{
		{map[string]string{"hostname": "laptop3; rm -rf /"}, func(f machineFacts) string { return f.Hostname }, "hostname"},
		{map[string]string{"gen_first": "1", "gen_first_release": `24.11"; evil = "x`}, func(f machineFacts) string { return f.GenFirstRelease }, "gen_first_release"},
		{map[string]string{"org_revision": "not-a-hash"}, func(f machineFacts) string { return f.OrgRevision }, "org_revision"},
	} {
		if _, err := readFrom(t, machine(t, c.facts)); err != nil {
			t.Fatal(err)
		}
		if got := c.got(adoptMachine); got != "" {
			t.Errorf("took the %s %q", c.name, got)
		}
	}
	if _, err := readFrom(t, machine(t, map[string]string{"timezone": `Europe/Berlin"; x = "`})); err != nil {
		t.Fatal(err)
	}
	if adoptMachine.Timezone != "UTC" {
		t.Fatalf("took the timezone %q", adoptMachine.Timezone)
	}
}

func chooseWith(t *testing.T, etc string, flags map[string]string) (*engine.Ctx, error) {
	t.Helper()
	x, _, _ := newFakeCtx(nil)
	x.Values["host"], x.Values["adopt_etc"] = "laptop3", etc
	for k, v := range flags {
		x.Values[k] = v
	}
	adoptMachine = machineFacts{Role: "server", MachineType: "headless", Boot: "uefi", Loader: "systemd-boot", StateVersion: "24.11"}
	return x, chooseAdoptStart(x)
}

func TestACopyNeedsNoneOfWhatOnlyASkeletonNeeds(t *testing.T) {
	x, _, _ := newFakeCtx(nil)
	x.Values["host"], x.Values["adopt_etc"], x.Values["copy-config"] = "laptop3", "configuration", "true"
	adoptMachine = machineFacts{Role: "server", MachineType: "headless", Boot: "bios", Loader: "grub"}
	if err := chooseAdoptStart(x); err != nil {
		t.Fatal(err)
	}
}

func TestHowTheFolderStartsIsChosen(t *testing.T) {
	if x, err := chooseWith(t, "configuration", map[string]string{"copy-config": "true"}); err != nil || x.Value("adopt_start") != "copy" {
		t.Errorf("--copy-config: %v, %q", err, x.Value("adopt_start"))
	}
	if x, err := chooseWith(t, "configuration", map[string]string{"skeleton": "true", "role": "workstation"}); err != nil ||
		x.Value("adopt_start") != "skeleton" || adoptMachine.Role != "workstation" || adoptMachine.MachineType != "headless" {
		t.Errorf("--skeleton --role workstation: %v, %q, %+v", err, x.Value("adopt_start"), adoptMachine)
	}
	if x, err := chooseWith(t, "flake", nil); err != nil || x.Value("adopt_start") != "skeleton" {
		t.Errorf("a flake: %v, %q", err, x.Value("adopt_start"))
	}
	if _, err := chooseWith(t, "flake", map[string]string{"copy-config": "true"}); err == nil {
		t.Error("copied a flake")
	}
	if _, err := chooseWith(t, "configuration", map[string]string{"copy-config": "true", "skeleton": "true"}); err == nil {
		t.Error("took both")
	}
	if _, err := chooseWith(t, "configuration", nil); err == nil || !strings.Contains(err.Error(), "--copy-config") {
		t.Errorf("chose with nobody to ask: %v", err)
	}
}

func TestATarThatReachesOutsideIsRefused(t *testing.T) {
	if _, err := untarFiles([]byte(tarOf(t, map[string]string{"../etc/shadow": "x"}))); err == nil {
		t.Fatal("took a path outside")
	}
	files, err := untarFiles([]byte(tarOf(t, map[string]string{"./a/b.nix": "x"})))
	if err != nil || string(files["a/b.nix"]) != "x" || len(files) != 1 {
		t.Fatalf("got %v, %v", files, err)
	}
}

func TestKnownEvaluationErrorsComeWithAHint(t *testing.T) {
	for stderr, want := range map[string]string{
		"error: The option `x' has conflicting definition values:":               "lib.mkForce",
		"error: file 'nixpkgs' was not found in the Nix search path":             "<nixpkgs>",
		"error: access to absolute path '/etc/x' is forbidden in pure eval mode": "outside the inventory",
		"something else": "",
	} {
		if got := evalHint(stderr); want == "" && got != "" || !strings.Contains(got, want) {
			t.Errorf("%q gives the hint %q", stderr, got)
		}
	}
}
