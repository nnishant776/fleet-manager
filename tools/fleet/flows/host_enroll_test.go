package flows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

const enrollDoc = "version: 1\nhost: pi\ncreated: 2026-10-18T12:14:02Z\n" +
	"age_recipient: age1testrecipient\n" +
	"deploy_pubkey: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDdBK90 fleet-deploy@pi\n"

// enrollHost answers like a host with fleet's tooling. identity and enrolled
// say how far its enrollment got; tamper corrupts the enrollment checksum.
func enrollHost(identity, enrolled, tamper bool) func(engine.Spec) (engine.Result, error) {
	sum := sha256.Sum256([]byte(enrollDoc))
	sumLine := hex.EncodeToString(sum[:]) + "  /var/lib/fleet/pi.enroll\n"
	if tamper {
		sumLine = strings.Repeat("0", 64) + "  /var/lib/fleet/pi.enroll\n"
	}
	return func(s engine.Spec) (engine.Result, error) {
		last := ""
		if len(s.Args) > 0 {
			last = s.Args[len(s.Args)-1]
		}
		switch {
		case s.Name == "ssh-agent":
			return engine.Result{Stdout: "SSH_AUTH_SOCK=/tmp/a.sock; export SSH_AUTH_SOCK;\nSSH_AGENT_PID=4242; export SSH_AGENT_PID;\n"}, nil
		case s.Name == "ssh" && strings.Contains(last, "has_tooling"):
			return engine.Result{Stdout: "login=admin\nsudo=true\nhas_tooling=true\n" +
				"has_identity=" + map[bool]string{true: "true", false: "false"}[identity] + "\n" +
				"has_config_key=" + map[bool]string{true: "true", false: "false"}[identity] + "\n" +
				"has_checkout=true\norigin=https://github.com/nnishant776/fleet-manager.git\nhas_support=true\n"}, nil
		case s.Name == "nix" && strings.Contains(strings.Join(s.Args, " "), "conf.fleet.repo.url"):
			return engine.Result{Stdout: "https://github.com/nnishant776/fleet-manager.git"}, nil
		case s.Name == "ssh" && strings.Contains(last, "pi.enroll"):
			return engine.Result{Stdout: enrollDoc + sumLine}, nil
		case s.Name == "yq":
			if enrolled {
				return engine.Result{Stdout: "age1testrecipient\n"}, nil
			}
			return engine.Result{Stdout: "\n"}, nil
		case s.Name == "git" && strings.Contains(strings.Join(s.Args, " "), "rev-list"):
			if enrolled {
				return engine.Result{Stdout: "0\n"}, nil
			}
			return engine.Result{Stdout: "1\n"}, nil
		}
		return engine.Result{}, nil
	}
}

func runEnroll(t *testing.T, respond func(engine.Spec) (engine.Result, error)) (error, *engine.Fake, string) {
	t.Helper()
	var out bytes.Buffer
	fake := &engine.Fake{Respond: respond}
	x := &engine.Ctx{
		Context:  context.Background(),
		UI:       ui.NewWith(&out, nil),
		Runner:   fake,
		Options:  engine.Options{Yes: true},
		Values:   map[string]string{},
		Stdin:    strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}
	repo := t.TempDir()
	err := engine.RunWith(x, HostEnroll, map[string]string{
		"host": "pi", "flake": "/no/such/flake", "key": "/keys/home", "repo": repo, "recovery": "",
		"register-with": "cat >> /forge/authorized_keys", "sync": "false",
	})
	return err, fake, out.String()
}

func ranMatching(f *engine.Fake, fragment string) []engine.Spec {
	var got []engine.Spec
	for _, s := range f.Ran {
		if strings.Contains(s.String(), fragment) {
			got = append(got, s)
		}
	}
	return got
}

