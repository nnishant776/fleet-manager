package fleetmgr

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
)

// HostKeyInput is the host key's fingerprint, read from the host's console,
// for a run that cannot ask whether the one it is shown matches.
var HostKeyInput = engine.Input{
	Name:   "host-key",
	Prompt: "Fingerprint of the host's key",
	Help: "SHA256:…, as ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub prints it on the host's console; " +
		"only needed when this machine does not know the host's key yet and cannot ask",
	Optional: true,
}

// AdminPasswordInput is the admin account's sudo password, for a host where
// its sudo takes neither a key nor a rule without a password.
var AdminPasswordInput = engine.Input{
	Name:   "admin-password",
	Prompt: "Sudo password of the admin account",
	Help: "Only needed when the admin account's sudo asks for its password; asked for then, or read from --admin-password-stdin. " +
		"It is kept in memory only, and passed to sudo on the host on stdin",
	Secret:   true,
	Optional: true,
}

// sshTarget is what ssh really connects to for an address, after its own
// configuration: the name it looks the host key up under, how to scan it,
// and the files it keeps known keys in.
type sshTarget struct {
	Hostname, Port, KeyName string
	UserFiles, GlobalFiles  []string
}

// readSSHTarget reads ssh -G for the address: an alias in ~/.ssh/config can
// map it to another name and port, under which ssh then looks for the key.
func readSSHTarget(x *engine.Ctx) (sshTarget, error) {
	res, err := engine.Cmd("ssh", "-G", "{{.address}}").RunQuiet(x)
	if err != nil {
		return sshTarget{}, fmt.Errorf("ssh -G %s: %w", x.Value("address"), err)
	}
	t := sshTarget{Hostname: x.Value("address"), Port: "22"}
	alias := ""
	home, _ := os.UserHomeDir()
	files := func(v string) []string {
		var out []string
		for _, f := range strings.Fields(v) {
			if rest, ok := strings.CutPrefix(f, "~/"); ok {
				f = filepath.Join(home, rest)
			}
			if f != "/dev/null" {
				out = append(out, f)
			}
		}
		return out
	}
	for _, line := range strings.Split(res.Stdout, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		switch k {
		case "hostname":
			t.Hostname = v
		case "port":
			t.Port = v
		case "hostkeyalias":
			alias = v
		case "userknownhostsfile":
			t.UserFiles = files(v)
		case "globalknownhostsfile":
			t.GlobalFiles = files(v)
		}
	}
	if len(t.UserFiles) == 0 {
		t.UserFiles = []string{filepath.Join(home, ".ssh", "known_hosts")}
	}
	switch {
	case alias != "":
		t.KeyName = alias
	case t.Port != "22":
		t.KeyName = "[" + t.Hostname + "]:" + t.Port
	default:
		t.KeyName = t.Hostname
	}
	return t, nil
}

// KnowHostKey makes sure ssh knows the host's key before anything logs in to
// it. A key it knows is used as it is. An unknown one is fetched, and its
// fingerprint compared with the one on the host's console: by the person,
// who is asked, or with --host-key. Only then is it recorded. --yes alone
// does not accept a key: trusting it is a decision, not a change to run.
func KnowHostKey(x *engine.Ctx) error {
	t, err := readSSHTarget(x)
	if err != nil {
		return err
	}
	// ssh-keygen finds nothing in a file that is not there.
	for _, f := range append(append([]string{}, t.UserFiles...), t.GlobalFiles...) {
		if _, err := engine.Cmd("ssh-keygen", "-F", t.KeyName, "-f", f).RunQuiet(x); err == nil {
			x.UI.OK("this machine knows " + x.Value("address") + "'s host key, in " + f)
			return nil
		}
	}
	x.Values["scan_host"], x.Values["scan_port"] = t.Hostname, t.Port
	res, err := engine.Cmd("ssh-keyscan", "-T", "10", "-t", "ed25519", "-p", "{{.scan_port}}", "{{.scan_host}}").RunQuiet(x)
	var line string
	for _, l := range strings.Split(res.Stdout, "\n") {
		if f := strings.Fields(l); len(f) == 3 && !strings.HasPrefix(l, "#") && f[1] == "ssh-ed25519" {
			line = f[1] + " " + f[2]
			break
		}
	}
	if line == "" {
		if err == nil {
			err = errors.New("no ed25519 key came back")
		}
		return fmt.Errorf("could not fetch %s's host key (%v): is it on, and is sshd running there?", x.Value("address"), err)
	}
	x.Values["scanned_key"] = t.KeyName + " " + line
	fp, err := engine.Cmd("ssh-keygen", "-l", "-f", "-").StdinFrom("scanned_key").RunQuiet(x)
	if err != nil {
		return fmt.Errorf("reading the fingerprint of %s's host key: %w", x.Value("address"), err)
	}
	f := strings.Fields(fp.Stdout)
	if len(f) < 2 || !strings.HasPrefix(f[1], "SHA256:") {
		return fmt.Errorf("ssh-keygen gave no fingerprint for %s's host key", x.Value("address"))
	}
	got := f[1]
	x.UI.Text("This machine does not know " + x.Value("address") + "'s host key yet. It answered with:\n\n  " + got + "\n\n" +
		"On " + x.Value("host") + "'s own console, ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub must print the same. " +
		"If it does not, something between here and there answers in its place: stop.")
	switch given := strings.TrimSpace(x.Value("host-key")); {
	case given != "":
		if strings.TrimPrefix(given, "SHA256:") != strings.TrimPrefix(got, "SHA256:") {
			return fmt.Errorf("the key %s answered with is %s, not %s, the one given with --host-key: not trusting it", x.Value("address"), got, given)
		}
		x.UI.OK("it matches the fingerprint given with --host-key")
	case x.Options.DryRun:
		x.UI.Skip("dry run: not recorded, so the steps that log in will not reach it")
		return nil
	case x.Options.Yes || !x.UI.Interactive():
		return errors.New("compare it with the console, then give it with --host-key " + got + "; --yes alone does not trust a key")
	default:
		ok, err := x.UI.Confirm("Does "+x.Value("host")+"'s console show the same fingerprint?", false)
		if err != nil {
			return err
		}
		if !ok {
			x.UI.Note("Not recorded: nothing logged in to " + x.Value("address") + ".")
			return engine.ErrStopped
		}
	}
	file := t.UserFiles[0]
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := out.WriteString(x.Value("scanned_key") + "\n"); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	x.UI.OK("recorded it in " + file + ", as " + t.KeyName)
	return nil
}

