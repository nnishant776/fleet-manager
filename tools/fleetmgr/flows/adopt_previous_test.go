package flows

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/fleetmgr"
)

func TestWhatOfAnEtcNixosIsTheHostsOwnDependsOnWhatItIs(t *testing.T) {
	if sel := selectPrevious("configuration", etcFacts{}, "generic"); !sel.All {
		t.Errorf("a configuration.nix: %+v", sel)
	}
	e := etcFacts{FleetManager: true, HostDir: true, Git: true,
		Changes: []string{" M hosts/generic/default.nix", "?? notes.txt", " D old.nix", `?? "we ird.nix"`}}
	sel := selectPrevious("flake", e, "generic")
	if sel.All || !slices.Equal(sel.Paths, []string{"hosts/generic", "notes.txt"}) {
		t.Errorf("a fleet-manager checkout: %+v", sel)
	}
	if sel := selectPrevious("flake", etcFacts{Git: true}, "generic"); !sel.All {
		t.Errorf("another flake: %+v", sel)
	}
	if sel := selectPrevious("none", etcFacts{}, "generic"); sel.All || len(sel.Paths) != 0 {
		t.Errorf("nothing: %+v", sel)
	}
}

func TestTheTarCommandReadsOnlyWhatIsSelected(t *testing.T) {
	all := previousTar(previousSelection{All: true})
	if !strings.Contains(all, "--exclude=./.git") || !strings.Contains(all, "--exclude=./result") {
		t.Errorf("everything: %q", all)
	}
	some := previousTar(previousSelection{Paths: []string{"hosts/generic", "notes.txt"}})
	if !strings.Contains(some, "-- 'hosts/generic' 'notes.txt'") && !strings.Contains(some, "-- hosts/generic notes.txt") {
		t.Errorf("paths: %q", some)
	}
	if got := previousTar(previousSelection{}); got != "" {
		t.Errorf("nothing: %q", got)
	}
}

func TestWhatLooksLikeASecretIsLeftOut(t *testing.T) {
	for _, p := range []string{"id_ed25519", "wifi.key", "cert.pem", "secrets.age", ".env"} {
		if !looksSecret(p, []byte("x")) {
			t.Errorf("%s was not taken for a secret", p)
		}
	}
	if !looksSecret("keys.nix", []byte("{ k = ''\n-----BEGIN OPENSSH PRIVATE KEY-----\n''; }")) {
		t.Error("a private key in a .nix file was not taken for a secret")
	}
	for _, p := range []string{"id_ed25519.pub", "configuration.nix", "hardware-configuration.nix"} {
		if looksSecret(p, []byte("{ }")) {
			t.Errorf("%s was taken for a secret", p)
		}
	}
}

func manyFiles(n int) map[string][]byte {
	files := map[string][]byte{}
	for i := 0; i < n; i++ {
		files[fmt.Sprintf("f%02d.nix", i)] = []byte("{ }\n")
	}
	return files
}

func TestAnEtcNixosOfManyFilesIsTooLarge(t *testing.T) {
	if previousTooLarge(manyFiles(51)) == "" {
		t.Error("51 files were not too many")
	}
	if got := previousTooLarge(manyFiles(50)); got != "" {
		t.Errorf("50 files: %q", got)
	}
}

// etcMachine answers a machine whose /etc/nixos is etcOut for the facts
// script and tarball for the tar.
func etcMachine(etcOut string, tarball string) func(string) (engine.Result, error) {
	return func(cmd string) (engine.Result, error) {
		switch {
		case strings.Contains(cmd, "safe.directory=/etc/nixos"):
			return engine.Result{Stdout: etcOut}, nil
		case strings.Contains(cmd, "-C /etc/nixos --exclude"):
			return engine.Result{Stdout: tarball}, nil
		}
		return engine.Result{}, nil
	}
}

func TestAFileThatLooksLikeASecretIsNotKept(t *testing.T) {
	x := adoptCtx(etcMachine("", tarOf(t, map[string]string{"./configuration.nix": "{ }", "./wifi.key": "hunter2"})))
	f := &machineFacts{Host: "laptop3", Hostname: "laptop3"}
	if err := readPrevious(x, f, "configuration"); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Previous["wifi.key"]; ok || len(f.Previous) != 1 {
		t.Errorf("kept %v", sortedPaths(f.Previous))
	}
	if f.PreviousNote != "" {
		t.Errorf("a note for a plain configuration: %q", f.PreviousNote)
	}
}

func TestTooManyFilesStopUnlessKeptOnPurpose(t *testing.T) {
	files := map[string]string{}
	for p := range manyFiles(51) {
		files["./"+p] = "{ }\n"
	}
	x := adoptCtx(etcMachine("", tarOf(t, files)))
	err := readPrevious(x, &machineFacts{Host: "laptop3", Hostname: "laptop3"}, "configuration")
	if err == nil || !strings.Contains(err.Error(), "--keep-previous") {
		t.Fatalf("got %v", err)
	}
	x = adoptCtx(etcMachine("", tarOf(t, files)))
	x.Values["keep-previous"] = "true"
	f := &machineFacts{Host: "laptop3", Hostname: "laptop3"}
	if err := readPrevious(x, f, "configuration"); err != nil || len(f.Previous) != 51 {
		t.Fatalf("with --keep-previous: %v, %d files", err, len(f.Previous))
	}
}