func TestEnrollDoesWhatIsMissingInOrder(t *testing.T) {
	err, fake, out := runEnroll(t, enrollHost(false, false, false))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	order := []string{"ssh-agent", "ssh-add /keys/home", "has_tooling", "fleet-secrets-sync init", "pi.enroll", "secrets host-enroll pi --from", "push", "sh -c 'cat >> /forge/authorized_keys'", "kill 4242"}
	cmds := strings.Join(fake.Commands(), "\n")
	pos := 0
	for _, want := range order {
		i := strings.Index(cmds[pos:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", want, cmds)
		}
		pos += i
	}
	for _, unwanted := range []string{"nixos-rebuild", "remote set-url"} {
		if strings.Contains(cmds, unwanted) {
			t.Errorf("ran %q although nothing called for it", unwanted)
		}
	}
	// Admin is reached only through the one-key agent, and that agent is the
	// only one forwarded.
	for _, s := range ranMatching(fake, "admin@pi") {
		if !strings.Contains(s.String(), "SSH_AUTH_SOCK=") || !strings.Contains(s.String(), " -A ") {
			t.Errorf("admin command without the one-key agent: %s", s)
		}
	}
	reg := ranMatching(fake, "authorized_keys")
	if len(reg) != 1 || !strings.HasPrefix(reg[0].Stdin, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDdBK90") {
		t.Fatalf("the deploy key was not handed to the registering command: %+v", reg)
	}
}

// noTooling answers like a host that does not run a fleet configuration yet,
// whose configuration here sets conf.secrets.repo.url, with upstream main at
// abc123.
func noTooling(respond func(engine.Spec) (engine.Result, error)) func(engine.Spec) (engine.Result, error) {
	return func(s engine.Spec) (engine.Result, error) {
		res, err := respond(s)
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "ssh" && strings.Contains(args, "has_tooling"):
			res.Stdout = strings.Replace(res.Stdout, "has_tooling=true", "has_tooling=false", 1)
		case s.Name == "nix" && strings.Contains(args, "conf.secrets.repo.url"):
			res.Stdout = "git@github.com:example-org/secrets.git"
		case s.Name == "git" && strings.Contains(args, "rev-parse FETCH_HEAD"):
			res.Stdout = "abc123\n"
		case s.Name == "nix" && strings.Contains(args, "c.fileSystems"):
			res.Stdout = `{"fs":{"/":"/dev/disk/by-label/nixos","/boot":"/dev/disk/by-label/boot"},"efi":true,"disko":false}`
		}
		return res, err
	}
}

// runSwitch enrolls pi, which needs a switch, from a checkout with a signers
// file.
func runSwitch(t *testing.T, respond func(engine.Spec) (engine.Result, error)) (error, *engine.Fake, string) {
	return runSwitchSync(t, respond, "false")
}

// runSwitchSync is runSwitch, with the sync at the end if sync is true.
func runSwitchSync(t *testing.T, respond func(engine.Spec) (engine.Result, error), sync string) (error, *engine.Fake, string) {
	t.Helper()
	trustCopies(t, fleet.UpstreamURL)
	flake := t.TempDir()
	_ = os.MkdirAll(filepath.Join(flake, "fleet"), 0o755)
	_ = os.WriteFile(filepath.Join(flake, "fleet", "allowed_signers"), []byte("me namespaces=\"git\" ssh-ed25519 AAAA\n"), 0o644)
	var out bytes.Buffer
	fake := &engine.Fake{Respond: respond}
	x := &engine.Ctx{
		Context: context.Background(), UI: ui.NewWith(&out, nil), Runner: fake,
		Options: engine.Options{Yes: true}, Values: map[string]string{}, Stdin: strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}
	err := engine.RunWith(x, HostEnroll, map[string]string{
		"host": "pi", "flake": flake, "key": "/keys/home", "repo": t.TempDir(), "recovery": "",
		"register-with": "true", "sync": sync,
	})
	return err, fake, out.String()
}

