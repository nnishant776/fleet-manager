// Package fleet knows this fleet's conventions: where the configuration and
// the secrets checkouts are, how hosts are listed, and how fleet reaches a
// host. Flows use it so that a rule like "go in as support and forward
// nothing" is written once.
package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// Upstream is the public configuration repository.
const Upstream = "github:nnishant776/fleet-manager"

// UpstreamURL is the same repository for git.
const UpstreamURL = "https://github.com/nnishant776/fleet-manager"

// UpstreamRaw serves its files by commit.
const UpstreamRaw = "https://raw.githubusercontent.com/nnishant776/fleet-manager"

// FlakeInput is the configuration to read hosts from. With no checkout here
// or in /etc/nixos it is asked for: never main straight from GitHub, which
// nothing has checked the signature of, though what is read from it decides
// where keys are registered and what a host switches to.
var FlakeInput = engine.Input{
	Name:   "flake",
	Prompt: "Configuration flake",
	Help: "A checkout or a flake reference, or the name of a repository (fleet repo list), which means the tool's own clone of it; " +
		"defaults to this directory's flake, then /etc/nixos",
	Resolve: func(x *engine.Ctx, v string) (string, error) { return ResolveName(x, "flake", v) },
	Default: func(*engine.Ctx) (string, error) {
		if v := os.Getenv("FLEET_FLAKE"); v != "" {
			return v, nil
		}
		for _, dir := range []string{".", "/etc/nixos"} {
			if _, err := os.Stat(filepath.Join(dir, "flake.nix")); err == nil {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return "", err
				}
				return abs, nil
			}
		}
		return "", nil
	},
}

// HostInputs are what every flow on one host asks: the host, how to reach
// it, and as whom.
func HostInputs() []engine.Input {
	return []engine.Input{
		FlakeInput,
		{
			Name:    "host",
			Prompt:  "Which host?",
			Help:    "A host of the configuration (hosts/<name>)",
			Pattern: `^[A-Za-z0-9][A-Za-z0-9-]*$`,
			Choices: Hosts,
		},
		{
			Name:   "address",
			Prompt: "Address to reach it at",
			Help:   "A name or IP address for SSH; defaults to the host's name",
			Default: func(x *engine.Ctx) (string, error) {
				return x.Value("host"), nil
			},
		},
		{
			Name:   "user",
			Prompt: "Account to log in as",
			Help:   "The account to log in as; support by default, which has sudo without a password, so nothing needs forwarding",
			Default: func(*engine.Ctx) (string, error) {
				return "support", nil
			},
		},
	}
}

// NixFlakes turns on what the Nix of the stock installer ISO leaves off; on
// a machine that has them on already it changes nothing.
var NixFlakes = []string{"--extra-experimental-features", "nix-command flakes"}

// Nix is a nix command that works on the installer as well.
func Nix(args ...string) *engine.Command {
	return engine.Cmd("nix", append(append([]string{}, NixFlakes...), args...)...)
}

// Hosts lists the NixOS hosts of the configuration flake.
func Hosts(x *engine.Ctx) ([]string, error) {
	res, err := x.Runner.Run(x.Context, engine.Spec{
		Name: "nix",
		Args: append(append([]string{}, NixFlakes...), "eval", "--json", x.Value("flake")+"#nixosConfigurations", "--apply", "builtins.attrNames"),
	})
	if err != nil {
		return nil, fmt.Errorf("listing hosts: %w: %s", err, strings.TrimSpace(res.Stderr))
	}
	var hosts []string
	if err := json.Unmarshal([]byte(res.Stdout), &hosts); err != nil {
		return nil, fmt.Errorf("listing hosts: %w", err)
	}
	return hosts, nil
}

// OnHost runs a shell script on the host over SSH, as the flow's user, with
// no agent forwarded, and never asking for a password or an unknown host key:
// a host whose key is not known yet fails, and says so.
func OnHost(script string) *engine.Command {
	return engine.Cmd("ssh",
		"-o", "ForwardAgent=no",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"{{.user}}@{{.address}}",
		script,
	).Explain(SSHTrouble("{{.user}}"))
}

