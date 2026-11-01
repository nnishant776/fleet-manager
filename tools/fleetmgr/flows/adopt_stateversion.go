package flows

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/fleetmgr"
)

// Where host adopt takes a skeleton's system.stateVersion from. It must be
// the release the machine was installed with, since services keep their
// data in formats it chooses, so fleetmgr reads it rather than guesses:
//
//  1. --state-version, when given;
//  2. the configuration the machine was built from, evaluated: the
//     fleet-manager host at the commit its /etc/org/revision names, the
//     flake in its /etc/nixos, or its configuration.nix, else the one value
//     that configuration.nix writes;
//  3. otherwise its oldest system generation suggests one: the install, when
//     that is generation 1, or a later release when older ones are gone. A
//     configuration brought over from an older machine keeps its older
//     release, so a suggestion is only asked about, with the inventory's
//     default beside it, and never taken unasked.

// adoptHostname is a hostname fleetmgr puts in a command or an attribute path.
var adoptHostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// adoptDate is a day as stat prints it.
var adoptDate = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// releaseOf is the release of a value Nix printed for system.stateVersion.
func releaseOf(out string) string {
	v := strings.Trim(strings.TrimSpace(out), `"`)
	if adoptRelease.MatchString(v) {
		return v
	}
	return ""
}

// fleetManagerHostAt looks for the fleet-manager commit the machine runs, as
// its /etc/org/revision names it: a commit of fleet-manager's main you trust,
// with the machine's host in it. It sets f.OrgFleetHost and, evaluated
// there, f.OrgStateVersion. A commit of another repository finds nothing.
func fleetManagerHostAt(x *engine.Ctx, f *machineFacts) error {
	if f.OrgRevision == "" || f.Hostname == "" {
		return nil
	}
	dir, signers, cleanup, err := fleetManagerHistory(x)
	if err != nil {
		return err
	}
	defer cleanup()
	git := func(args ...string) error {
		_, err := engine.Cmd("git", append([]string{"-C", dir}, args...)...).ReadOnly().Quiet().Run(x)
		return err
	}
	if git("cat-file", "-e", f.OrgRevision+"^{commit}") != nil ||
		git("merge-base", "--is-ancestor", f.OrgRevision, fmMain) != nil {
		x.UI.Skip("its /etc/org/revision, " + f.OrgRevision[:12] + ", is not a commit of fleet-manager's main")
		return nil
	}
	if git("-c", "gpg.ssh.allowedSignersFile="+signers, "verify-commit", f.OrgRevision) != nil {
		x.UI.Skip("fleet-manager's " + f.OrgRevision[:12] + ", which it runs, is not signed by a key you trust: nothing is read from it")
		return nil
	}
	if git("cat-file", "-e", f.OrgRevision+":hosts/"+f.Hostname+"/default.nix") != nil {
		x.UI.Skip("it runs fleet-manager's " + f.OrgRevision[:12] + ", which has no host " + f.Hostname)
		return nil
	}
	f.OrgFleetHost = f.Hostname
	res, err := fleetmgr.Nix("eval", "--raw",
		"git+file://"+dir+"?rev="+f.OrgRevision+"#nixosConfigurations."+f.Hostname+".config.system.stateVersion").
		ReadOnly().Quiet().OK("evaluated fleet-manager's " + f.Hostname + " at " + f.OrgRevision[:12]).Run(x)
	if err == nil {
		f.OrgStateVersion = releaseOf(res.Stdout)
	}
	return nil
}

// stateVersionFromConfig evaluates system.stateVersion in the machine's own
// /etc/nixos; for a configuration.nix that does not evaluate there, it takes
// the one value the file writes. It returns "" when neither gives one.
func stateVersionFromConfig(x *engine.Ctx, f *machineFacts, kind string) (version, from string) {
	switch {
	case kind == "flake" && f.Hostname != "":
		res, err := fleetmgr.OnAdmin("sudo -n nix --extra-experimental-features 'nix-command flakes' eval --raw " +
			"/etc/nixos#nixosConfigurations." + f.Hostname + ".config.system.stateVersion 2>/dev/null").ReadOnly().Quiet().Run(x)
		if v := releaseOf(res.Stdout); err == nil && v != "" {
			return v, "nixosConfigurations." + f.Hostname + " of the flake in its /etc/nixos, evaluated"
		}
	case kind == "configuration":
		res, err := fleetmgr.OnAdmin(`sudo -n nix-instantiate --eval --strict -E ` +
			`'(import <nixpkgs/nixos> { configuration = /etc/nixos/configuration.nix; }).config.system.stateVersion' 2>/dev/null`).
			ReadOnly().Quiet().Run(x)
		if v := releaseOf(res.Stdout); err == nil && v != "" {
			return v, "its /etc/nixos/configuration.nix, evaluated"
		}
		res, err = fleetmgr.OnAdmin(`sudo -n grep -hoE 'system\.stateVersion *= *"[0-9]+\.[0-9]+"' /etc/nixos/configuration.nix 2>/dev/null`).
			ReadOnly().Quiet().Run(x)
		if err == nil {
			seen := map[string]bool{}
			for _, line := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
				if _, q, ok := strings.Cut(line, "="); ok {
					if v := releaseOf(q); v != "" {
						seen[v] = true
					}
				}
			}
			if len(seen) == 1 {
				for v := range seen {
					return v, "the value its /etc/nixos/configuration.nix sets"
				}
			}
		}
	}
	return "", ""
}