// The first switch takes main's newest commit only once it is signed by a
// key the checkout here trusts, and builds exactly that commit, fetched with
// git.
func TestEnrollSwitchesToASignedCommitOfMain(t *testing.T) {
	err, fake, out := runSwitch(t, noTooling(enrollHost(false, false, false)))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	order := []string{"fetch -q --depth=1 https://github.com/nnishant776/fleet-manager refs/heads/main",
		// Quoted for the host's shell, then again as part of the ssh command.
		"verify-commit abc123", `nixos-rebuild switch --flake '\''git+https://github.com/nnishant776/fleet-manager?rev=abc123#pi'\''`}
	pos := 0
	for _, want := range order {
		i := strings.Index(cmds[pos:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", want, cmds)
		}
		pos += i
	}
}

func TestEnrollDoesNotSwitchToAnUnsignedCommit(t *testing.T) {
	err, fake, out := runSwitch(t, noTooling(func(s engine.Spec) (engine.Result, error) {
		if s.Name == "git" && strings.Contains(strings.Join(s.Args, " "), "verify-commit") {
			return engine.Result{Code: 1, Stderr: "No signature"}, errors.New("exited with code 1")
		}
		return enrollHost(false, false, false)(s)
	}))
	if err == nil {
		t.Fatalf("switched to a commit no trusted key signed\n%s", out)
	}
	if !strings.Contains(out, "is not signed by a key you trust") {
		t.Errorf("output:\n%s", out)
	}
	if got := ranMatching(fake, "nixos-rebuild"); len(got) > 0 {
		t.Fatalf("switched anyway: %s", got[0])
	}
}

// withEtcNixos answers that /etc/nixos holds kind (git, files or empty),
// with changes of its own if dirty, fetching origin; the configuration's ref
// is main.
func withEtcNixos(respond func(engine.Spec) (engine.Result, error), kind string, dirty bool, origin string) func(engine.Spec) (engine.Result, error) {
	return func(s engine.Spec) (engine.Result, error) {
		res, err := respond(s)
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "ssh" && strings.Contains(args, "has_tooling"):
			// Later lines win, as a host's answer is read.
			res.Stdout += "etc_nixos=" + kind + "\ndirty=" + map[bool]string{true: "true", false: "false"}[dirty] + "\norigin=" + origin + "\n"
		case s.Name == "nix" && strings.Contains(args, "conf.fleet.repo.ref"):
			res.Stdout = "main"
		}
		return res, err
	}
}

// listsEtcNixos answers the warning's read-only command like a host whose
// /etc/nixos holds listing.
func listsEtcNixos(respond func(engine.Spec) (engine.Result, error), listing string) func(engine.Spec) (engine.Result, error) {
	return func(s engine.Spec) (engine.Result, error) {
		if s.Name == "ssh" && strings.Contains(s.String(), "ls -A /etc/nixos | head") || s.Name == "ssh" && strings.Contains(s.String(), "symbolic-ref") {
			return engine.Result{Stdout: listing}, nil
		}
		return respond(s)
	}
}

// remakeRuns finds the run of the remake script among the commands: the unit
// that runs it, once the script has been written to the host.
func remakeRuns(fake *engine.Fake) (int, engine.Spec) {
	written := false
	for i, s := range fake.Ran {
		if strings.Contains(s.Stdin, "systemctl start fleet-bootstrap") && strings.Contains(s.String(), "/run/fleet-enroll-remake.sh") {
			written = true
		}
		if written && strings.Contains(s.String(), "--unit=fleet-enroll-remake") {
			return i, s
		}
	}
	return -1, engine.Spec{}
}

