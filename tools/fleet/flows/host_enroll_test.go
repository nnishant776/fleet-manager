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
		"host": "pi", "flake": "/etc/nixos", "key": "/keys/home", "repo": repo, "recovery": "",
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
		}
		return res, err
	}
}

// runSwitch enrolls pi, which needs a switch, from a checkout with a signers
// file.
func runSwitch(t *testing.T, respond func(engine.Spec) (engine.Result, error)) (error, *engine.Fake, string) {
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
		"register-with": "true", "sync": "false",
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

// Plain files in /etc/nixos are moved aside, with their permissions, before
// the switch; the first-boot clone then makes the checkout.
func TestEnrollMovesAStockEtcNixosAside(t *testing.T) {
	err, fake, out := runSwitch(t, withEtcNixos(noTooling(enrollHost(false, false, false)), "files", false, ""))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	moved, switched := strings.Index(cmds, "mv /etc/nixos /etc/nixos.before-fleet"), strings.Index(cmds, "nixos-rebuild switch")
	if moved < 0 || switched < 0 || moved > switched {
		t.Fatalf("/etc/nixos was not moved aside before the switch:\n%s", cmds)
	}
	if !strings.Contains(out, "moved to /etc/nixos.before-fleet") {
		t.Errorf("did not say where the files are:\n%s", out)
	}
}

// A checkout of something else becomes the configuration repository's, at
// the commit the host switches to, before the switch.
func TestEnrollMakesAnExistingCheckoutTheConfigurations(t *testing.T) {
	err, fake, out := runSwitch(t, withEtcNixos(noTooling(enrollHost(false, false, false)), "git", false, "https://example.org/mine.git"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	prepared := ranMatching(fake, "sudo -n sh -s -- https://github.com/nnishant776/fleet-manager.git main abc123")
	if len(prepared) != 1 || !strings.Contains(prepared[0].Stdin, "remote rename origin before-fleet") {
		t.Fatalf("the checkout was not prepared with the script: %+v\n%s", prepared, strings.Join(fake.Commands(), "\n"))
	}
	cmds := strings.Join(fake.Commands(), "\n")
	if strings.Index(cmds, "sh -s --") > strings.Index(cmds, "nixos-rebuild switch") {
		t.Fatalf("prepared /etc/nixos after the switch:\n%s", cmds)
	}
	if strings.Contains(cmds, "remote set-url origin") {
		t.Error("pointed origin elsewhere again after preparing the checkout")
	}
}

func TestEnrollStopsOnAnEtcNixosWithChangesOfItsOwn(t *testing.T) {
	err, fake, out := runSwitch(t, withEtcNixos(noTooling(enrollHost(false, false, false)), "git", true, "https://example.org/mine.git"))
	if err == nil || !strings.Contains(out, "uncommitted changes or untracked files") {
		t.Fatalf("went on with changes in /etc/nixos: %v\n%s", err, out)
	}
	for _, unwanted := range []string{"sh -s --", "nixos-rebuild"} {
		if got := ranMatching(fake, unwanted); len(got) > 0 {
			t.Errorf("ran %s", got[0])
		}
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
		"host": "pi", "flake": "/etc/nixos", "key": "/keys/home", "repo": t.TempDir(), "recovery": "",
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