// inventoryStateVersion is the stateVersion fleet-manager gives a host by
// default, at the commit the inventory pins, or "" when that commit says none.
func inventoryStateVersion(x *engine.Ctx) string {
	res, err := fleetmgr.Nix("eval", "--raw", "--impure", "--expr",
		"(builtins.getFlake "+nixString(x.Value("flake"))+").inputs.fleet-manager.lib.defaultStateVersion or \"\"").
		ReadOnly().Quiet().Run(x)
	if err != nil {
		return ""
	}
	return releaseOf(res.Stdout)
}

// generationNote says what the machine's oldest system generation shows.
func generationNote(f machineFacts) string {
	if f.GenFirst == 0 || f.GenFirstRelease == "" {
		return ""
	}
	when := ""
	if f.GenFirstDate != "" {
		when = " on " + f.GenFirstDate
	}
	if f.GenFirst == 1 {
		return fmt.Sprintf("generation 1, the install%s, ran NixOS %s", when, f.GenFirstRelease)
	}
	return fmt.Sprintf("its oldest generation still there, %d, made%s, ran NixOS %s; the install was that release or an older one", f.GenFirst, when, f.GenFirstRelease)
}

// settleStateVersion sets f.StateVersion and where it came from, for a
// skeleton, or says what to give.
func settleStateVersion(x *engine.Ctx, f *machineFacts, kind string) error {
	if v := x.Value("state-version"); v != "" {
		f.StateVersion, f.StateVersionFrom = v, "given with --state-version"
		x.UI.OK("system.stateVersion " + v + ", as given")
		return nil
	}
	if f.StateVersion != "" {
		return nil
	}
	if f.OrgStateVersion != "" {
		f.StateVersion = f.OrgStateVersion
		f.StateVersionFrom = "fleet-manager's nixosConfigurations." + f.OrgFleetHost + " at " + f.OrgRevision[:12] + ", which it was built from"
		x.UI.OK("system.stateVersion " + f.StateVersion + ", from " + f.StateVersionFrom)
		return nil
	}
	if v, from := stateVersionFromConfig(x, f, kind); v != "" {
		f.StateVersion, f.StateVersionFrom = v, from
		x.UI.OK("system.stateVersion " + v + ", from " + from)
		return nil
	}
	gen := generationNote(*f)
	def := inventoryStateVersion(x)
	var evidence []string
	if gen != "" {
		evidence = append(evidence, gen)
	}
	if def != "" {
		evidence = append(evidence, "the inventory's default is "+def)
	}
	suggest := f.GenFirstRelease
	if suggest == "" {
		suggest = def
	}
	what := "could not read " + f.Host + "'s system.stateVersion from the configuration it was built from"
	if len(evidence) > 0 {
		what += ": " + strings.Join(evidence, ", and ")
	}
	if !x.UI.Interactive() || x.Options.Yes {
		hint := "give --state-version with the release it was installed with"
		if suggest != "" {
			hint = "give --state-version " + suggest + " if that is the release it was installed with"
		}
		return fmt.Errorf("%s. A configuration brought over from an older machine keeps its older release, so fleetmgr does not take a guess: %s", what, hint)
	}
	x.UI.Note(what + ". A configuration brought over from an older machine keeps its older release.")
	v, err := x.UI.Input("system.stateVersion for "+f.Host,
		"The NixOS release it was installed with, such as 24.11; services keep their data in the formats it chooses",
		suggest, func(v string) error {
			if !adoptRelease.MatchString(v) {
				return fmt.Errorf("%q is not a release such as 24.11", v)
			}
			return nil
		})
	if err != nil {
		return err
	}
	f.StateVersion = v
	f.StateVersionFrom = "typed in when it was adopted"
	if gen != "" {
		f.StateVersionFrom += "; " + gen
	}
	return nil
}

// atoiOr is a number from the machine, or 0.
func atoiOr(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 0 {
		return 0
	}
	return n
}