// /etc/nixos is replaced, not kept: before the switch the person sees what it
// holds and is asked, and nothing changes; after the switch the first-boot
// clone makes the checkout, from the script run on the host.
func TestEnrollWarnsThenReplacesEtcNixos(t *testing.T) {
	for _, tc := range []struct {
		kind, origin, listing, wasGit string
		shown                         []string
	}{
		{"files", "", "configuration.nix\nhardware-configuration.nix\n", "false", []string{"configuration.nix", "hardware-configuration.nix"}},
		{"git", "https://example.org/mine.git", "origin: https://example.org/mine.git\nbranch: main\nuncommitted or untracked entries: 3\n", "true",
			[]string{"origin: https://example.org/mine.git", "branch: main", "uncommitted or untracked entries: 3"}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			err, fake, out := runSwitch(t, listsEtcNixos(withEtcNixos(noTooling(enrollHost(false, false, false)), tc.kind, true, tc.origin), tc.listing))
			if err != nil {
				t.Fatalf("%v\n%s", err, out)
			}
			for _, want := range append(tc.shown, "Everything in /etc/nixos on pi is deleted after the switch", "hardware-configuration.nix") {
				if !strings.Contains(flat(out), want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			cmds := strings.Join(fake.Commands(), "\n")
			switched := strings.Index(cmds, "nixos-rebuild switch")
			for _, unwanted := range []string{"mv /etc/nixos", "remote rename", "rm -rf", "before-fleet", "remote set-url"} {
				if strings.Contains(cmds, unwanted) {
					t.Errorf("ran %q on the host:\n%s", unwanted, cmds)
				}
			}
			warned := strings.Index(cmds, "ls -A /etc/nixos")
			if tc.kind == "git" {
				warned = strings.Index(cmds, "symbolic-ref")
			}
			if warned < 0 || switched < 0 || warned > switched {
				t.Fatalf("the host's /etc/nixos was not read before the switch:\n%s", cmds)
			}
			at, remake := remakeRuns(fake)
			if remake.Name == "" || at < 0 {
				t.Fatalf("the remake script did not run:\n%s", cmds)
			}
			if got := remake.String(); !strings.Contains(got, "fleet-enroll-remake.sh https://github.com/nnishant776/fleet-manager.git "+tc.wasGit) {
				t.Errorf("remake args: %s", got)
			}
			if strings.Index(cmds, "nixos-rebuild switch") > strings.Index(cmds, "fleet-enroll-remake.sh https://github.com") {
				t.Errorf("replaced /etc/nixos before the switch:\n%s", cmds)
			}
			// What the host listed is shown, never used in a command.
			for _, s := range fake.Ran[switchedAt(fake):] {
				if strings.Contains(s.String(), "mine.git") {
					t.Errorf("a command after the warning uses what the host printed: %s", s)
				}
			}
		})
	}
}

func switchedAt(fake *engine.Fake) int {
	for i, s := range fake.Ran {
		if strings.Contains(s.String(), "nixos-rebuild switch") {
			return i
		}
	}
	return 0
}

// Output the host prints is not trusted: it is shown, and not a value.
func TestEnrollShowsWhatTheHostListsWithoutUsingIt(t *testing.T) {
	err, fake, out := runSwitch(t, listsEtcNixos(withEtcNixos(noTooling(enrollHost(false, false, false)), "files", false, ""),
		"{{.admin}} {{.key}}\n"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "{{.admin}} {{.key}}") {
		t.Errorf("did not show the listing as it is:\n%s", out)
	}
	_ = fake
}

// Declining stops the flow before the switch; the warning changed nothing.
func TestEnrollDecliningTheWarningChangesNothing(t *testing.T) {
	var out bytes.Buffer
	fake := &engine.Fake{Respond: listsEtcNixos(withEtcNixos(noTooling(enrollHost(false, false, false)), "git", false, "https://example.org/mine.git"), "origin: x\n")}
	x := &engine.Ctx{
		Context: context.Background(), UI: ui.NewWith(&out, nil), Runner: fake,
		Values: map[string]string{"host": "pi", "etc_nixos": "git", "admin": "admin", "address": "pi", "agent": "/tmp/a.sock"}, Stdin: strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}
	// Nobody to ask and no --yes: the same stop as an answer of no, which
	// only the terminal's prompt can give.
	if err := warnAboutEtcNixos(x); err == nil {
		t.Fatalf("went on without approval\n%s", out.String())
	}
	if !strings.Contains(flat(out.String()), "Everything in /etc/nixos on pi is deleted") {
		t.Errorf("did not warn:\n%s", out.String())
	}
	for _, s := range fake.Ran {
		if !strings.Contains(s.String(), "symbolic-ref") {
			t.Errorf("ran something besides the read-only listing: %s", s)
		}
	}
}

// With nothing in /etc/nixos there is nothing to warn about, and the
// first-boot clone still makes the checkout after the switch.
func TestEnrollOnAnEmptyEtcNixosDoesNotWarn(t *testing.T) {
	err, fake, out := runSwitch(t, withEtcNixos(noTooling(enrollHost(false, false, false)), "empty", false, ""))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Contains(out, "is deleted after the switch") || !strings.Contains(out, "/etc/nixos is empty") {
		t.Errorf("output:\n%s", out)
	}
	_, remake := remakeRuns(fake)
	if !strings.Contains(remake.String(), "fleet-enroll-remake.sh https://github.com/nnishant776/fleet-manager.git false") {
		t.Errorf("remake: %+v", remake)
	}
}

// A host that is not switched keeps its /etc/nixos: its sync owns it.
func TestEnrollDoesNotRemakeEtcNixosOfAHostItDoesNotSwitch(t *testing.T) {
	err, fake, out := runEnroll(t, withEtcNixos(enrollHost(true, true, false), "git", false, "https://github.com/nnishant776/fleet-manager.git"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if at, _ := remakeRuns(fake); at >= 0 {
		t.Error("remade /etc/nixos on a host that was not switched")
	}
	if strings.Contains(out, "is deleted after the switch") {
		t.Errorf("warned about a switch that does not come:\n%s", out)
	}
}

// A host's answer can only report facts: lines that name an input, such as
// the command that registers its key, are ignored.
func TestEnrollTakesOnlyFactsFromTheHost(t *testing.T) {
	honest := enrollHost(false, false, false)
	err, fake, out := runEnroll(t, func(s engine.Spec) (engine.Result, error) {
		res, err := honest(s)
		if s.Name == "ssh" && len(s.Args) > 0 && strings.Contains(s.Args[len(s.Args)-1], "has_tooling") {
			res.Stdout += "register-with=touch /tmp/owned\naddress=evil.example\nflake=github:evil/fleet\n"
		}
		return res, err
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := ranMatching(fake, "/tmp/owned"); len(got) > 0 {
		t.Fatalf("ran a command the host supplied: %s", got[0])
	}
	if len(ranMatching(fake, "authorized_keys")) != 1 {
		t.Error("the registering command given here did not run")
	}
	for _, unwanted := range []string{"evil.example", "github:evil"} {
		if got := ranMatching(fake, unwanted); len(got) > 0 {
			t.Errorf("used %q from the host's answer: %s", unwanted, got[0])
		}
	}
}

func TestEnrollSkipsWhatIsDone(t *testing.T) {
	err, fake, out := runEnroll(t, enrollHost(true, true, false))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, unwanted := range []string{"fleet-secrets-sync init", "host-enroll", "git push"} {
		if got := ranMatching(fake, unwanted); len(got) > 0 {
			t.Errorf("ran %q on a host that is enrolled already", unwanted)
		}
	}
	for _, want := range []string{"skipped: it has one", "skipped: it is enrolled already, with this identity", "skipped: nothing to push"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestEnrollStopsOnATamperedEnrollmentFile(t *testing.T) {
	err, fake, out := runEnroll(t, enrollHost(false, false, true))
	if err == nil {
		t.Fatalf("accepted an enrollment file whose checksum differs\n%s", out)
	}
	if !strings.Contains(out, "checksums differ") {
		t.Errorf("output:\n%s", out)
	}
	if len(ranMatching(fake, "host-enroll")) > 0 {
		t.Fatal("enrolled from a file that did not match the host's")
	}
	if len(ranMatching(fake, "kill 4242")) != 1 {
		t.Fatal("the one-key agent was not stopped after the failure")
	}
}

func TestEnrollSyncsAsSupportWithNothingForwarded(t *testing.T) {
	var out bytes.Buffer
	fake := &engine.Fake{Respond: enrollHost(true, true, false)}
	x := &engine.Ctx{
		Context: context.Background(), UI: ui.NewWith(&out, nil), Runner: fake,
		Options: engine.Options{Yes: true}, Values: map[string]string{}, Stdin: strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}
	err := engine.RunWith(x, HostEnroll, map[string]string{
		"host": "pi", "flake": "/no/such/flake", "key": "/keys/home", "repo": t.TempDir(), "recovery": "",
		"register-with": "true", "sync": "true",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	syncs := ranMatching(fake, "systemctl start config-auto-update")
	if len(syncs) != 1 {
		t.Fatalf("ran the sync %d times", len(syncs))
	}
	s := syncs[0].String()
	if !strings.Contains(s, "support@pi") || !strings.Contains(s, "ForwardAgent=no") || strings.Contains(s, "SSH_AUTH_SOCK") {
		t.Fatalf("the sync did not go in as support with nothing forwarded: %s", s)
	}
}

// A host enrolled before hosts had a configuration key, whose configuration
// is now private: init adds the key, and both keys are registered.
func TestEnrollAddsAndRegistersTheConfigurationKey(t *testing.T) {
	base := enrollHost(true, true, false)
	doc := enrollDoc + "config_deploy_pubkey: ssh-ed25519 AAAACONFIG fleet-config@pi\n"
	sum := sha256.Sum256([]byte(doc))
	err, fake, out := runEnroll(t, func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "ssh" && strings.Contains(args, "has_tooling"):
			r, err := base(s)
			r.Stdout = strings.Replace(r.Stdout, "has_config_key=true", "has_config_key=false", 1)
			// It fetches that repository already: this is not a move.
			r.Stdout = strings.Replace(r.Stdout, "origin=https://github.com/nnishant776/fleet-manager.git", "origin=git@github.com:example-org/inventory.git", 1)
			return r, err
		case s.Name == "ssh" && strings.Contains(args, "pi.enroll"):
			return engine.Result{Stdout: doc + hex.EncodeToString(sum[:]) + "  /var/lib/fleet/pi.enroll\n"}, nil
		case s.Name == "nix" && strings.Contains(args, "conf.fleet.repo.url"):
			return engine.Result{Stdout: "git@github.com:example-org/inventory.git"}, nil
		}
		return base(s)
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if n := len(ranMatching(fake, "fleet-secrets-sync init")); n != 1 {
		t.Errorf("init ran %d times, want once to add the configuration key\n%s", n, out)
	}
	registered := ranMatching(fake, "cat >> /forge/authorized_keys")
	if len(registered) != 2 || !strings.Contains(registered[0].Stdin, "fleet-deploy@pi") ||
		!strings.Contains(registered[1].Stdin, "AAAACONFIG") {
		t.Errorf("registered %d keys, want the deploy key then the configuration key\n%s", len(registered), out)
	}
}

// A machine whose filesystems are not the ones its configuration names is
// not switched: it would not boot.
func TestEnrollDoesNotSwitchAMachineItsConfigurationDoesNotFit(t *testing.T) {
	err, fake, out := runSwitch(t, noTooling(func(s engine.Spec) (engine.Result, error) {
		if s.Name == "ssh" && strings.Contains(s.Stdin, "findmnt") {
			return engine.Result{Stdout: "differs / /dev/disk/by-label/nixos /dev/sda2\nnoefi\n"}, nil
		}
		return enrollHost(false, false, false)(s)
	}))
	if err == nil {
		t.Fatalf("switched a machine its configuration does not fit\n%s", out)
	}
	for _, want := range []string{"mounts /dev/disk/by-label/nixos at /, and pi has /dev/sda2 there", "boots with UEFI, and pi does not"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if got := ranMatching(fake, "nixos-rebuild"); len(got) > 0 {
		t.Fatalf("switched anyway: %s", got[0])
	}
}

// A host without fleet's tools makes its identity before the switch, with
// fleet-secrets-sync from the fleet-manager commit checked for it.
func TestEnrollMakesTheIdentityBeforeTheSwitch(t *testing.T) {
	err, fake, out := runSwitch(t, noTooling(enrollHost(false, false, false)))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	// The commands as the host's shell gets them, in the order they ran.
	init, switched := -1, -1
	for i, s := range fake.Ran {
		script := ""
		if len(s.Args) > 0 {
			script = s.Args[len(s.Args)-1]
		}
		if strings.Contains(script, "run 'git+https://github.com/nnishant776/fleet-manager?rev=abc123#fleet-secrets-sync' -- init --state /var/lib/fleet --host pi") {
			init = i
		}
		if strings.Contains(script, "nixos-rebuild switch") {
			switched = i
		}
	}
	if init < 0 || switched < 0 || init > switched {
		t.Fatalf("the identity was not made before the switch:\n%s", strings.Join(fake.Commands(), "\n"))
	}
}

// privateEnroll answers like pi, running NixOS without fleet's tools, whose
// configuration is private (git@github.com:example-org/inventory.git), and
// like the checkout of that inventory in dir.
func privateEnroll(dir string) func(engine.Spec) (engine.Result, error) {
	doc := enrollDoc + "config_deploy_pubkey: ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIConfigKey fleet-config@pi\n"
	sum := sha256.Sum256([]byte(doc))
	host := noTooling(enrollHost(false, false, false))
	repo := prepRepo("git@github.com:example-org/inventory.git", "")
	return func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		last := ""
		if len(s.Args) > 0 {
			last = s.Args[len(s.Args)-1]
		}
		switch {
		case s.Name == "git" && (strings.Contains(args, "-C "+dir) || strings.Contains(args, ":bootstrap") || strings.Contains(args, "verify-commit")):
			return repo(s)
		case s.Name == "nix" && strings.Contains(args, "conf.fleet.repo.url"):
			return engine.Result{Stdout: "git@github.com:example-org/inventory.git"}, nil
		case s.Name == "nix" && strings.Contains(args, "lib.forgeHostKeys.text"):
			return engine.Result{Stdout: "github.com ssh-ed25519 AAAAC3NzaPinned\n"}, nil
		case s.Name == "nix" && strings.Contains(args, "lib.forgeHostKeys.algorithms"):
			return engine.Result{Stdout: "ssh-ed25519"}, nil
		case s.Name == "ssh" && strings.Contains(last, "pi.enroll"):
			return engine.Result{Stdout: doc + hex.EncodeToString(sum[:]) + "  /var/lib/fleet/pi.enroll\n"}, nil
		}
		return host(s)
	}
}

// A running machine joins a private fleet without a reinstall: its identity
// first, from the checked fleet-manager commit; both keys registered; then
// the switch to the checked commit of its private configuration, fetched
// with its own key and GitHub's pinned host keys.
func TestEnrollAPrivateHostWithoutReinstalling(t *testing.T) {
	dir := prepCheckout(t, pinnedFM)
	var out bytes.Buffer
	fake := &engine.Fake{Respond: privateEnroll(dir)}
	x := &engine.Ctx{
		Context: context.Background(), UI: ui.NewWith(&out, nil), Runner: fake,
		Options: engine.Options{Yes: true}, Values: map[string]string{}, Stdin: strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}
	err := engine.RunWith(x, HostEnroll, map[string]string{
		"host": "pi", "flake": dir, "key": "/keys/home", "repo": t.TempDir(), "recovery": "",
		"register-with": "cat >> /forge/authorized_keys", "sync": "false",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	at := map[string]int{}
	for i, s := range fake.Ran {
		script := s.String()
		if len(s.Args) > 0 {
			script = s.Args[len(s.Args)-1]
		}
		for name, frag := range map[string]string{
			"init":     "run 'git+https://github.com/nnishant776/fleet-manager?rev=" + pinnedFM + "#fleet-secrets-sync' -- init",
			"config":   "cat >> /forge/authorized_keys",
			"pinned":   "cat > /var/lib/fleet/known_hosts.pinned",
			"switch":   "nixos-rebuild switch --flake 'git+ssh://git@github.com/example-org/inventory.git?ref=main&rev=" + pinnedINV + "#pi'",
			"with key": "'--setenv=GIT_SSH_COMMAND=ssh -i /var/lib/fleet/config_deploy_key -o IdentitiesOnly=yes -o GlobalKnownHostsFile=/var/lib/fleet/known_hosts.pinned",
		} {
			if strings.Contains(script, frag) {
				at[name] = i
			}
		}
	}
	for _, name := range []string{"init", "config", "pinned", "switch", "with key"} {
		if _, ok := at[name]; !ok {
			t.Fatalf("%s did not happen:\n%s", name, strings.Join(fake.Commands(), "\n"))
		}
	}
	if !(at["init"] < at["config"] && at["config"] < at["pinned"] && at["pinned"] < at["switch"]) {
		t.Errorf("out of order: %v\n%s", at, strings.Join(fake.Commands(), "\n"))
	}
	if !strings.Contains(fake.Ran[at["pinned"]].Stdin, "AAAAC3NzaPinned") {
		t.Error("the pinned keys written are not fleet-manager's")
	}
}

// movingHost answers like pi, which runs a fleet configuration from
// https://example.org/old.git, with a checkout there, while the configuration
// here names another repository; upstream main is at abc123.
func movingHost(respond func(engine.Spec) (engine.Result, error)) func(engine.Spec) (engine.Result, error) {
	return withEtcNixos(func(s engine.Spec) (engine.Result, error) {
		res, err := respond(s)
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "nix" && strings.Contains(args, "conf.secrets.repo.url"):
			res.Stdout = "git@github.com:example-org/secrets.git"
		case s.Name == "git" && strings.Contains(args, "rev-parse FETCH_HEAD"):
			res.Stdout = "abc123\n"
		case s.Name == "nix" && strings.Contains(args, "c.fileSystems"):
			res.Stdout = `{"fs":{"/":"/dev/disk/by-label/nixos","/boot":"/dev/disk/by-label/boot"},"efi":true,"disko":false}`
		}
		return res, err
	}, "git", false, "https://example.org/old.git")
}

// A host that runs a fleet configuration from another repository is moved to
// this one: the commit is chosen, the configuration checked against the
// machine, the person warned, the host switched, /etc/nixos remade from
// scratch, and only then the sync.
func TestEnrollMovesAHostFromAnotherRepository(t *testing.T) {
	err, fake, out := runSwitchSync(t, listsEtcNixos(movingHost(enrollHost(true, true, false)), "origin: https://example.org/old.git\nbranch: main\nuncommitted or untracked entries: 0\n"), "true")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	order := []string{"verify-commit abc123", "sh -s -- true / /dev/disk", "symbolic-ref", "nixos-rebuild switch", "fleet-enroll-remake.sh https://github.com", "systemctl start config-auto-update"}
	pos := 0
	for _, want := range order {
		i := strings.Index(cmds[pos:], want)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", want, cmds)
		}
		pos += i
	}
	if !strings.Contains(flat(out), "pi moves from https://example.org/old.git to https://github.com/nnishant776/fleet-manager.git") {
		t.Errorf("did not say where it moves from and to:\n%s", out)
	}
	_, remake := remakeRuns(fake)
	if !strings.Contains(remake.String(), "fleet-enroll-remake.sh https://github.com/nnishant776/fleet-manager.git true") {
		t.Errorf("the old checkout is not replaced: %+v", remake)
	}
}

// A host that runs this repository's configuration already is not switched or
// remade, and the steps say which repository.
func TestEnrollSkipsTheSwitchOfAHostOnThisRepository(t *testing.T) {
	err, fake, out := runEnroll(t, withEtcNixos(enrollHost(true, true, false), "git", false, "https://github.com/nnishant776/fleet-manager.git"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.Count(out, "skipped: it already runs a configuration with secrets from https://github.com/nnishant776/fleet-manager.git"); got != 4 {
		t.Errorf("%d steps name the repository, want 4 (choose, fit, warn, switch):\n%s", got, out)
	}
	for _, unwanted := range []string{"nixos-rebuild", "verify-commit", "sh -s -- true / /dev/disk", "fleet-enroll-remake.sh https://github.com"} {
		if got := ranMatching(fake, unwanted); len(got) > 0 {
			t.Errorf("ran %s", got[0])
		}
	}
}
