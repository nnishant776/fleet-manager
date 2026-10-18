// Package fleet knows this fleet's conventions: where the configuration and
// the secrets checkouts are, how hosts are listed, and how fleet reaches a
// host. Flows use it so that a rule like "go in as support and forward
// nothing" is written once.
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// Upstream is the public configuration repository.
const Upstream = "github:nnishant776/fleet-manager"

// FlakeInput is the configuration to read hosts from.
var FlakeInput = engine.Input{
	Name:   "flake",
	Prompt: "Configuration flake",
	Help:   "A checkout or a flake reference; defaults to this directory's flake, then /etc/nixos, then " + Upstream,
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
		return Upstream, nil
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

// Hosts lists the NixOS hosts of the configuration flake.
func Hosts(x *engine.Ctx) ([]string, error) {
	res, err := x.Runner.Run(x.Context, engine.Spec{
		Name: "nix",
		Args: []string{"eval", "--json", x.Value("flake") + "#nixosConfigurations", "--apply", "builtins.attrNames"},
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
	)
}
