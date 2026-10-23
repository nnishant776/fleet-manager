package flows

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

const prepBootstrap = "#!/bin/sh\n# pinned-installs: 1 (marker)\necho pinned\n"

// prepCheckout is a configuration checkout whose flake.lock pins
// fleet-manager at fm, or none when fm is empty, as fleet-manager's own.
func prepCheckout(t *testing.T, fm string) string {
	t.Helper()
	trustCopies(t, "git@github.com:example-org/inventory.git", fleet.UpstreamURL)
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".git"), 0o755)
	_ = os.MkdirAll(filepath.Join(dir, "fleet"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "fleet", "allowed_signers"), []byte("me namespaces=\"git\" ssh-ed25519 AAAA\n"), 0o644)
	lock := `{"nodes":{"root":{}}}`
	if fm != "" {
		lock = `{"nodes":{"fleet-manager":{"locked":{"rev":"` + fm + `"}},"root":{}}}`
	}
	_ = os.WriteFile(filepath.Join(dir, "flake.lock"), []byte(lock), 0o644)
	return dir
}

// prepRepo answers like a checkout of the inventory at pinnedINV, signed and
// pushed, with fleet-manager signed too; fail names a check to fail.
func prepRepo(url, fail string) func(engine.Spec) (engine.Result, error) {
	return func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		no := func() (engine.Result, error) { return engine.Result{Code: 1}, errors.New("exited with code 1") }
		switch {
		case s.Name == "git" && strings.Contains(args, "rev-parse HEAD"):
			return engine.Result{Stdout: pinnedINV + "\n"}, nil
		case s.Name == "git" && strings.HasSuffix(args, "verify-commit HEAD"):
			if fail == "config" {
				return no()
			}
		case s.Name == "git" && strings.HasSuffix(args, "verify-commit "+pinnedFM):
			if fail == "fleet-manager" {
				return no()
			}
		case s.Name == "git" && strings.Contains(args, "branch -r --contains HEAD"):
			if fail == "push" {
				return engine.Result{}, nil
			}
			return engine.Result{Stdout: "  origin/main\n"}, nil
		case s.Name == "git" && strings.Contains(args, "remote get-url origin"):
			return engine.Result{Stdout: url + "\n"}, nil
		case s.Name == "git" && strings.Contains(args, ":bootstrap"):
			if fail == "old" {
				return engine.Result{Stdout: "#!/bin/sh\necho old\n"}, nil
			}
			return engine.Result{Stdout: prepBootstrap}, nil
		case s.Name == "nix" && strings.Contains(args, "conf.fleet.repo.url"):
			return engine.Result{Stdout: url}, nil
		}
		return engine.Result{}, nil
	}
}

func runPrep(t *testing.T, flake string, respond func(engine.Spec) (engine.Result, error)) (error, *engine.Fake, string) {
	t.Helper()
	x, fake, out := newFakeCtx(respond)
	err := engine.RunWith(x, InstallPrep, map[string]string{"flake": flake, "host": "nas"})
	return err, fake, out.String()
}

func bootstrapSum() string {
	sum := sha256.Sum256([]byte(prepBootstrap))
	return hex.EncodeToString(sum[:])
}

// A private configuration: the line pins the inventory's commit, and the
// fleet-manager commit its flake.lock pins, and checks bootstrap's sum.
func TestPrepPrintsTheLineForAPrivateHost(t *testing.T) {
	err, fake, out := runPrep(t, prepCheckout(t, pinnedFM), prepRepo("git@github.com:example-org/inventory.git", ""))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"curl -sSfL https://raw.githubusercontent.com/nnishant776/fleet-manager/" + pinnedFM + "/bootstrap -o /tmp/bootstrap",
		"echo '" + bootstrapSum() + "  /tmp/bootstrap' | sha256sum -c",
		"REV=" + pinnedFM + " FLAKE='git+ssh://git@github.com/example-org/inventory.git?rev=" + pinnedINV + "' sh /tmp/bootstrap nas",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the line lacks %q:\n%s", want, out)
		}
	}
	cmds := strings.Join(fake.Commands(), "\n")
	if !strings.Contains(cmds, "fetch -q --depth=1 https://github.com/nnishant776/fleet-manager "+pinnedFM) ||
		!strings.Contains(cmds, "verify-commit "+pinnedFM) {
		t.Errorf("did not check fleet-manager's commit:\n%s", cmds)
	}
}

