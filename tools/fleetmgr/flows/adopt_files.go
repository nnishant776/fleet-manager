package flows

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// machineFacts is what fleetmgr host adopt reads from a machine that runs no
// fleet configuration, checked, as its host's folder in the inventory is
// written from it.
type machineFacts struct {
	Host         string // the name of its folder, and conf.host.name
	Platform     string // nixpkgs.hostPlatform: x86_64-linux or aarch64-linux
	Boot         string // uefi or bios
	Loader       string // systemd-boot, grub or uboot
	GrubDevice   string // for grub on bios: the disk it is installed to
	Backend      string // networkmanager or networkd
	Timezone     string
	Locale       string
	StateVersion string
	Role         string
	MachineType  string
	Accounts     []machineAccount
	// HardwareConfig is nixos-generate-config's hardware configuration, as
	// the machine printed it.
	HardwareConfig string
	// Previous holds the files of the machine's /etc/nixos, by their path in
	// it, kept in previous/.
	Previous map[string][]byte
}

// machineAccount is a person's account on the machine.
type machineAccount struct {
	Name, Full string
	Privileged bool
	Groups     []string // supplementary groups, other than the privileged ones
	Password   bool     // has a password hash, which moves to the secrets
	Keys       bool     // has login keys, which move to the secrets
}

// fleetSettings are what the inventory says every host of it sets.
type fleetSettings struct {
	RepoURL     string // conf.fleet.repo.url
	SecretsURL  string // conf.secrets.repo.url
	SignersPath string // conf.fleet.signing.allowedSignersFile, from the host's folder
}

// privilegedGroups are the groups conf.users.accounts gives through
// privileged = true, and refuses in groups.
var privilegedGroups = map[string]bool{
	"wheel": true, "docker": true, "podman": true, "libvirtd": true, "networkmanager": true, "wpa_supplicant": true,
}

// signersFrom is the path from a host's folder, hostsDir/<host>, to the
// inventory's fleet/allowed_signers.
func signersFrom(hostsDir string) string {
	depth := len(strings.Split(path.Clean(hostsDir), "/")) + 1
	return strings.Repeat("../", depth) + "fleet/allowed_signers"
}

// accountsBlock is the accounts' declarations, indented for conf.
func accountsBlock(accounts []machineAccount) string {
	var b strings.Builder
	for _, a := range accounts {
		b.WriteString("    users.accounts." + nixAttr(a.Name) + " = {\n")
		b.WriteString("      fullName = " + nixString(a.Full) + ";\n")
		b.WriteString("      privileged = " + strconv.FormatBool(a.Privileged) + ";\n")
		if len(a.Groups) > 0 {
			quoted := make([]string, len(a.Groups))
			for i, g := range a.Groups {
				quoted[i] = nixString(g)
			}
			b.WriteString("      groups = [ " + strings.Join(quoted, " ") + " ];\n")
		}
		if a.Password {
			b.WriteString("      # The hash it had, from the secrets.\n")
			b.WriteString("      secrets.password = true;\n")
		} else {
			b.WriteString("      # It had no password: it stays locked, reached with a key.\n")
			b.WriteString("      secrets.password = false;\n")
		}
		if a.Keys {
			b.WriteString("      secrets.authorizedKeys = true;\n")
		}
		b.WriteString("    };\n")
	}
	return b.String()
}

// fleetBlock is the settings every host of the inventory has.
func fleetBlock(s fleetSettings) string {
	return "    # A fleet host: it updates itself from signed commits of this repository,\n" +
		"    # and takes its secrets, support's keys among them, from the secrets\n" +
		"    # repository.\n" +
		"    fleet = {\n" +
		"      repo.url = " + nixString(s.RepoURL) + ";\n" +
		"      signing.allowedSignersFile = " + s.SignersPath + ";\n" +
		"      support.keysFromSecrets = true;\n" +
		"    };\n" +
		"    secrets.repo.url = " + nixString(s.SecretsURL) + ";\n"
}

func header(f machineFacts, how string) string {
	return "# " + f.Host + ", adopted into the fleet by fleetmgr host adopt, from the\n" +
		"# machine as it ran then. " + how + "\n"
}

// skeletonDefault is a minimal default.nix for the machine, beside its
// hardware-configuration.nix.
func skeletonDefault(f machineFacts, s fleetSettings) string {
	var b strings.Builder
	b.WriteString(header(f, "Its own /etc/nixos is kept in previous/, for\n# reference only: nothing imports it."))
	b.WriteString("{ ... }:\n{\n")
	b.WriteString("  nixpkgs.hostPlatform = " + nixString(f.Platform) + ";\n")
	b.WriteString("  # The release it was installed with, which some services' data depends on.\n")
	b.WriteString("  system.stateVersion = " + nixString(f.StateVersion) + ";\n")
	if f.Loader == "grub" && f.Boot == "bios" {
		b.WriteString("  boot.loader.grub.device = " + nixString(f.GrubDevice) + ";\n")
	}
	if f.Loader == "grub" && f.Boot == "uefi" {
		b.WriteString("  boot.loader.grub = {\n    efiSupport = true;\n    device = \"nodev\";\n  };\n")
	}
	b.WriteString("\n  conf = {\n")
	b.WriteString("    host = {\n")
	b.WriteString("      name = " + nixString(f.Host) + ";\n")
	b.WriteString("      timezone = " + nixString(f.Timezone) + ";\n")
	b.WriteString("      locale = " + nixString(f.Locale) + ";\n")
	b.WriteString("    };\n")
	b.WriteString("    role = " + nixString(f.Role) + ";\n")
	b.WriteString("    machineType = " + nixString(f.MachineType) + ";\n")
	b.WriteString("    # Its disks stay as they were installed: hardware-configuration.nix names them.\n")
	b.WriteString("    hardware.disk.enable = false;\n")
	b.WriteString("    hardware.boot = {\n")
	b.WriteString("      mode = " + nixString(f.Boot) + ";\n")
	b.WriteString("      loader = " + nixString(f.Loader) + ";\n")
	b.WriteString("    };\n")
	b.WriteString("    networking.backend = " + nixString(f.Backend) + ";\n")
	b.WriteString(accountsBlock(f.Accounts))
	b.WriteString(fleetBlock(s))
	b.WriteString("  };\n}\n")
	return b.String()
}

