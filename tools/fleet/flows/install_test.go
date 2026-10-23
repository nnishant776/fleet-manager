package flows

import (
	"errors"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

const nasFacts = `{"disks":{"main":"/dev/sda","data":null},"encrypted":true,"secrets":true,` +
	`"statePath":"/var/lib/fleet","repo":true,"tpm2":false}`

func installer(t *testing.T, online bool) func(engine.Spec) (engine.Result, error) {
	return func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name == "nix" && strings.Contains(args, "flake metadata"):
			if !online {
				return engine.Result{Code: 1, Stderr: "unable to download"}, errors.New("exited with code 1")
			}
		case s.Name == "nix" && strings.Contains(args, " eval --json github:x/y#nixosConfigurations.nas.config "):
			return engine.Result{Stdout: nasFacts}, nil
		case s.Name == "findmnt":
			return engine.Result{Code: 1}, errors.New("exited with code 1")
		}
		return engine.Result{}, nil
	}
}

func TestInstallRunsOSInstallWithTheChoices(t *testing.T) {
	x, fake, out := newFakeCtx(installer(t, true))
	err := engine.RunWith(x, Install, map[string]string{
		"flake": "github:x/y", "host": "nas", "disk": "data=/dev/vdb\nmain=/dev/vda", "enroll-out": "/media/usb/nas.enroll",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	want := "nix --extra-experimental-features 'nix-command flakes' run 'github:x/y#os-install' -- -H nas" +
		" -d data=/dev/vdb -d main=/dev/vda --yes --enroll-out /media/usb/nas.enroll"
	if !strings.Contains(cmds, want) {
		t.Errorf("os-install was not run as\n  %s\nbut:\n%s", want, cmds)
	}
	for _, w := range []string{"sudo -n test -f /mnt/var/lib/fleet/age.key", "readlink /mnt/nix/var/nix/profiles/system"} {
		if !strings.Contains(cmds, w) {
			t.Errorf("did not check %q", w)
		}
	}
	for _, w := range []string{"encrypted: os-install asks for one passphrase", "fleet secrets host-enroll nas --from",
		"✓ its identity is under /mnt/var/lib/fleet", "fleet host check nas"} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, out)
		}
	}
}

func TestInstallRefusesAnUnnamedDiskForTwo(t *testing.T) {
	x, fake, _ := newFakeCtx(installer(t, true))
	err := engine.RunWith(x, Install, map[string]string{"flake": "github:x/y", "host": "nas", "disk": "/dev/vda"})
	if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "-d NAME=/dev/vda") {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(strings.Join(fake.Commands(), "\n"), "os-install") {
		t.Error("os-install ran")
	}
}

func TestInstallWithoutNetworkSaysHowToConnect(t *testing.T) {
	x, fake, out := newFakeCtx(installer(t, false))
	err := engine.RunWith(x, Install, map[string]string{"flake": "github:x/y", "host": "nas"})
	if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "no network") {
		t.Fatalf("got %v", err)
	}
	if !strings.Contains(out.String(), "Wi-Fi:") || len(fake.Ran) != 1 {
		t.Errorf("ran %q:\n%s", fake.Commands(), out)
	}
}

