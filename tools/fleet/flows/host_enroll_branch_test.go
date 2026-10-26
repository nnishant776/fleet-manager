package flows

import (
	"regexp"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

const declaredSecrets = `{
  "password/alice": {"sopsFile": "/var/lib/fleet/secrets/users/alice/pi.yaml", "key": "password"},
  "users/support/authorized_keys": {"sopsFile": "/var/lib/fleet/secrets/users/support/pi.yaml", "key": "authorized_keys"},
  "wifi/home": {"sopsFile": "/var/lib/fleet/secrets/hosts/pi.yaml", "key": "wifi/home"},
  "kept": {"sopsFile": "/nix/store/abc-source/kept.yaml", "key": "a"}
}`

// checkBranch runs the check against a fake of host/pi that holds files, where JSON stands in for the sops files:
// the keys are in clear, the values are not read.
func checkBranch(t *testing.T, files map[string]string) (error, *engine.Fake, string) {
	t.Helper()
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "nix" && strings.Contains(args, "conf.secrets.statePath"):
			return engine.Result{Stdout: "/var/lib/fleet"}, nil
		case s.Name == "nix" && strings.Contains(args, "config.sops.secrets"):
			return engine.Result{Stdout: declaredSecrets}, nil
		case s.Name == "git" && strings.Contains(args, "show refs/heads/host/pi:"):
			doc, ok := files[strings.SplitN(args, "refs/heads/host/pi:", 2)[1]]
			if !ok {
				return engine.Result{Code: 128}, errExit
			}
			return engine.Result{Stdout: doc}, nil
		case s.Name == "yq":
			parts := regexp.MustCompile(`\["([^"]*)"\]`).FindAllStringSubmatch(args, -1)
			doc := s.Stdin
			for _, p := range parts {
				i := strings.Index(doc, `"`+p[1]+`"`)
				if i < 0 {
					return engine.Result{Stdout: "false\n"}, nil
				}
				doc = doc[i+len(p[1])+2:]
			}
			return engine.Result{Stdout: "true\n"}, nil
		}
		return engine.Result{}, nil
	})
	x.Values["host"], x.Values["repo"], x.Values["eval_flake"] = "pi", t.TempDir(), "/flake"
	return checkBranchHoldsSecrets(x), fake, out.String()
}

var errExit = &exitError{}

type exitError struct{}

func (*exitError) Error() string { return "exited with code 128" }

func TestEnrollChecksTheBranchHoldsEverySecretTheConfigurationDeclares(t *testing.T) {
	err, fake, out := checkBranch(t, map[string]string{
		"users/alice/pi.yaml":   `{"password": "ENC[…]", "sops": {}}`,
		"users/support/pi.yaml": `{"authorized_keys": "ENC[…]", "sops": {}}`,
		"hosts/pi.yaml":         `{"wifi": {"home": "ENC[…]"}, "sops": {}}`,
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "holds the 3 secrets") {
		t.Errorf("the 3 files of the secrets repository were not the ones counted:\n%s", out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "sops") {
			t.Errorf("ran %s: nothing should be decrypted", c)
		}
	}
}

func TestEnrollStopsForAKeyTheBranchLacksAndSaysWhatAddsIt(t *testing.T) {
	err, _, out := checkBranch(t, map[string]string{
		"users/alice/pi.yaml":   `{"password": "ENC[…]", "sops": {}}`,
		"users/support/pi.yaml": `{"password": "ENC[…]", "sops": {}}`,
		"hosts/pi.yaml":         `{"wifi": {"other": "ENC[…]"}, "sops": {}}`,
	})
	if err == nil {
		t.Fatalf("a missing key was not found\n%s", out)
	}
	for _, want := range []string{
		"missing key authorized_keys in users/support/pi.yaml",
		"missing key wifi/home in hosts/pi.yaml",
		"lacks 2 of the secrets",
		"fleet secrets user-keys support pi --authorized-keys <file of public keys>",
		"fleet host secret pi wifi/home",
	} {
		if !strings.Contains(out+err.Error(), want) {
			t.Errorf("lacks %q:\n%v\n%s", want, err, out)
		}
	}
}

func TestEnrollStopsForAFileTheBranchLacks(t *testing.T) {
	err, _, out := checkBranch(t, map[string]string{
		"users/alice/pi.yaml": `{"password": "ENC[…]", "sops": {}}`,
		"hosts/pi.yaml":       `{"wifi": {"home": "ENC[…]"}, "sops": {}}`,
	})
	if err == nil || !strings.Contains(out, "missing users/support/pi.yaml") {
		t.Fatalf("got %v\n%s", err, out)
	}
}

func TestEnrollChecksTheBranchBeforeTheSwitch(t *testing.T) {
	check, sw := -1, -1
	for i, s := range HostEnroll.Steps {
		switch {
		case strings.HasPrefix(s.Title, "Check host/{{.host}} holds"):
			check = i
		case strings.HasPrefix(s.Title, "Switch {{.host}} to its fleet configuration"):
			sw = i
		}
	}
	if check < 0 || sw < 0 || check > sw {
		t.Errorf("the check is step %d, the switch %d", check, sw)
	}
}
