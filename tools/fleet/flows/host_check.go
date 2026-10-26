package flows

import (
	"errors"
	"fmt"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// HostCheck looks at one host without changing anything.
var HostCheck = &engine.Flow{
	Path:  []string{"host", "check"},
	Title: "Check a host",
	Short: "Check a host: configuration, sync, secrets and health, without changing anything",
	Long: "Logs in to the host as support, with nothing forwarded, and reports what it\n" +
		"runs, whether its last sync succeeded, whether a problem is recorded for\n" +
		"everyone to see, what its secrets snapshot holds, and whether any unit failed.\n" +
		"It changes nothing, so it never asks.",
	Example: "  fleet host check neerajpinew\n  fleet host check pi-gw --address 192.168.0.9",
	Args:    []string{"host"},
	Inputs:  fleet.HostInputs(),
	Needs:   []string{"ssh", "nix"},
	Report:  true,
	Steps: []engine.Step{
		{
			Title:    "Reach {{.host}}",
			Why:      "As {{.user}}, with no agent forwarded, and only to a host whose key this machine already knows.",
			Required: true,
			Do: engine.Checks(engine.Probe{
				What: "{{.user}} at {{.address}}",
				Cmd:  fleet.OnHost("id -un; hostname"),
				Expect: func(out string) (string, error) {
					lines := strings.Fields(out)
					if len(lines) < 2 {
						return "", errors.New("unexpected answer: " + out)
					}
					return "logged in, hostname " + lines[1], nil
				},
			}),
		},
		{
			Title: "Configuration and sync",
			Why:   "The running system should be the revision its checkout holds, and the last sync should have succeeded.",
			Do: engine.Checks(
				engine.Probe{
					What: "running revision",
					Cmd:  fleet.OnHost(`r=$(cat /etc/org/revision); h=$(git -C /etc/nixos rev-parse HEAD 2>/dev/null || echo none); echo "$r $h"`),
					Expect: func(out string) (string, error) {
						f := strings.Fields(out)
						if len(f) != 2 {
							return "", errors.New("unexpected answer: " + out)
						}
						switch {
						case f[0] == "unknown":
							return "", errors.New("the system records no revision (installed from uncommitted changes?)")
						case f[1] == "none":
							return short(f[0]) + ", with no checkout in /etc/nixos", nil
						case f[0] != f[1]:
							return "", fmt.Errorf("runs %s but its checkout is at %s", short(f[0]), short(f[1]))
						}
						return short(f[0]) + ", the same as its checkout", nil
					},
				},
				engine.Probe{
					What: "checkout",
					Cmd:  fleet.OnHost(`test -d /etc/nixos/.git || { echo none; exit 0; }; git -C /etc/nixos status --porcelain | wc -l`),
					Expect: func(out string) (string, error) {
						switch out = strings.TrimSpace(out); out {
						case "none":
							return "none (not a fleet host)", nil
						case "0":
							return "no local changes", nil
						}
						return "", fmt.Errorf("%s local changes, which the next sync discards", out)
					},
				},
				engine.Probe{
					What: "last sync",
					Cmd:  fleet.OnHost(`systemctl show -p LoadState,Result,ExecMainExitTimestamp --value config-auto-update.service`),
					Expect: func(out string) (string, error) {
						f := strings.SplitN(strings.TrimSpace(out), "\n", 3)
						if len(f) < 3 || f[0] == "not-found" {
							return "no sync on this host", nil
						}
						if f[1] != "success" {
							return "", fmt.Errorf("%s, at %s", f[1], orNever(f[2]))
						}
						return "succeeded, at " + orNever(f[2]), nil
					},
				},
				engine.Probe{
					What: "problems recorded for everyone",
					Cmd:  fleet.OnHost(`cat /var/lib/fleet-sync/status 2>/dev/null || true`),
					Expect: func(out string) (string, error) {
						if strings.TrimSpace(out) == "" {
							return "none", nil
						}
						fields := map[string]string{}
						for _, line := range strings.Split(out, "\n") {
							if k, v, ok := strings.Cut(line, "="); ok {
								fields[k] = v
							}
						}
						return "", fmt.Errorf("%s since %s: %s", fields["kind"], fields["since"], fields["message"])
					},
				},
			),
		},
		{
			Title: "Secrets",
			Why:   "The snapshot names the secrets commit the host last accepted; /run/secrets holds what activation decrypted.",
			Do: engine.Checks(
				engine.Probe{
					What: "secrets snapshot",
					Cmd:  fleet.OnHost(`sudo -n test -d /var/lib/fleet || { echo none; exit 0; }; if sudo -n readlink /var/lib/fleet/secrets; then sudo -n cat /var/lib/fleet/secrets/manifest 2>/dev/null || true; else echo missing; fi`),
					Expect: func(out string) (string, error) {
						lines := strings.Split(strings.TrimSpace(out), "\n")
						switch name := strings.TrimSpace(lines[0]); {
						case name == "none":
							return "none (this host takes no secrets)", nil
						case name == "missing":
							return "", errors.New("an identity but no snapshot yet: not enrolled, or its deploy key is not registered")
						}
						// The snapshot is named by the hash of its manifest, which
						// says which commits it was made from; one from before the
						// manifest is named by its commit.
						commit := strings.TrimPrefix(strings.TrimSpace(lines[0]), "snapshots/")
						var users []string
						for _, l := range lines[1:] {
							f := strings.Fields(l)
							switch {
							case len(f) == 2 && f[0] == "host":
								commit = f[1]
							case len(f) == 3 && f[0] == "user" && f[2] == "host-branch":
								users = append(users, f[1]+" (host branch)")
							case len(f) == 3 && f[0] == "user":
								users = append(users, f[1]+" (own branch, "+short(f[2])+")")
							case len(f) == 4 && f[0] == "user" && f[2] == "kept":
								users = append(users, f[1]+" (kept the file from "+short(f[3])+")")
							}
						}
						detail := "commit " + short(commit)
						if len(users) > 0 {
							detail += "; users: " + strings.Join(users, ", ")
						}
						return detail, nil
					},
				},
				engine.Probe{
					What: "users' own secrets",
					Cmd:  fleet.OnHost(`cat /var/lib/fleet-sync/user-problems 2>/dev/null || true`),
					Expect: func(out string) (string, error) {
						var lines []string
						for _, l := range strings.Split(out, "\n") {
							if user, reason, ok := strings.Cut(strings.TrimSpace(l), " "); ok {
								lines = append(lines, "\n        "+user+": "+reason)
							}
						}
						if len(lines) == 0 {
							return "all up to date", nil
						}
						return "", fmt.Errorf("%d could not be updated and keep the file they had:%s", len(lines), strings.Join(lines, ""))
					},
				},
				engine.Probe{
					What: "decrypted secrets",
					Cmd:  fleet.OnHost(`sudo -n find -H /run/secrets /run/secrets-for-users -mindepth 1 ! -type d -printf '%P\n' 2>/dev/null | sort -u`),
					Expect: func(out string) (string, error) {
						names := strings.Fields(out)
						if len(names) == 0 {
							return "none", nil
						}
						return strings.Join(names, ", "), nil
					},
				},
			),
		},
		{
			Title: "Health",
			Why:   "A failed unit is worth knowing about before changing anything else.",
			Do: engine.Checks(engine.Probe{
				What: "system state",
				Cmd:  fleet.OnHost(`systemctl is-system-running || true; systemctl --failed --no-legend --plain | cut -d' ' -f1`),
				Expect: func(out string) (string, error) {
					f := strings.Fields(out)
					if len(f) == 0 {
						return "", errors.New("no answer")
					}
					if f[0] == "running" {
						return "running, no failed units", nil
					}
					return "", fmt.Errorf("%s; failed: %s", f[0], strings.Join(f[1:], ", "))
				},
			}),
		},
	},
}

func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

func orNever(ts string) string {
	if ts = strings.TrimSpace(ts); ts == "" || ts == "n/a" {
		return "never"
	}
	return ts
}