// copiedDefault is a default.nix that imports the machine's own
// configuration, unchanged in previous/, and adds what the fleet needs.
func copiedDefault(f machineFacts, s fleetSettings) string {
	var b strings.Builder
	b.WriteString(header(f, "Its own configuration is in previous/, as\n# it was; this file adds only what the fleet needs."))
	b.WriteString("{ lib, ... }:\n{\n")
	b.WriteString("  imports = [ ./previous/configuration.nix ];\n\n")
	b.WriteString("  # Read by the fleet before anything is evaluated, so it is set here too.\n")
	b.WriteString("  nixpkgs.hostPlatform = " + nixString(f.Platform) + ";\n\n")
	b.WriteString("  conf = {\n")
	b.WriteString("    # The same as its own configuration's, which the fleet sets as well.\n")
	b.WriteString("    host = {\n")
	b.WriteString("      name = " + nixString(f.Host) + ";\n")
	b.WriteString("      timezone = " + nixString(f.Timezone) + ";\n")
	b.WriteString("      locale = " + nixString(f.Locale) + ";\n")
	b.WriteString("    };\n")
	b.WriteString("    role = " + nixString(f.Role) + ";\n")
	b.WriteString("    machineType = " + nixString(f.MachineType) + ";\n")
	b.WriteString("    # Its disks and its boot loader stay as its own configuration has them.\n")
	b.WriteString("    hardware.disk.enable = false;\n")
	b.WriteString("    hardware.boot.loader = null;\n")
	b.WriteString("    networking.backend = " + nixString(f.Backend) + ";\n")
	b.WriteString(accountsBlock(f.Accounts))
	b.WriteString(fleetBlock(s))
	b.WriteString("  };\n")
	// What a host with secrets refuses, set by the old configuration.
	names := []string{"root"}
	for _, a := range f.Accounts {
		names = append(names, a.Name)
	}
	b.WriteString("\n  # A host with secrets takes login keys and passwords from them only; the\n" +
		"  # old configuration's are put aside.\n")
	b.WriteString("  users.users = {\n")
	for _, n := range names {
		b.WriteString("    " + nixAttr(n) + " = {\n")
		b.WriteString("      openssh.authorizedKeys.keys = lib.mkForce [ ];\n")
		b.WriteString("      openssh.authorizedKeys.keyFiles = lib.mkForce [ ];\n")
		if n != "root" && passwordMoves(f, n) {
			for _, o := range []string{"hashedPassword", "initialPassword", "initialHashedPassword", "password"} {
				b.WriteString("      " + o + " = lib.mkForce null;\n")
			}
		}
		b.WriteString("    };\n")
	}
	b.WriteString("  };\n}\n")
	return b.String()
}

func passwordMoves(f machineFacts, name string) bool {
	for _, a := range f.Accounts {
		if a.Name == name {
			return a.Password
		}
	}
	return false
}

// adoptFiles is the host's folder, by path in it: default.nix, and either
// hardware-configuration.nix beside it (the skeleton) or nothing more, with
// the machine's own /etc/nixos in previous/ either way.
func adoptFiles(f machineFacts, s fleetSettings, copy bool) (map[string][]byte, error) {
	files := map[string][]byte{}
	for p, data := range f.Previous {
		clean := path.Clean(p)
		if path.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, "../") || clean == "." {
			return nil, fmt.Errorf("/etc/nixos holds %q, which is not a path inside it", p)
		}
		files["previous/"+clean] = data
	}
	if copy {
		if _, ok := f.Previous["configuration.nix"]; !ok {
			return nil, fmt.Errorf("%s's /etc/nixos has no configuration.nix to import", f.Host)
		}
		files["default.nix"] = []byte(copiedDefault(f, s))
		return files, nil
	}
	files["default.nix"] = []byte(skeletonDefault(f, s))
	files["hardware-configuration.nix"] = []byte(f.HardwareConfig)
	return files, nil
}

// sortedPaths are a folder's paths in order, for showing.
func sortedPaths(files map[string][]byte) []string {
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	return paths
}

// writeHostFolder writes the files under root, making the folders they need.
func writeHostFolder(root string, files map[string][]byte) error {
	for _, p := range sortedPaths(files) {
		dest := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, files[p], 0o644); err != nil {
			return err
		}
	}
	return nil
}
