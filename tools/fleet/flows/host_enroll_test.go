package flows

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
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
