package flows

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/secrets"
)

// Install guides through os-install on the installer: it checks the network,
// picks the host and the disks, explains what os-install will ask, runs it,
// and checks what landed on the new disk. os-install does the work.
var Install = &engine.Flow{
	Path:  []string{"install"},
	Title: "Install a host on this machine",
	Short: "Install a host from the configuration onto this machine's disks, from the installer",
	Long: "Run on a machine booted from the NixOS installer. fleet checks the network,\n" +
		"helps pick the host and the disks to erase, says what os-install will ask for,\n" +
		"then runs os-install, which erases the disks, installs, and creates the host's\n" +
		"identity when it fetches secrets. Afterwards it checks the new disk and lists\n" +
		"what follows the first boot. The flags are os-install's.",
	Example: "  fleet install\n" +
		"  fleet install -H laptop1 -d /dev/nvme0n1\n" +
		"  fleet install -H nas1 -d main=/dev/nvme0n1 -d data=/dev/sda --enroll-out /media/usb/nas1.enroll",
	Args: []string{"host"},
	Inputs: []engine.Input{
		fleet.FlakeInput,
		{Name: "host", Short: "H", Prompt: "Which host?", Help: "The host to install (hosts/<name>); chosen from a list when not given",
			Pattern: secrets.NamePattern, Optional: true},
		{Name: "disk", Short: "d", Prompt: "Disks", Help: "[NAME=]DEVICE for a disk of the host's layout, as os-install takes it; chosen from a list when not given",
			Many: true, Optional: true},
		{Name: "identity", Prompt: "Identity to carry over", Help: "An identity document from 'fleet-identity --export' on the old install, instead of a new identity",
			Optional: true},
		{Name: "no-enroll", Prompt: "Skip waiting for the enrollment?", Help: "Do not wait for the host to be enrolled; it retries after boot", Bool: true},
		{Name: "enroll-out", Prompt: "Also copy the enrollment file to", Help: "A path, for example on a mounted USB stick",
			Optional: true},
	},
	Needs: []string{"nix"},
	Steps: []engine.Step{
		{
			Title: "Check the network",
			Why:   "Everything comes over the network: the configuration from {{.flake}}, and the packages from the binary cache. A cable usually works by itself; Wi-Fi needs joining.",
			Do:    engine.Func(checkNetwork),
		},
		{
			Title: "Choose the host",
			Why:   "Each directory under hosts/ is one machine's configuration, and the name it will have.",
			When: func(x *engine.Ctx) (bool, string) {
				if x.Value("host") != "" {
					return false, "given: " + x.Value("host")
				}
				return true, ""
			},
			Do: engine.Func(chooseHost),
		},
		{
			Title: "Look at {{.host}}",
			Why:   "What os-install will do: the disks the layout names, whether they are encrypted, and whether the host needs enrolling. os-install reads the same again.",
			Do:    engine.Func(readHostFacts),
		},
		{
			Title: "Choose the disks",
			Why:   "Each disk the layout names needs a device on this machine. Everything on those devices is erased. The disk the installer runs from is never offered.",
			Do:    engine.Func(chooseDisks),
		},
		{
			Title: "Install {{.host}}",
			Why:   "os-install does the rest, and asks for what it needs: the host name typed out before it erases anything, and the disk passphrase twice when the disks are encrypted. It partitions, formats and mounts the disks at /mnt, installs onto them, and creates the host's identity when it fetches secrets.",
			Do:    engine.Func(runOSInstall),
		},
		{
			Title: "Check the new disk",
			Why:   "What is on it before the reboot: the installed system and, for a host that fetches secrets, its identity.",
			Do:    engine.Func(checkInstalled),
		},
		{
			Title: "Reboot",
			Why:   "The machine is ready to start from its own disk.",
			Do:    engine.Func(afterInstall),
		},
	},
}

func checkNetwork(x *engine.Ctx) error {
	for {
		_, err := fleet.Nix("flake", "metadata", "{{.flake}}").ReadOnly().Quiet().OK("{{.flake}} is reachable").Run(x)
		if err == nil {
			return nil
		}
		x.UI.Text("To connect:")
		x.UI.Text("  a cable: plug it in; the address comes by DHCP within seconds")
		if _, err := x.LookPath("nmtui"); err == nil {
			x.UI.Text("  Wi-Fi: nmtui, then Activate a connection")
		} else {
			x.UI.Text(`  Wi-Fi: sudo systemctl start wpa_supplicant, then in wpa_cli:`)
			x.UI.Text(`    add_network, set_network 0 ssid "NAME", set_network 0 psk "PASSPHRASE", enable_network 0`)
		}
		if x.Options.Yes || x.Options.DryRun || !x.UI.Interactive() {
			return errors.New("no network: connect, then run fleet install again")
		}
		if err := engine.WaitFor(x, "Connected? Try again"); err != nil {
			return err
		}
	}
}