// SSHTrouble explains a failure of ssh itself, which exits with 255, from
// its message: the reason in plain words and what to try. login is the
// account, templated. A failure of the command on the host is left as it is.
func SSHTrouble(login string) func(engine.Result) string {
	return func(r engine.Result) string {
		if r.Code != 255 {
			return ""
		}
		e := r.Stderr
		switch {
		case strings.Contains(e, "Could not resolve hostname"):
			return "the name {{.address}} does not resolve to an address: give its IP address with --address"
		case strings.Contains(e, "REMOTE HOST IDENTIFICATION HAS CHANGED"):
			return "{{.address}} shows a different host key from the one this machine knows. If it was reinstalled, " +
				"remove the old key with ssh-keygen -R {{.address}} and check the new one; if not, stop here"
		case strings.Contains(e, "Host key verification failed"):
			return "this machine does not know {{.address}}'s host key yet: log in once with ssh " + login +
				"@{{.address}}, check the fingerprint, and run this again"
		case strings.Contains(e, "Connection refused"):
			return "nothing at {{.address}} accepts SSH: is it the right address, and is sshd running there?"
		case strings.Contains(e, "timed out"), strings.Contains(e, "No route to host"), strings.Contains(e, "Network is unreachable"):
			return "{{.address}} does not answer: is it on, and on a network this machine can reach?"
		case strings.Contains(e, "Permission denied"):
			return login + "@{{.address}} refused the keys this machine offered: is the key in the host's configuration, and loaded (ssh-add -l)?"
		}
		return ""
	}
}

// OnAdmin runs a script on the host as the admin account, forwarding only the
// one-key agent StartAgent made: admin's sudo checks that key through the
// forwarded agent. Use it only until the host has support (OnHost).
func OnAdmin(script string) *engine.Command {
	return engine.Cmd("ssh",
		"-A",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=10",
		"{{.admin}}@{{.address}}",
		script,
	).Env("SSH_AUTH_SOCK={{.agent}}").Explain(SSHTrouble("{{.admin}}"))
}

// StartAgent starts an ssh-agent holding only the key in the value "key",
// for an hour at most, and keeps its socket as "agent". Your own agent, with
// your other keys, is never forwarded. StopAgent stops it.
func StartAgent(x *engine.Ctx) error {
	dir, err := os.MkdirTemp("", "fleet-agent-")
	if err != nil {
		return err
	}
	x.Values["agent_dir"] = dir
	x.Values["agent"] = filepath.Join(dir, "agent.sock")
	res, err := engine.Cmd("ssh-agent", "-s", "-t", "3600", "-a", "{{.agent}}").ReadOnly().Quiet().
		OK("started; it forgets its key within the hour").Run(x)
	if err != nil {
		return err
	}
	for _, field := range strings.Split(res.Stdout, ";") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(field), "SSH_AGENT_PID="); ok {
			x.Values["agent_pid"] = v
		}
	}
	_, err = engine.Cmd("ssh-add", "{{.key}}").Env("SSH_AUTH_SOCK={{.agent}}").ReadOnly().Prompts().
		OK("holds {{.key}} only").Run(x)
	return err
}

// StopAgent stops the agent StartAgent made, if it did.
func StopAgent(x *engine.Ctx) {
	if pid := x.Value("agent_pid"); pid != "" {
		// Never on a cancelled context, which would not start the kill.
		if _, err := x.Runner.Run(context.WithoutCancel(x.Context), engine.Spec{Name: "kill", Args: []string{pid}}); err != nil {
			x.UI.Fail("could not stop the one-key agent; stop it with: kill " + pid)
		} else {
			x.UI.Skip("stopped the one-key agent")
		}
	}
	if dir := x.Value("agent_dir"); dir != "" {
		_ = os.RemoveAll(dir)
	}
}

// StillRunning notes, while c runs, that an interruption leaves what it
// started on the host running there, where it finishes by itself, and how
// to follow it: unit is the systemd unit it runs as, login the account to
// look with.
func StillRunning(c *engine.Command, what, unit, login string) *engine.Command {
	key := "running " + unit
	return c.Before(func(x *engine.Ctx) error {
		x.Running(key, what+" on {{.host}} may still be running there, and finishes by itself; follow it with: ssh "+
			login+"@{{.address}} journalctl -fu "+unit)
		return nil
	}).After(func(x *engine.Ctx) error {
		x.Running(key, "")
		return nil
	})
}

// Creates registers, before c runs, removing the files it is about to
// create (templated), should it not finish. Once it has, they stay, and the
// note says so.
func Creates(c *engine.Command, note string, files ...string) *engine.Command {
	key := "create " + strings.Join(files, " ")
	return c.Before(func(x *engine.Ctx) error {
		var paths []string
		for _, f := range files {
			p, err := x.Expand(f)
			if err != nil {
				return err
			}
			paths = append(paths, p)
		}
		x.Undo(key, engine.Cmd("rm", append([]string{"-f", "--"}, paths...)...).OK("removed what it had started to write"))
		return nil
	}).After(func(x *engine.Ctx) error {
		x.Done(key)
		x.Kept(key, note)
		return nil
	})
}