// sudoPrelude goes before every script OnAdmin runs once the admin's sudo
// takes a password. It reads the password from the first line of stdin into
// the environment, never onto a command line or into a file, and puts a sudo
// first in PATH that asks for it through an askpass helper: -n is dropped,
// -A added. Nested shells find it too, and the rest of stdin is the
// script's. The helpers hold no secret; they go when the script ends.
const sudoPrelude = `IFS= read -r FLEETMGR_SUDO_PW; export FLEETMGR_SUDO_PW
fleetmgr_d=$(mktemp -d "${XDG_RUNTIME_DIR:-/tmp}/fleetmgr-sudo.XXXXXX") || exit 1
trap 'rm -rf "$fleetmgr_d"' EXIT
printf '%s\n' '#!/bin/sh' 'printf "%s\n" "$FLEETMGR_SUDO_PW"' > "$fleetmgr_d/askpass"
printf '%s\n' '#!/bin/sh' '[ "$1" = -n ] && shift' "exec $(command -v sudo) -A \"\$@\"" > "$fleetmgr_d/sudo"
chmod 700 "$fleetmgr_d/askpass" "$fleetmgr_d/sudo"
export SUDO_ASKPASS="$fleetmgr_d/askpass" PATH="$fleetmgr_d:$PATH"
`

// AdminSudo settles how the admin account's sudo works on the host, before
// anything relies on it. Without a password, by the key in the agent or a
// rule, nothing changes. Otherwise the password, from --admin-password-stdin
// or asked for once, is checked with a single attempt, so that a wrong one
// cannot count three times against a lockout, and from then on OnAdmin hands
// it to sudo on stdin.
func AdminSudo(x *engine.Ctx) error {
	x.Values["admin_prelude"], x.Values["admin_password"] = "", ""
	res, err := OnAdmin("sudo -n true 2>/dev/null && echo sudo=key || echo sudo=password").RunQuiet(x)
	if err != nil {
		return fmt.Errorf("could not log in as %s: %w", x.Value("admin"), err)
	}
	switch {
	case strings.Contains(res.Stdout, "sudo=key"):
		x.UI.OK(x.Value("admin") + "'s sudo needs no password: the key in the agent, or a rule, lets it in")
		return nil
	case !strings.Contains(res.Stdout, "sudo=password"):
		return fmt.Errorf("unexpected answer from %s about its sudo", x.Value("address"))
	}
	pw := x.Value("admin-password")
	if pw == "" {
		if x.Options.DryRun || !x.UI.Interactive() {
			return fmt.Errorf("%s's sudo asks for its password: give it with --admin-password-stdin, or run on a terminal to be asked", x.Value("admin"))
		}
		if pw, err = x.UI.Secret("Sudo password of "+x.Value("admin")+" on "+x.Value("host"),
			"Asked once, kept in this run's memory only, and passed to sudo on the host on stdin", nil); err != nil {
			return err
		}
	}
	x.Values["admin_password"] = pw
	// One line on stdin: sudo -S reads the next for a second attempt, finds
	// none, and stops.
	if res, err := OnAdmin("sudo -S -p '' -v").RunQuiet(x); err != nil {
		x.Values["admin_password"] = ""
		if res.Code == 255 {
			return err
		}
		return fmt.Errorf("sudo on %s refused %s's password", x.Value("address"), x.Value("admin"))
	}
	x.Values["admin_prelude"] = sudoPrelude
	x.UI.OK(x.Value("admin") + "'s sudo takes its password, which stays in this run's memory and goes to sudo on stdin")
	return nil
}