func chooseHost(x *engine.Ctx) error {
	hosts, err := fleet.Hosts(x)
	if err != nil {
		return err
	}
	if !x.UI.Interactive() {
		return errors.New("no host given: give -H HOST, one of " + strings.Join(hosts, ", "))
	}
	host, err := x.UI.Choose("Which host?", "hosts/<name> in "+x.Value("flake"), hosts)
	if err != nil {
		return err
	}
	x.Values["host"] = host
	x.UI.OK(host)
	return nil
}

// hostFacts is what the steps need from the host's configuration, in one
// evaluation; os-install reads the same. One line, as it is shown.
const hostFacts = `c: { disks = builtins.mapAttrs (_: d: d.device) c.disko.devices.disk; ` +
	`encrypted = c.boot.initrd.luks.devices != { }; secrets = c.conf.secrets.repo.url != null; ` +
	`statePath = c.conf.secrets.statePath; repo = c.conf.fleet.repo.url != null; tpm2 = c.conf.hardware.boot.tpm2Unlock; }`

type facts struct {
	Disks     map[string]*string `json:"disks"`
	Encrypted bool               `json:"encrypted"`
	Secrets   bool               `json:"secrets"`
	StatePath string             `json:"statePath"`
	Repo      bool               `json:"repo"`
	TPM2      bool               `json:"tpm2"`
}

func readHostFacts(x *engine.Ctx) error {
	res, err := fleet.Nix("eval", "--json", "{{.flake}}#nixosConfigurations.{{.host}}.config", "--apply", hostFacts).
		ReadOnly().Quiet().OK("read its configuration").Run(x)
	if err != nil {
		return err
	}
	var f facts
	if err := json.Unmarshal([]byte(res.Stdout), &f); err != nil {
		return fmt.Errorf("reading the host's facts: %w", err)
	}
	if len(f.Disks) == 0 {
		return fmt.Errorf("%s declares no disk layout: set conf.hardware.disk.enable, or add hosts/%s/disko-config.nix", x.Value("host"), x.Value("host"))
	}
	var names []string
	for name, dev := range f.Disks {
		names = append(names, name)
		layout := "no device in its layout"
		if dev != nil {
			layout = *dev + " in its layout"
		}
		x.UI.Text("disk " + name + ": " + layout)
	}
	slices.Sort(names)
	x.Values["disk_names"] = strings.Join(names, "\n")
	if f.Encrypted {
		x.UI.Text("encrypted: os-install asks for one passphrase, for every encrypted disk")
	}
	if f.Secrets {
		x.UI.Text("secrets: it gets an identity, and is enrolled during the install")
		x.Values["state_path"] = f.StatePath
	}
	if f.Repo {
		x.UI.Text("on first boot it clones its configuration into /etc/nixos and syncs from it")
		x.Values["repo"] = "true"
	}
	if f.TPM2 {
		x.Values["tpm2"] = "true"
	}
	return nil
}

// bootMedium is the disk the installer runs from, or "".
func bootMedium(x *engine.Ctx) string {
	src, err := engine.Silently(x, nil, "findmnt", "-no", "SOURCE", "/iso")
	if err != nil || src == "" {
		return ""
	}
	parent, err := engine.Silently(x, nil, "lsblk", "-no", "PKNAME", src)
	if err != nil || parent == "" {
		return ""
	}
	return "/dev/" + strings.Fields(parent)[0]
}