func TestAFleetManagerCheckoutKeepsTheHostsFolderAndItsChanges(t *testing.T) {
	head := strings.Repeat("c", 40)
	facts := "git=true\norigin=https://github.com/example-org/fleet-manager\nhead=" + head + "\nbranch=main\n" +
		"fleet_manager=true\nhost_dir=true\nchange= M hosts/generic/default.nix\nchange=?? notes.txt\n"
	tarball := tarOf(t, map[string]string{"./hosts/generic/default.nix": "{ }", "./hosts/generic/hardware.nix": "{ }", "./notes.txt": "hi"})
	var fake *engine.Fake
	x, fk, _ := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		return etcMachine(facts, tarball)(s.Name + " " + strings.Join(s.Args, " "))
	})
	fake = fk
	for k, v := range map[string]string{"host": "generic", "address": "generic", "admin": "alice", "agent": "/tmp/a.sock", "admin_prelude": "", "admin_password": ""} {
		x.Values[k] = v
	}
	f := &machineFacts{Host: "generic", Hostname: "generic"}
	if err := readPrevious(x, f, "flake"); err != nil {
		t.Fatal(err)
	}
	if got := sortedPaths(f.Previous); !slices.Equal(got, []string{"hosts/generic/default.nix", "hosts/generic/hardware.nix", "notes.txt"}) {
		t.Errorf("kept %v", got)
	}
	for _, want := range []string{"https://github.com/example-org/fleet-manager", head, "nixosConfigurations.generic"} {
		if !strings.Contains(f.PreviousNote, want) {
			t.Errorf("previous.md lacks %q:\n%s", want, f.PreviousNote)
		}
	}
	tars := 0
	for _, c := range fake.Commands() {
		if strings.Contains(c, "tar ") {
			tars++
			if !strings.Contains(c, "hosts/generic") || !strings.Contains(c, "notes.txt") {
				t.Errorf("the tar reads %q", c)
			}
		}
	}
	if tars != 1 {
		t.Errorf("%d tar commands", tars)
	}
}

func TestAnEmptyEtcNixosIsNotedByTheCommitItRan(t *testing.T) {
	x, fake, _ := newFakeCtx(nil)
	f := &machineFacts{Host: "laptop3", Hostname: "generic", OrgRevision: strings.Repeat("d", 40), OrgFleetHost: "generic"}
	if err := readPrevious(x, f, "none"); err != nil {
		t.Fatal(err)
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, "tar ") {
			t.Errorf("ran %q", c)
		}
	}
	if !strings.Contains(f.PreviousNote, "nixosConfigurations.generic") || !strings.Contains(f.PreviousNote, f.OrgRevision) {
		t.Errorf("previous.md:\n%s", f.PreviousNote)
	}
}

func TestPreviousMdIsAddedToTheFolder(t *testing.T) {
	f := machineFacts{Host: "laptop3", Platform: "x86_64-linux", Boot: "uefi", Loader: "systemd-boot", Timezone: "UTC", Locale: "en_US.UTF-8",
		StateVersion: "24.11", Role: "server", MachineType: "headless", Previous: map[string][]byte{"configuration.nix": []byte("{ }")}}
	files, err := adoptFiles(f, fleetSettings{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := files["previous.md"]; ok {
		t.Error("previous.md without a note")
	}
	f.PreviousNote = "# note\n"
	files, err = adoptFiles(f, fleetSettings{}, false)
	if err != nil || string(files["previous.md"]) != "# note\n" {
		t.Fatalf("previous.md: %v, %q", err, files["previous.md"])
	}
}

func TestASkeletonSaysWhereItsStateVersionCameFrom(t *testing.T) {
	f := machineFacts{Host: "laptop3", Platform: "x86_64-linux", Boot: "uefi", Loader: "systemd-boot", Timezone: "UTC", Locale: "en_US.UTF-8",
		StateVersion: "24.11", Role: "server", MachineType: "headless"}
	f.StateVersionFrom = "its /etc/nixos/configuration.nix, evaluated"
	want := "  # The release it was installed with, which some services' data depends on:\n  # its /etc/nixos/configuration.nix, evaluated.\n  system.stateVersion = \"24.11\";"
	if got := skeletonDefault(f, fleetSettings{}); !strings.Contains(got, want) {
		t.Errorf("with a source:\n%s", got)
	}
	f.StateVersionFrom = ""
	old := "  # The release it was installed with, which some services' data depends on.\n  system.stateVersion = \"24.11\";"
	if got := skeletonDefault(f, fleetSettings{}); !strings.Contains(got, old) {
		t.Errorf("without a source:\n%s", got)
	}
}

func TestTheFleetManagerHostAMachineRunsIsFoundAtItsCommit(t *testing.T) {
	rev := strings.Repeat("e", 40)
	run := func(t *testing.T, ancestor bool) machineFacts {
		t.Helper()
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		p, _ := fleetmgr.SignersCopy(fleetmgr.UpstreamURL)
		if err := fleetmgr.WriteSignersCopy(p, []byte("me@test ssh-ed25519 AAAA\n")); err != nil {
			t.Fatal(err)
		}
		x := adoptCtx(func(cmd string) (engine.Result, error) {
			switch {
			case strings.Contains(cmd, "merge-base --is-ancestor") && !ancestor:
				return engine.Result{Code: 1}, fmt.Errorf("exited with code 1")
			case strings.Contains(cmd, "nixosConfigurations.generic.config.system.stateVersion"):
				return engine.Result{Stdout: "26.05"}, nil
			}
			return engine.Result{}, nil
		})
		f := machineFacts{Host: "laptop3", Hostname: "generic", OrgRevision: rev}
		if err := fleetManagerHostAt(x, &f); err != nil {
			t.Fatal(err)
		}
		return f
	}
	if f := run(t, true); f.OrgFleetHost != "generic" || f.OrgStateVersion != "26.05" {
		t.Errorf("a commit of main: %+v", f)
	}
	if f := run(t, false); f.OrgFleetHost != "" || f.OrgStateVersion != "" {
		t.Errorf("a commit that is not on main: %+v", f)
	}
}
