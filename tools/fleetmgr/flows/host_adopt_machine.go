package flows

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/fleetmgr"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/secrets"
)

// fleetmgr host adopt has two cases. A personal machine of the inventory has
// its owner adopted (host_adopt.go). A machine the inventory does not have,
// which runs no fleet configuration, is taken in whole: this file. It reads
// the machine over SSH as its admin account, writes its host's folder into
// the inventory, from its own configuration copied as it is or from a
// skeleton, checks that it evaluates, commits, pushes and publishes it.

// adoptCase is "owner" for a personal machine of the inventory, and
// "machine" otherwise, worked out once from the flake.
func adoptCase(x *engine.Ctx) string {
	if c := x.Value("adopt_case"); c != "" {
		return c
	}
	host := x.Value("host")
	c := "machine"
	if regexp.MustCompile(secrets.NamePattern).MatchString(host) {
		out, err := engine.Silently(x, nil, "nix", "eval", "--json", x.Value("flake")+"#nixosConfigurations",
			"--apply", `cs: if cs ? "`+host+`" then cs."`+host+`".config.conf.fleet.personal.enable else null`)
		if err == nil && out == "true" {
			c = "owner"
		}
	}
	x.Values["adopt_case"] = c
	return c
}

// onlyFor leaves an input out unless the case is kase.
func onlyFor(kase string) func(*engine.Ctx) bool {
	return func(x *engine.Ctx) bool { return adoptCase(x) != kase }
}

// stepFor shows a step only in one case.
func stepFor(kase string, s engine.Step) engine.Step {
	s.Hide = onlyFor(kase)
	return s
}

// machineInputs are the inputs of the machine case.
var machineInputs = []engine.Input{
	{Name: "admin", Prompt: "Admin account", Help: "An account with sudo on the machine, to read it as; admin by default",
		Default: func(*engine.Ctx) (string, error) { return "admin", nil }, Skip: onlyFor("machine")},
	{Name: "key", Prompt: "Key that logs in to that account",
		Help: "A private key file; the only key the agent fleetmgr starts holds and forwards",
		Default: func(*engine.Ctx) (string, error) {
			home, _ := os.UserHomeDir()
			if p := filepath.Join(home, ".ssh", "id_ed25519"); fileExists(p) {
				return p, nil
			}
			return "", nil
		}, Skip: onlyFor("machine")},
	withSkip(fleetmgr.HostKeyInput, onlyFor("machine")),
	withSkip(fleetmgr.AdminPasswordInput, onlyFor("machine")),
	{Name: "copy-config", Prompt: "Copy its configuration as it is?", Bool: true, Skip: onlyFor("machine"),
		Help: "Start the host's folder from the machine's own /etc/nixos, unchanged in previous/, which a default.nix imports"},
	{Name: "skeleton", Prompt: "Start from a skeleton?", Bool: true, Skip: onlyFor("machine"),
		Help: "Start the host's folder from a minimal default.nix and the machine's hardware-configuration.nix; its /etc/nixos is kept in previous/, for reference"},
	{Name: "role", Prompt: "Its role", Optional: true, Skip: onlyFor("machine"), Pattern: `^(` + strings.Join(adoptRoles, "|") + `)$`,
		Help: "conf.role: " + strings.Join(adoptRoles, ", ") + "; guessed from the machine when not given"},
	{Name: "machine-type", Prompt: "Its machine type", Optional: true, Skip: onlyFor("machine"), Pattern: `^(` + strings.Join(adoptMachineTypes, "|") + `)$`,
		Help: "conf.machineType: " + strings.Join(adoptMachineTypes, ", ") + "; guessed from the machine when not given"},
	{Name: "grub-device", Prompt: "Disk GRUB is installed to", Optional: true, Skip: onlyFor("machine"), Pattern: `^/dev/[A-Za-z0-9/_.:-]+$`,
		Help: "For a machine that boots GRUB with BIOS; by default the disk that holds /boot, or /, by its /dev/disk/by-id name"},
	{Name: "secrets-address", Prompt: "The secrets repository's address", Optional: true, Skip: onlyFor("machine"),
		Help: "conf.secrets.repo.url for the host; by default the one the inventory's hosts share"},
}

func withSkip(in engine.Input, skip func(*engine.Ctx) bool) engine.Input {
	in.Skip = skip
	return in
}