func TestInstallDryRunNamesWhatIsMissing(t *testing.T) {
	x, fake, out := newFakeCtx(installer(t, true))
	x.Options = engine.Options{DryRun: true}
	if err := engine.RunWith(x, Install, map[string]string{"flake": "github:x/y", "host": "nas"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "-d 'data=<device for data>' -d 'main=<device for main>'") {
		t.Errorf("output:\n%s", out)
	}
	for _, s := range fake.Ran {
		if strings.Contains(s.String(), "os-install") {
			t.Errorf("a dry run ran %s", s)
		}
	}
}

const (
	pinnedFM  = "1111111111111111111111111111111111111111"
	pinnedINV = "2222222222222222222222222222222222222222"
)

// A private host's install is refused unless fleet install prep pinned both
// commits, or --unpinned asks for what main has now.
func TestInstallRefusesAnUnpinnedPrivateHost(t *testing.T) {
	x, fake, out := newFakeCtx(installer(t, true))
	err := engine.RunWith(x, Install, map[string]string{"flake": "git+ssh://git@github.com/example-org/inventory", "host": "nas", "disk": "main=/dev/vda"})
	if err == nil || !strings.Contains(out.String(), "run fleet install prep nas there") {
		t.Fatalf("installed an unpinned private host: %v\n%s", err, out)
	}
	if strings.Contains(strings.Join(fake.Commands(), "\n"), "os-install") {
		t.Error("os-install ran")
	}
}

func TestInstallsAnUnpinnedPrivateHostWhenAsked(t *testing.T) {
	x, fake, out := newFakeCtx(installer(t, true))
	err := engine.RunWith(x, Install, map[string]string{"flake": "git+ssh://git@github.com/example-org/inventory", "host": "nas", "disk": "main=/dev/vda", "unpinned": "true"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(strings.Join(fake.Commands(), "\n"), "run 'github:nnishant776/fleet-manager#os-install'") {
		t.Errorf("did not run main's os-install:\n%s", strings.Join(fake.Commands(), "\n"))
	}
}

// A public host's unpinned install goes ahead, with a warning.
func TestInstallWarnsAboutAnUnpinnedPublicHost(t *testing.T) {
	x, _, out := newFakeCtx(installer(t, true))
	err := engine.RunWith(x, Install, map[string]string{"flake": "github:x/y", "host": "nas", "disk": "data=/dev/vdb\nmain=/dev/vda"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out.String(), "Not pinned: this installs what main has now") {
		t.Errorf("no warning:\n%s", out)
	}
}

func TestInstallFromAPrivateConfiguration(t *testing.T) {
	x, fake, out := newFakeCtx(installer(t, true))
	flake := "git+ssh://git@github.com/example-org/inventory?rev=" + pinnedINV
	err := engine.RunWith(x, Install, map[string]string{"flake": flake, "rev": pinnedFM, "host": "nas", "disk": "main=/dev/vda"})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	// os-install from the pinned commit of fleet-manager, fetched with git.
	want := "nix --extra-experimental-features 'nix-command flakes' run 'git+https://github.com/nnishant776/fleet-manager?rev=" + pinnedFM + "#os-install' --" +
		" --flake '" + flake + "' -H nas -d main=/dev/vda --yes"
	if !strings.Contains(cmds, want) {
		t.Errorf("os-install was not run as\n  %s\nbut:\n%s", want, cmds)
	}
	for _, unwanted := range []string{"flake metadata " + flake, "nixosConfigurations"} {
		if strings.Contains(cmds, unwanted) {
			t.Errorf("read the private flake before the host was enrolled: %q in\n%s", unwanted, cmds)
		}
	}
	for _, w := range []string{"the configuration key to the configuration repository", "sudo -n test -f /mnt/var/lib/fleet/age.key"} {
		if !strings.Contains(out.String()+cmds, w) {
			t.Errorf("lacks %q:\n%s", w, out)
		}
	}
}

// Anywhere but the installer, fleet install stops before anything else, so
// a mistyped command cannot start an install on your own machine.
func TestInstallRunsOnlyOnTheInstaller(t *testing.T) {
	onInstaller = func() bool { return false }
	defer func() { onInstaller = func() bool { return true } }()
	x, fake, out := newFakeCtx(installer(t, true))
	err := engine.RunWith(x, Install, map[string]string{"flake": "github:x/y", "host": "nas", "disk": "data=/dev/vdb\nmain=/dev/vda"})
	if err == nil || !strings.Contains(out.String(), "this machine is not booted from the NixOS installer") {
		t.Fatalf("went ahead off the installer: %v\n%s", err, out)
	}
	if len(fake.Ran) > 0 {
		t.Errorf("ran %s off the installer", fake.Ran[0])
	}
}