// fleet-manager's own checkout: one commit, bootstrap's default flake.
func TestPrepPrintsTheLineForAPublicHost(t *testing.T) {
	err, _, out := runPrep(t, prepCheckout(t, ""), func(s engine.Spec) (engine.Result, error) {
		if s.Name == "git" && strings.Contains(strings.Join(s.Args, " "), "rev-parse HEAD") {
			return engine.Result{Stdout: pinnedFM + "\n"}, nil
		}
		return prepRepo("https://github.com/nnishant776/fleet-manager.git", "")(s)
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "&& REV="+pinnedFM+" sh /tmp/bootstrap nas") || strings.Contains(out, "FLAKE=") {
		t.Errorf("the line is not bootstrap's default at the commit:\n%s", out)
	}
}

func TestPrepRefusesWhatIsNotCheckedOrPushed(t *testing.T) {
	for fail, want := range map[string]string{
		"config":        "is not signed by a key you trust",
		"push":          "push it, since the installer fetches it",
		"fleet-manager": "which flake.lock pins, is not signed by a key you trust",
		"old":           "predates pinned installs",
	} {
		err, _, out := runPrep(t, prepCheckout(t, pinnedFM), prepRepo("git@github.com:example-org/inventory.git", fail))
		if err == nil || !strings.Contains(out, want) {
			t.Errorf("%s: went ahead, or without saying %q: %v\n%s", fail, want, err, out)
		}
		if strings.Contains(out, "sh /tmp/bootstrap") {
			t.Errorf("%s: printed the line anyway", fail)
		}
	}
}

// A checkout of another repository than the one the host installs from.
func TestPrepRefusesAnotherRepository(t *testing.T) {
	checkout := prepCheckout(t, pinnedFM)
	// Even with keys trusted for the fork, it is not where the host installs from.
	trustCopies(t, "git@github.com:someone/fork.git", fleet.UpstreamURL)
	err, _, out := runPrep(t, checkout, func(s engine.Spec) (engine.Result, error) {
		if s.Name == "git" && strings.Contains(strings.Join(s.Args, " "), "remote get-url origin") {
			return engine.Result{Stdout: "git@github.com:someone/fork.git\n"}, nil
		}
		return prepRepo("git@github.com:example-org/inventory.git", "")(s)
	})
	if err == nil || !strings.Contains(out, "run this in a checkout of that") {
		t.Fatalf("went ahead in a checkout of another repository: %v\n%s", err, out)
	}
}

// withPrep answers origin for the checkout's remote and url for where the
// host syncs from, "" for none, as an example or template host.
func withPrep(respond func(engine.Spec) (engine.Result, error), origin, url string) func(engine.Spec) (engine.Result, error) {
	return func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "git" && strings.Contains(args, "remote get-url origin"):
			return engine.Result{Stdout: origin + "\n"}, nil
		case s.Name == "nix" && strings.Contains(args, "conf.fleet.repo.url"):
			return engine.Result{Stdout: url}, nil
		case s.Name == "git" && strings.Contains(args, "rev-parse HEAD") && origin == fleetManagerGit:
			return engine.Result{Stdout: pinnedFM + "\n"}, nil
		}
		return respond(s)
	}
}

const fleetManagerGit = "https://github.com/nnishant776/fleet-manager.git"

// A template host of fleet-manager's own checkout syncs from nowhere; it is
// installed from fleet-manager at the commit.
func TestPrepPrintsTheLineForATemplateHost(t *testing.T) {
	err, _, out := runPrep(t, prepCheckout(t, ""), withPrep(prepRepo("", ""), fleetManagerGit, ""))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "&& REV="+pinnedFM+" sh /tmp/bootstrap nas") {
		t.Errorf("no line:\n%s", out)
	}
}

func TestPrepRefusesWhereTheInstallerCouldNotFetchIt(t *testing.T) {
	for name, c := range map[string]struct {
		fm, origin, url, want string
	}{
		"a fork of fleet-manager":     {"", "https://github.com/someone/fleet-manager.git", "", "from where the installer fetches fleet-manager's commits"},
		"a host naming no repository": {pinnedFM, "git@github.com:example-org/inventory.git", "", "names no repository to sync from"},
	} {
		respond := withPrep(prepRepo("", ""), c.origin, c.url)
		if c.fm == "" {
			// fleet-manager's own checkout: one commit.
			inner := respond
			respond = func(s engine.Spec) (engine.Result, error) {
				if s.Name == "git" && strings.Contains(strings.Join(s.Args, " "), "rev-parse HEAD") {
					return engine.Result{Stdout: pinnedFM + "\n"}, nil
				}
				return inner(s)
			}
		}
		checkout := prepCheckout(t, c.fm)
		trustCopies(t, c.origin, fleet.UpstreamURL)
		err, _, out := runPrep(t, checkout, respond)
		if err == nil || !strings.Contains(out, c.want) {
			t.Errorf("%s: went ahead, or without saying %q: %v\n%s", name, c.want, err, out)
		}
	}
}

// Commits are checked against your copy of the keys, never the checkout's own
// fleet/allowed_signers, which a pushed commit could have changed.
func TestPrepChecksAgainstYourCopyNotTheCheckouts(t *testing.T) {
	checkout := prepCheckout(t, pinnedFM)
	err, fake, out := runPrep(t, checkout, prepRepo("git@github.com:example-org/inventory.git", ""))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	state := os.Getenv("XDG_STATE_HOME")
	for _, s := range ranMatching(fake, "verify-commit") {
		args := strings.Join(s.Args, " ")
		if !strings.Contains(args, "gpg.ssh.allowedSignersFile="+state+"/fleet/repos/github.com/") {
			t.Errorf("checked against something else than your copy: %s", s)
		}
		if strings.Contains(args, checkout) && strings.Contains(args, "allowedSignersFile="+checkout) {
			t.Errorf("checked against the checkout's own file: %s", s)
		}
	}
}

func TestPrepStopsWithoutYourCopy(t *testing.T) {
	checkout := prepCheckout(t, pinnedFM)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	err, fake, out := runPrep(t, checkout, prepRepo("git@github.com:example-org/inventory.git", ""))
	if err == nil || !strings.Contains(out, "you trust no keys for github.com/example-org/inventory yet") ||
		!strings.Contains(out, "fleet trust signers --flake "+checkout) {
		t.Fatalf("went ahead without your copy: %v\n%s", err, out)
	}
	if len(ranMatching(fake, "verify-commit")) > 0 {
		t.Error("checked a commit without your copy")
	}
}