// adoptRoles and adoptMachineTypes are conf.role's and conf.machineType's
// values (modules/conf/role.nix, machineType.nix).
var (
	adoptRoles        = []string{"developer", "embedded", "gaming", "minimal", "server", "workstation"}
	adoptMachineTypes = []string{"laptop", "desktop", "headless", "vm"}
)

// machineSteps are the steps of the machine case.
var machineSteps = []engine.Step{
	{
		Title: "Check the inventory",
		Why: "The host is committed on top of the inventory's main, so the checkout must be clean, level with origin's main, and that commit signed by a key you trust. " +
			"Its layout says where hosts go. A host it has already, from an earlier run, is not written again.",
		Required: true,
		Do:       engine.Func(checkAdoptTarget),
	},
	knowHostKeyStep,
	{
		Title: "Start an agent that holds one key",
		Why:   "{{.admin}} logs in with it. Only this agent is forwarded, never yours, which holds your signing key.",
		Do:    engine.Func(fleetmgr.StartAgent),
	},
	adminSudoStep,
	{
		Title: "Read {{.host}}",
		Why: "What its folder is written from: the platform, how it boots, its network, timezone and locale, the release it was installed with, its hardware configuration, " +
			"its /etc/nixos, and the people's accounts, with their password hashes (kept in memory only, never shown) and login keys.",
		When: adoptWrites,
		Do:   engine.Func(readMachine),
	},
	{
		Title: "Choose how {{.host}}'s folder starts",
		Why: "Its own configuration, copied as it is, keeps everything it does; a skeleton keeps only what fleetmgr read, and the fleet's roles do the rest. " +
			"Either way its /etc/nixos goes into previous/.",
		When: adoptWrites,
		Do:   engine.Func(chooseAdoptStart),
	},
	{
		Title: "Write {{.host}}'s folder into the inventory",
		Why:   "Each file is shown first. If a later step fails, the folder stays, for you to look at or change; run this again to go on from it.",
		When:  adoptWrites,
		Do:    engine.Func(writeAdoptFolder),
	},
	{
		Title: "Check it evaluates in the inventory",
		Why: "The host is evaluated as the hosts will see it: the address it fetches, its branch, the signers file it uses, and that every host still evaluates. " +
			"An evaluation that fails says why, with a hint where one is known, and leaves the folder as it is.",
		When: unless("adopt_in_inventory", "it is in the inventory already"),
		Do:   engine.Func(checkAdopted),
	},
	{
		Title: "Commit it",
		Why:   "One commit that adds the host's folder. The repository's own git settings sign it.",
		When:  unless("adopt_in_inventory", "it is in the inventory already"),
		Do:    engine.Func(commitAdopted),
	},
	{
		Title: "Push it",
		Why:   "Puts the commit on origin's main. A push that is not a fast-forward is refused: then someone else pushed first. What you push is the commit you trust on main from then on.",
		When:  adoptAhead,
		Do:    engine.Func(pushImport),
	},
	{
		Title: "Publish its branch",
		Why:   "The host fetches its own branch, host/{{.host}}, not main. Only that branch is cut, checked to build from its own files alone, signed and pushed.",
		When: func(x *engine.Ctx) (bool, string) {
			if x.Value("imp_host_branches") != "true" {
				return false, "the inventory does not set hostBranches: its hosts fetch main"
			}
			return true, ""
		},
		Do: engine.Func(publishImport),
	},
}

// machineNextStep ends the machine case.
var machineNextStep = engine.Step{
	Title: "Next",
	Why:   "What {{.host}} is now, and what is left to you.",
	Do:    engine.Func(showAdoptMachineNext),
}

// adoptWrites is whether this run writes the host's folder: not when the
// inventory has it, or an earlier run wrote it and it is not committed yet.
func adoptWrites(x *engine.Ctx) (bool, string) {
	switch {
	case x.Value("adopt_in_inventory") == "true":
		return false, "it is in the inventory already"
	case x.Value("adopt_written") == "true":
		return false, "an earlier run wrote its folder, which is checked again as it is"
	}
	return true, ""
}