func chooseDisks(x *engine.Ctx) error {
	names := strings.Split(x.Value("disk_names"), "\n")
	chosen := map[string]string{}
	for _, d := range strings.Split(x.Value("disk"), "\n") {
		switch name, dev, named := strings.Cut(d, "="); {
		case d == "":
		case named && !slices.Contains(names, name):
			return fmt.Errorf("%s has no disk %q; its disks are %s", x.Value("host"), name, strings.Join(names, ", "))
		case named:
			chosen[name] = dev
		case len(names) != 1:
			return fmt.Errorf("%s has disks %s: name the one %s is for, as -d NAME=%s", x.Value("host"), strings.Join(names, ", "), d, d)
		default:
			chosen[names[0]] = d
		}
	}
	boot := bootMedium(x)
	for _, name := range names {
		if chosen[name] != "" {
			continue
		}
		if !x.UI.Interactive() {
			if x.Options.DryRun {
				chosen[name] = "<device for " + name + ">"
				continue
			}
			return fmt.Errorf("no device for disk %q: give -d %s=DEVICE", name, name)
		}
		list, err := engine.Silently(x, nil, "lsblk", "--nodeps", "--noheadings", "--exclude", "7,11", "--output", "PATH,SIZE,TRAN,MODEL")
		if err != nil {
			return fmt.Errorf("listing the disks: %w", err)
		}
		var options []string
		for _, line := range strings.Split(list, "\n") {
			f := strings.Fields(line)
			if len(f) == 0 || f[0] == boot || slices.Contains(mapValues(chosen), f[0]) {
				continue
			}
			options = append(options, strings.Join(f, "  "))
		}
		if len(options) == 0 {
			return fmt.Errorf("no disk left for %q", name)
		}
		choice, err := x.UI.Choose("Device for disk '"+name+"' of "+x.Value("host"), "Everything on it is erased.", options)
		if err != nil {
			return err
		}
		chosen[name] = strings.Fields(choice)[0]
	}
	var args []string
	for _, name := range names {
		x.UI.OK(name + " → " + chosen[name] + ", to be erased")
		args = append(args, name+"="+chosen[name])
	}
	x.Values["disks"] = strings.Join(args, "\n")
	return nil
}

func mapValues(m map[string]string) []string {
	var v []string
	for _, s := range m {
		v = append(v, s)
	}
	return v
}

func runOSInstall(x *engine.Ctx) error {
	args := []string{"run", "{{.flake}}#os-install", "--", "-H", "{{.host}}"}
	for _, d := range strings.Split(x.Value("disks"), "\n") {
		args = append(args, "-d", d)
	}
	if x.Options.Yes {
		args = append(args, "--yes")
	}
	if v := x.Value("identity"); v != "" {
		args = append(args, "--identity", v)
	}
	if x.Value("no-enroll") == "true" {
		args = append(args, "--no-enroll")
	}
	if v := x.Value("enroll-out"); v != "" {
		args = append(args, "--enroll-out", v)
	}
	if x.Value("state_path") != "" && x.Value("identity") == "" && x.Value("no-enroll") != "true" {
		x.UI.Text("When it shows the enrollment file and its QR code, on the maintainer's machine:")
		x.Say("  fleet secrets host-enroll {{.host}} --from <the file>")
		x.UI.Text("  add the deploy key it prints to the secrets repository, read-only")
		x.Say("  fleet secrets user-password <account> {{.host}}, for each account")
		x.UI.Text("  push the secrets repository; then press Enter here")
	}
	// Partly written disks cannot be put back; the note says how to start over.
	_, err := fleet.Nix(args...).Interactive().OK("{{.host}} is installed").Before(func(x *engine.Ctx) error {
		x.Kept("install", "the disks as os-install left them, perhaps partly written; to start over: "+
			"sudo umount -R /mnt; sudo swapoff -a; then fleet install again")
		return nil
	}).After(func(x *engine.Ctx) error {
		x.Kept("install", "")
		return nil
	}).Run(x)
	return err
}

func checkInstalled(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: nothing was installed")
		return nil
	}
	if _, err := engine.Cmd("readlink", "/mnt/nix/var/nix/profiles/system").RunQuiet(x); err != nil {
		return errors.New("no system on /mnt")
	}
	x.UI.OK("the system is on /mnt")
	if p := x.Value("state_path"); p != "" {
		// The state directory is root's alone.
		if _, err := engine.Cmd("sudo", "-n", "test", "-f", "/mnt"+p+"/age.key").RunQuiet(x); err != nil {
			return errors.New("no identity under /mnt" + p)
		}
		x.UI.OK("its identity is under /mnt" + p)
	}
	return nil
}

func afterInstall(x *engine.Ctx) error {
	x.UI.Text("Remove the installer medium, then: sudo reboot")
	if x.Value("repo") == "true" {
		x.UI.Text("With a network, it clones its configuration into /etc/nixos and syncs from it.")
	}
	if x.Value("tpm2") == "true" {
		x.UI.Text("To unlock with the TPM as well, run the systemd-cryptenroll line os-install printed.")
	}
	if x.Value("state_path") != "" {
		x.Say("If it was not enrolled during the install, from the maintainer's machine: fleet host enroll {{.host}}")
	}
	x.Say("Then, from the maintainer's machine: fleet host check {{.host}}")
	return nil
}