// adoptAhead is whether the checkout has a commit origin's main lacks.
func adoptAhead(x *engine.Ctx) (bool, string) {
	if x.Options.DryRun && x.Value("adopt_in_inventory") != "true" {
		return true, ""
	}
	out, err := engine.Silently(x, nil, "git", "-C", x.Value("flake"), "rev-list", "--count", "refs/remotes/origin/main..HEAD")
	if err == nil && out == "0" {
		return false, "origin's main has it"
	}
	return true, ""
}

func checkAdoptTarget(x *engine.Ctx) error {
	if err := checkInventoryCheckout(x); err != nil {
		return err
	}
	res, err := engine.Cmd("nix", "eval", "--json", "{{.flake}}#fleetLayout").ReadOnly().Quiet().OK("read the inventory's layout").Run(x)
	if err != nil {
		return err
	}
	var l layout
	if err := json.Unmarshal([]byte(res.Stdout), &l); err != nil {
		return fmt.Errorf("fleetLayout: %w", err)
	}
	hosts, err := cleanLayoutDir("hosts", l.Hosts)
	if err != nil {
		return err
	}
	x.Values["imp_hosts_dir"], x.Values["imp_host_branches"] = hosts, fmt.Sprint(l.HostBranches)
	host := x.Value("host")
	rel := hosts + "/" + host
	x.Values["imp_paths"], x.Values["imp_kind"] = rel, "nixos"
	x.Values["adopt_in_inventory"], x.Values["adopt_written"] = "false", "false"
	tracked, _ := engine.Silently(x, nil, "git", "-C", x.Value("flake"), "ls-files", "--", rel)
	switch {
	case tracked != "":
		x.Values["adopt_in_inventory"] = "true"
		x.UI.OK(host + " is in the inventory already, in " + rel)
	case fileExists(filepath.Join(x.Value("flake"), filepath.FromSlash(rel), "default.nix")):
		x.Values["adopt_written"] = "true"
		x.UI.OK(rel + " was written by an earlier run, and is not committed: it is checked again as it is")
	default:
		x.UI.OK(host + " is not in the inventory yet; it goes to " + rel)
	}
	return nil
}

var (
	adoptTimezone = regexp.MustCompile(`^[A-Za-z0-9_+/-]+$`)
	adoptLocale   = regexp.MustCompile(`^[A-Za-z0-9_.@-]+$`)
	adoptRelease  = regexp.MustCompile(`^[0-9]{2}\.[0-9]{2}$`)
	adoptDevice   = regexp.MustCompile(`^/dev/[A-Za-z0-9/_.:-]+$`)
	adoptGroup    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

// machineFactsScript prints, one key=value a line, what the machine's folder
// is written from. Its answers are checked one by one: the host is not
// trusted yet.
const machineFactsScript = `set -u
echo "arch=$(uname -m)"
[ -e /etc/NIXOS ] && echo nixos=true || echo nixos=false
[ -d /sys/firmware/efi ] && echo boot=uefi || echo boot=bios
if sudo -n test -e /boot/loader/loader.conf; then echo loader=systemd-boot
elif sudo -n test -e /boot/extlinux/extlinux.conf; then echo loader=uboot
elif sudo -n test -d /boot/grub; then echo loader=grub
else echo loader=; fi
systemctl is-active --quiet NetworkManager && echo backend=networkmanager || echo backend=networkd
echo "timezone=$(timedatectl show -p Timezone --value 2>/dev/null)"
echo "locale=$(sed -n 's/^LANG=//p' /etc/locale.conf 2>/dev/null | tr -d '"' | head -1)"
echo "release=$(sudo -n grep -rhoE 'system\.stateVersion *= *"[0-9]+\.[0-9]+"' /etc/nixos 2>/dev/null | head -1 | grep -oE '[0-9]+\.[0-9]+')"
systemctl is-active --quiet display-manager && echo graphical=true || echo graphical=false
echo "virt=$(systemd-detect-virt 2>/dev/null || true)"
echo "chassis=$(hostnamectl chassis 2>/dev/null || true)"
if sudo -n test -e /etc/nixos/flake.nix; then echo etc=flake
elif sudo -n test -e /etc/nixos/configuration.nix; then echo etc=configuration
else echo etc=none; fi
src=$(findmnt -no SOURCE /boot 2>/dev/null || findmnt -no SOURCE / 2>/dev/null)
disk=$(lsblk -no PKNAME "$src" 2>/dev/null | head -1)
if [ -n "$disk" ]; then
  for l in /dev/disk/by-id/*; do
    case "$l" in *-part*|*/wwn-*) continue ;; esac
    if [ "$(readlink -f "$l")" = "/dev/$disk" ]; then echo "grub_device=$l"; break; fi
  done
  echo "grub_disk=/dev/$disk"
fi
getent passwd | while IFS=: read -r name _ uid _ gecos home shell; do
  [ "$uid" -ge 1000 ] && [ "$uid" -lt 60000 ] || continue
  case "$shell" in */nologin|*/false) continue ;; esac
  echo "account=$name:$(printf '%s' "$gecos" | cut -d, -f1 | tr ':' ' '):$home:$(id -nG "$name" | tr ' ' ',')"
done`

func readMachine(x *engine.Ctx) error {
	res, err := fleetmgr.OnAdmin(machineFactsScript).ReadOnly().Quiet().OK("read what it is").Run(x)
	if err != nil {
		return err
	}
	facts := map[string]string{}
	type acct struct{ name, full, home, groups string }
	var accounts []acct
	for _, line := range strings.Split(res.Stdout, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		if k == "account" {
			f := strings.SplitN(v, ":", 4)
			if len(f) == 4 {
				accounts = append(accounts, acct{f[0], f[1], f[2], f[3]})
			}
			continue
		}
		facts[k] = v
	}
	host := x.Value("host")
	if facts["nixos"] != "true" {
		return fmt.Errorf("%s does not run NixOS (it has no /etc/NIXOS): only a NixOS machine can be adopted this way", host)
	}
	f := machineFacts{Host: host}
	switch facts["arch"] {
	case "x86_64":
		f.Platform = "x86_64-linux"
	case "aarch64":
		f.Platform = "aarch64-linux"
	default:
		return fmt.Errorf("%s is a %q machine, which the fleet does not build for", host, facts["arch"])
	}
	f.Boot = map[string]string{"uefi": "uefi"}[facts["boot"]]
	if f.Boot == "" {
		f.Boot = "bios"
	}
	// What only a skeleton needs, its loader, GRUB's disk and the release, is
	// checked once the skeleton is chosen: a copy keeps the machine's own.
	if f.Loader = facts["loader"]; !slices.Contains([]string{"systemd-boot", "grub", "uboot"}, f.Loader) {
		f.Loader = ""
	}
	if f.Loader == "grub" && f.Boot == "bios" {
		for _, d := range []string{x.Value("grub-device"), facts["grub_device"], facts["grub_disk"]} {
			if adoptDevice.MatchString(d) {
				f.GrubDevice = d
				break
			}
		}
	}
	f.Backend = map[string]string{"networkmanager": "networkmanager"}[facts["backend"]]
	if f.Backend == "" {
		f.Backend = "networkd"
	}
	if f.Timezone = facts["timezone"]; !adoptTimezone.MatchString(f.Timezone) {
		f.Timezone = "UTC"
		x.UI.Note("Its timezone could not be read; UTC is written, which you can change.")
	}
	if f.Locale = facts["locale"]; !adoptLocale.MatchString(f.Locale) {
		f.Locale = "en_US.UTF-8"
		x.UI.Note("Its locale could not be read; en_US.UTF-8 is written, which you can change.")
	}
	if f.StateVersion = facts["release"]; !adoptRelease.MatchString(f.StateVersion) {
		f.StateVersion = ""
	}
	f.Role, f.MachineType = guessRole(facts), guessMachineType(facts)
	x.Values["adopt_etc"] = facts["etc"]

	for _, a := range accounts {
		if !ownerNamePattern.MatchString(a.name) || !homePattern.MatchString(a.home) {
			x.UI.Skip("left out the account " + strconv.Quote(a.name) + ", whose name or home is not a plain one")
			continue
		}
		if !namePattern.MatchString(a.name) {
			return fmt.Errorf("%s has the account %s, which cannot be a user in the secrets repository, whose names are lowercase letters, digits and '-'", host, a.name)
		}
		m := machineAccount{Name: a.name, Full: a.full}
		for _, g := range strings.Split(a.groups, ",") {
			switch {
			case g == "wheel":
				m.Privileged = true
			case privilegedGroups[g], g == a.name, g == "users", g == "":
			case adoptGroup.MatchString(g):
				m.Groups = append(m.Groups, g)
			}
		}
		sort.Strings(m.Groups)
		hash, err := readShadowHash(x, a.name)
		if err != nil {
			return err
		}
		if hash != "" {
			m.Password = true
			x.Values["adopt_hash_"+a.name] = hash
		}
		keys, err := readAuthorizedKeys(x, a.name, a.home)
		if err != nil {
			return err
		}
		if len(keys) > 0 {
			m.Keys = true
			x.Values["adopt_keys_"+a.name] = strings.Join(keys, "\n")
		}
		f.Accounts = append(f.Accounts, m)
	}
	if len(f.Accounts) == 0 {
		return fmt.Errorf("%s has no person's account (uid 1000 and up, with a login shell) to bring along", host)
	}

	hw, err := fleetmgr.OnAdmin("sudo -n nixos-generate-config --show-hardware-config 2>/dev/null").ReadOnly().Quiet().
		OK("read its hardware configuration").Run(x)
	if err != nil {
		return fmt.Errorf("nixos-generate-config --show-hardware-config on %s: %w", host, err)
	}
	f.HardwareConfig = hw.Stdout
	f.Previous = map[string][]byte{}
	if facts["etc"] != "none" {
		tarball, err := fleetmgr.OnAdmin("sudo -n tar -h -C /etc/nixos --exclude=./.git --exclude=./result -cf - .").ReadOnly().Quiet().
			OK("read its /etc/nixos").Run(x)
		if err != nil {
			return fmt.Errorf("reading %s's /etc/nixos: %w", host, err)
		}
		if f.Previous, err = untarFiles([]byte(tarball.Stdout)); err != nil {
			return fmt.Errorf("%s's /etc/nixos: %w", host, err)
		}
	}
	adoptMachine = f

	x.UI.OK(fmt.Sprintf("a NixOS %s machine, booting with %s, %s; network by %s", f.Platform, strings.ToUpper(f.Boot), f.Loader, f.Backend))
	x.UI.OK("timezone " + f.Timezone + ", locale " + f.Locale + ", installed with " + f.StateVersion)
	for _, a := range f.Accounts {
		what := "an ordinary account"
		if a.Privileged {
			what = "an administrator"
		}
		pw := "no password"
		if a.Password {
			pw = "a password hash (not shown)"
		}
		keys := "no login keys"
		if a.Keys {
			keys = fmt.Sprintf("%d login key(s)", strings.Count(x.Value("adopt_keys_"+a.Name), "\n")+1)
		}
		x.UI.OK(fmt.Sprintf("%s, %s: %s, %s", a.Name, what, pw, keys))
	}
	x.UI.OK(fmt.Sprintf("its /etc/nixos holds %d file(s): %s", len(f.Previous), map[string]string{
		"flake": "a flake", "configuration": "a configuration.nix", "none": "nothing to build from"}[facts["etc"]]))
	return nil
}

// adoptMachine is what readMachine found, for the steps after it in the same
// run. Only one flow runs in a process.
var adoptMachine machineFacts

func readShadowHash(x *engine.Ctx, name string) (string, error) {
	if _, err := fleetmgr.OnAdmin("sudo -n getent shadow " + name).CaptureSecret("adopt_shadow").ReadOnly().Quiet().Run(x); err != nil {
		return "", fmt.Errorf("reading %s's password hash (sudo getent shadow): %w", name, err)
	}
	line := x.Value("adopt_shadow")
	delete(x.Values, "adopt_shadow")
	sf := strings.Split(line, ":")
	if len(sf) < 2 || sf[0] != name {
		return "", fmt.Errorf("unexpected answer of getent shadow for %s", name)
	}
	hash := sf[1]
	if hash == "" || strings.HasPrefix(hash, "!") || strings.HasPrefix(hash, "*") || !shadowPattern.MatchString(hash) {
		return "", nil
	}
	return hash, nil
}

// readAuthorizedKeys reads an account's login keys from where sshd looks on
// NixOS: its ~/.ssh/authorized_keys, and /etc/ssh/authorized_keys.d/<name>,
// where the keys its configuration declares are.
func readAuthorizedKeys(x *engine.Ctx, name, home string) ([]string, error) {
	res, err := fleetmgr.OnAdmin(fmt.Sprintf("for f in '%[1]s/.ssh/authorized_keys' /etc/ssh/authorized_keys.d/%[2]s; do "+
		"if sudo -n test -f \"$f\"; then sudo -n cat \"$f\"; echo; fi; done", home, name)).
		ReadOnly().Quiet().Run(x)
	if err != nil {
		return nil, fmt.Errorf("reading %s's login keys: %w", name, err)
	}
	lines, _ := keyLines(res.Stdout)
	var keys []string
	seen := map[string]bool{}
	for _, k := range lines {
		if m := keyMaterial(k); m != "" && !seen[m] {
			seen[m] = true
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// untarFiles takes the regular files of a tar, by their path in it. Links and
// anything else are left out: previous/ is for reading and importing.
func untarFiles(data []byte) (map[string][]byte, error) {
	files := map[string][]byte{}
	r := tar.NewReader(bytes.NewReader(data))
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(strings.TrimPrefix(h.Name, "./"))
		if name == "." || path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("it holds %q, which is not a path inside it", h.Name)
		}
		b, err := io.ReadAll(io.LimitReader(r, 4<<20))
		if err != nil {
			return nil, err
		}
		files[name] = b
	}
}

func guessRole(facts map[string]string) string {
	if facts["graphical"] == "true" {
		return "workstation"
	}
	return "server"
}

func guessMachineType(facts map[string]string) string {
	switch {
	case facts["virt"] != "" && facts["virt"] != "none":
		return "vm"
	case facts["chassis"] == "laptop" || facts["chassis"] == "convertible" || facts["chassis"] == "tablet":
		return "laptop"
	case facts["graphical"] == "true":
		return "desktop"
	}
	return "headless"
}

func chooseAdoptStart(x *engine.Ctx) error {
	copyGiven, skelGiven := x.Value("copy-config") == "true", x.Value("skeleton") == "true"
	etc := x.Value("adopt_etc")
	choice := ""
	switch {
	case copyGiven && skelGiven:
		return errors.New("give --copy-config or --skeleton, not both")
	case copyGiven && etc != "configuration":
		return fmt.Errorf("%s's /etc/nixos holds %s, not a configuration.nix that can be copied as it is: give --skeleton", x.Value("host"),
			map[string]string{"flake": "a flake, whose inputs are not the inventory's", "none": "nothing to build from"}[etc])
	case copyGiven:
		choice = "copy"
	case skelGiven:
		choice = "skeleton"
	case etc != "configuration":
		x.UI.Note(map[string]string{"flake": "Its /etc/nixos is a flake, whose inputs are not the inventory's, so it cannot be copied as it is",
			"none": "Its /etc/nixos holds nothing to build from"}[etc] + ": the folder starts from a skeleton, with /etc/nixos kept in previous/ for reference.")
		choice = "skeleton"
	case !x.UI.Interactive():
		return errors.New("give --copy-config, to copy its configuration as it is, or --skeleton, to start from a minimal one")
	default:
		const copyIt, skel = "Copy its configuration as it is", "Start from a minimal skeleton"
		got, err := x.UI.Choose("How does "+x.Value("host")+"'s folder start?",
			"A copy keeps everything its configuration does; a skeleton keeps only what fleetmgr read, and the fleet's roles do the rest.",
			[]string{copyIt, skel})
		if err != nil {
			return err
		}
		choice = map[string]string{copyIt: "copy", skel: "skeleton"}[got]
	}
	x.Values["adopt_start"] = choice
	if choice == "skeleton" {
		if err := skeletonReady(x.Value("host"), adoptMachine); err != nil {
			return err
		}
	}

	f := &adoptMachine
	for _, p := range []struct {
		input, guess string
		values       []string
		set          *string
		what         string
	}{
		{"role", f.Role, adoptRoles, &f.Role, "role"},
		{"machine-type", f.MachineType, adoptMachineTypes, &f.MachineType, "machine type"},
	} {
		v := x.Value(p.input)
		if v == "" && x.UI.Interactive() && !x.Options.Yes {
			opts := append([]string{p.guess}, slices.DeleteFunc(slices.Clone(p.values), func(s string) bool { return s == p.guess })...)
			var err error
			if v, err = x.UI.Choose("Its "+p.what+"?", "fleetmgr guesses "+p.guess+" from what the machine runs.", opts); err != nil {
				return err
			}
		}
		if v == "" {
			v = p.guess
			x.UI.Note("Its " + p.what + " is guessed: " + v + " (--" + p.input + " gives another).")
		}
		*p.set = v
	}
	x.UI.OK(map[string]string{"copy": "its configuration is copied as it is", "skeleton": "it starts from a skeleton"}[choice] +
		"; role " + f.Role + ", machine type " + f.MachineType)
	return nil
}

// adoptSecretsAddress is the secrets repository's address for the host:
// --secrets-address, or the one the inventory's hosts share.
func adoptSecretsAddress(x *engine.Ctx) (string, error) {
	if a := x.Value("secrets-address"); a != "" {
		return a, nil
	}
	res, err := engine.Cmd("nix", "eval", "--json", "{{.flake}}#nixosConfigurations",
		"--apply", "cs: builtins.mapAttrs (_: c: c.config.conf.secrets.repo.url) cs").ReadOnly().Quiet().Run(x)
	if err == nil {
		var urls map[string]*string
		if json.Unmarshal([]byte(res.Stdout), &urls) == nil {
			common := ""
			for _, u := range urls {
				switch {
				case u == nil || *u == "":
				case common == "":
					common = *u
				case common != *u:
					common = "-"
				}
			}
			if common != "" && common != "-" {
				x.UI.OK("the inventory's hosts take their secrets from " + common)
				return common, nil
			}
		}
	}
	return "", errors.New("the inventory's hosts share no secrets repository address: give it with --secrets-address")
}

func writeAdoptFolder(x *engine.Ctx) error {
	address, err := inventoryAddressOf(x)
	if err != nil {
		return err
	}
	secretsURL, err := adoptSecretsAddress(x)
	if err != nil {
		return err
	}
	x.Values["imp_address"] = address
	s := fleetSettings{RepoURL: address, SecretsURL: secretsURL, SignersPath: signersFrom(x.Value("imp_hosts_dir"))}
	files, err := adoptFiles(adoptMachine, s, x.Value("adopt_start") == "copy")
	if err != nil {
		return err
	}
	rel := x.Value("imp_paths")
	x.UI.Text("default.nix, as fleetmgr writes it:\n\n" + string(files["default.nix"]))
	var listed []string
	for _, p := range sortedPaths(files) {
		if p != "default.nix" {
			listed = append(listed, fmt.Sprintf("  %s (%d bytes, from the machine)", p, len(files[p])))
		}
	}
	x.UI.Text("Beside it, as the machine has them:\n" + strings.Join(listed, "\n"))
	x.UI.Note("The files from the machine go into a repository every host's evaluation reads. Look at them for anything that does not belong there, such as a password in clear text.")
	if ok, err := x.Approve(fmt.Sprintf("Write %d files into %s/%s?", len(files), x.Value("flake"), rel)); !ok || err != nil {
		return err
	}
	if err := writeHostFolder(filepath.Join(x.Value("flake"), filepath.FromSlash(rel)), files); err != nil {
		return err
	}
	x.Kept("adopt folder", rel+" in {{.flake}}, not committed: look at it, change it if it does not evaluate, and run this again to go on from it")
	x.UI.OK("wrote " + rel)
	return nil
}

// evalHint says, for an evaluation error the copied configuration often
// gives, what to do about it.
func evalHint(stderr string) string {
	switch {
	case strings.Contains(stderr, "has conflicting definition values"):
		return "the configuration in previous/ and the fleet's modules both set that option: take it out of previous/, or give default.nix a value with lib.mkForce"
	case strings.Contains(stderr, "was not found in the Nix search path"):
		return "previous/ reads <nixpkgs> or another channel, which a flake does not have: use the pkgs, lib and modulesPath the module is given instead"
	case strings.Contains(stderr, "access to absolute path") && strings.Contains(stderr, "forbidden in pure"):
		return "previous/ reads a file outside the inventory: copy it into the host's folder and read it from there"
	case strings.Contains(stderr, "Failed assertions"):
		return "the configuration fails one of its assertions, listed above"
	}
	return ""
}

func checkAdopted(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: nothing is written, so there is nothing to evaluate")
		return nil
	}
	if x.Value("imp_address") == "" {
		address, err := inventoryAddressOf(x)
		if err != nil {
			return err
		}
		x.Values["imp_address"] = address
	}
	err := checkImported(x)
	if err == nil {
		return nil
	}
	// The files stay, unstaged, for the person to change.
	_, _ = engine.Cmd("git", "-C", "{{.flake}}", "reset", "-q", "--", "{{.imp_paths}}").Quiet().ReadOnly().Run(x)
	msg := err.Error()
	if hint := evalHint(msg); hint != "" {
		msg += "\nHint: " + hint
	}
	return fmt.Errorf("%s\nThe folder %s stays as it is: change it in %s, then run this again", msg, x.Value("imp_paths"), x.Value("flake"))
}

func commitAdopted(x *engine.Ctx) error {
	x.Values["imp_subject"] = "feat(hosts): " + x.Value("host") + ", adopted from the machine as it ran"
	x.Values["imp_body"] = map[string]string{
		"copy":     "Its own configuration is in previous/, unchanged, imported by default.nix, which adds what the fleet needs.",
		"skeleton": "From a skeleton written by fleetmgr host adopt, with its hardware configuration; its /etc/nixos is kept in previous/.",
		"":         "Written by fleetmgr host adopt.",
	}[x.Value("adopt_start")] + " It fetches " + x.Value("imp_address") + "."
	if _, err := engine.Cmd("git", "-C", "{{.flake}}", "commit", "-q", "-m", "{{.imp_subject}}", "-m", "{{.imp_body}}", "--", "{{.imp_paths}}").
		OK("committed: {{.imp_subject}}").Run(x); err != nil {
		return err
	}
	if x.Options.DryRun {
		return nil
	}
	x.Kept("adopt folder", "")
	x.Kept("push import", "the commit “{{.imp_subject}}” in {{.flake}}, not pushed yet (git -C {{.flake}} push origin HEAD:refs/heads/main)")
	return nil
}

func showAdoptMachineNext(x *engine.Ctx) error {
	x.Kept("adopt folder", "")
	x.Kept("adopt secrets", "")
	host, flake := x.Value("host"), x.Value("flake_given")
	if flake == "" {
		flake = x.Value("flake")
	}
	if x.Value("switched") != "true" {
		if ok, _ := needsSwitch(x); !ok {
			x.UI.Text(host + " runs the fleet's configuration from the inventory already: there is nothing left to adopt.")
			return nil
		}
		x.UI.Text(host + " was not switched: run this again to go on from where it stopped.")
		return nil
	}
	x.UI.Text(host + " is a fleet host now. It runs host/" + host + " of the inventory, its secrets come from the secrets repository, " +
		"and its accounts kept their passwords and keys, which change through the secrets from now on: passwd on the machine no longer sticks.\n" +
		"What its old configuration did is in " + x.Value("imp_paths") + "/previous/ in the inventory, for reference. Check it at any time with:\n" +
		"  fleetmgr host check " + host + " --flake " + flake)
	return nil
}

// forCase shows steps only in one case.
func forCase(kase string, steps []engine.Step) []engine.Step {
	out := make([]engine.Step, len(steps))
	for i, s := range steps {
		out[i] = stepFor(kase, s)
	}
	return out
}

// joinInputs makes one list of a flow's inputs from its parts.
func joinInputs(parts ...[]engine.Input) []engine.Input {
	var all []engine.Input
	for _, p := range parts {
		all = append(all, p...)
	}
	return all
}

// skeletonReady says what a skeleton needs that the machine did not show:
// a copy keeps the machine's own loader and release instead.
func skeletonReady(host string, f machineFacts) error {
	switch {
	case f.Loader == "":
		return fmt.Errorf("could not tell which boot loader %s uses: its /boot has no systemd-boot, GRUB or extlinux files. Copy its configuration with --copy-config, which keeps its own loader", host)
	case f.Loader == "grub" && f.Boot == "bios" && f.GrubDevice == "":
		return fmt.Errorf("could not tell which disk GRUB is installed to on %s: give it with --grub-device", host)
	case f.StateVersion == "":
		return fmt.Errorf("could not read system.stateVersion from %s's /etc/nixos: give the skeleton one by hand, or copy its configuration with --copy-config, which keeps its own", host)
	}
	return nil
}
