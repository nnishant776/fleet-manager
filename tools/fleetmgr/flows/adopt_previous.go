package flows

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleetmgr/internal/fleetmgr"
)

// What host adopt keeps of the machine's /etc/nixos: the part that is the
// host's own in previous/, and in previous.md beside it, a note of what the
// rest was. A plain /etc/nixos, a configuration.nix and what it imports, is
// all the host's own and is kept whole, as a copy needs it to build. A
// checkout of fleet-manager holds every host of that repository: only the
// host's folder and the checkout's local changes are its own, and the note
// says which commit the rest is. Anything that looks like a secret is left
// out, whatever the kind.

// etcFacts is what the machine's /etc/nixos is, beyond flake or
// configuration.nix.
type etcFacts struct {
	Git                  bool
	Origin, Head, Branch string
	Changes              []string // git status --porcelain lines
	FleetManager         bool     // a checkout of fleet-manager: lib/mkHost.nix and hosts/
	HostDir              bool     // it has hosts/<hostname>
}

// etcFactsScript prints what etcFacts holds, one key=value a line, for the
// machine whose hostname is host, checked by the caller.
func etcFactsScript(host string) string {
	return `set -u
g() { sudo -n git -c safe.directory=/etc/nixos -C /etc/nixos "$@"; }
if sudo -n test -d /etc/nixos/.git; then
  echo git=true
  echo "origin=$(g remote get-url origin 2>/dev/null)"
  echo "head=$(g rev-parse HEAD 2>/dev/null)"
  echo "branch=$(g symbolic-ref --short -q HEAD 2>/dev/null)"
  g status --porcelain=v1 --untracked-files=all 2>/dev/null | sed 's/^/change=/'
fi
if sudo -n test -e /etc/nixos/lib/mkHost.nix && sudo -n test -d /etc/nixos/hosts; then echo fleet_manager=true; fi
if sudo -n test -d /etc/nixos/hosts/` + host + `; then echo host_dir=true; fi`
}

func parseEtcFacts(out string) etcFacts {
	var e etcFacts
	for _, line := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch k {
		case "git":
			e.Git = v == "true"
		case "origin":
			e.Origin = strings.TrimSpace(v)
		case "head":
			if fullHash.MatchString(strings.TrimSpace(v)) {
				e.Head = strings.TrimSpace(v)
			}
		case "branch":
			e.Branch = strings.TrimSpace(v)
		case "change":
			e.Changes = append(e.Changes, v)
		case "fleet_manager":
			e.FleetManager = v == "true"
		case "host_dir":
			e.HostDir = v == "true"
		}
	}
	return e
}

// keptPath is a path fleetmgr names in a command: letters, digits and
// ._+@-, in parts separated by /, inside /etc/nixos.
var keptPath = regexp.MustCompile(`^[A-Za-z0-9._+@-]+(/[A-Za-z0-9._+@-]+)*$`)

// changedPaths are the paths git status names as added, changed or new; a
// deleted one is gone, and a name git had to quote is left out, and said.
func changedPaths(changes []string) (paths, deleted, odd []string) {
	for _, c := range changes {
		if len(c) < 4 {
			continue
		}
		code, p := c[:2], c[3:]
		if _, to, ok := strings.Cut(p, " -> "); ok {
			p = to
		}
		switch {
		case strings.Contains(code, "D"):
			deleted = append(deleted, p)
		case !keptPath.MatchString(p) || strings.Contains("/"+p+"/", "/../"):
			odd = append(odd, p)
		default:
			paths = append(paths, p)
		}
	}
	return paths, deleted, odd
}

// previousSelection is which part of /etc/nixos to keep: all of it, or the
// paths listed.
type previousSelection struct {
	All   bool
	Paths []string
}

// selectPrevious decides what of the machine's /etc/nixos is the host's own.
func selectPrevious(kind string, e etcFacts, hostname string) previousSelection {
	switch {
	case kind == "none":
		return previousSelection{}
	case kind == "flake" && e.FleetManager:
		var sel previousSelection
		dir := "hosts/" + hostname
		if e.HostDir {
			sel.Paths = append(sel.Paths, dir)
		}
		changed, _, _ := changedPaths(e.Changes)
		for _, p := range changed {
			if p != dir && !strings.HasPrefix(p, dir+"/") {
				sel.Paths = append(sel.Paths, p)
			}
		}
		return sel
	}
	return previousSelection{All: true}
}

// previousTar is the command that reads the selection from the machine, or
// "" when there is nothing to read.
func previousTar(sel previousSelection) string {
	switch {
	case sel.All:
		return "sudo -n tar -h -C /etc/nixos --exclude=./.git --exclude=./result -cf - ."
	case len(sel.Paths) == 0:
		return ""
	}
	quoted := make([]string, len(sel.Paths))
	for i, p := range sel.Paths {
		quoted[i] = engine.Quote(p)
	}
	return "sudo -n tar -h -C /etc/nixos --exclude=.git --ignore-failed-read -cf - -- " + strings.Join(quoted, " ")
}

// Names and contents that mark a secret: private keys, age identities and
// files that hold credentials. A public key, *.pub, is not one.
var (
	secretName = regexp.MustCompile(`(?i)(^id_[^.]*$|\.(key|pem|p12|pfx|age|kdbx)$|^\.env$|^\.netrc$)`)
	secretText = []string{"PRIVATE KEY-----", "AGE-SECRET-KEY-1"}
)

// looksSecret says whether a file of /etc/nixos looks like it holds a secret.
func looksSecret(p string, data []byte) bool {
	if secretName.MatchString(path.Base(p)) {
		return true
	}
	for _, t := range secretText {
		if bytes.Contains(data, []byte(t)) {
			return true
		}
	}
	return false
}

// leaveOutSecrets drops the files that look like secrets, and names them.
func leaveOutSecrets(files map[string][]byte) []string {
	var out []string
	for p, data := range files {
		if looksSecret(p, data) {
			out = append(out, p)
			delete(files, p)
		}
	}
	slices.Sort(out)
	return out
}

// Above these, previous/ is kept whole only when asked.
const (
	previousMaxFiles = 50
	previousMaxBytes = 1 << 20
)

// previousTooLarge says what is large about the files, or "".
func previousTooLarge(files map[string][]byte) string {
	total := 0
	for _, d := range files {
		total += len(d)
	}
	if len(files) <= previousMaxFiles && total <= previousMaxBytes {
		return ""
	}
	return fmt.Sprintf("%d files, %d KiB", len(files), (total+1023)/1024)
}

// previousOrigin is what the machine was built from when its /etc/nixos
// says nothing: its /etc/org/revision, and the fleet-manager host of that
// commit when fleetmgr found one.
type previousOrigin struct {
	Revision    string // from /etc/org/revision
	FleetHost   string // nixosConfigurations.<name> of fleet-manager at Revision, when found
	EtcNixosWas string // flake, configuration or none
}

// previousNote is previous.md: what the machine's /etc/nixos was, what of
// it previous/ keeps, and what it left out.
func previousNote(host, hostname string, kind string, e etcFacts, o previousOrigin, kept, secrets, odd, deleted []string) string {
	var b strings.Builder
	b.WriteString("# " + host + "'s /etc/nixos when it was adopted\n\n")
	switch {
	case kind == "none" && o.FleetHost != "":
		fmt.Fprintf(&b, "Its /etc/nixos was empty. It ran fleet-manager's `nixosConfigurations.%s` at commit %s, as its /etc/org/revision says; those files are in fleet-manager at that commit.\n", o.FleetHost, o.Revision)
	case kind == "none" && o.Revision != "":
		fmt.Fprintf(&b, "Its /etc/nixos was empty. Its /etc/org/revision says it was built from commit %s of a flake fleetmgr does not have.\n", o.Revision)
	case kind == "none":
		b.WriteString("Its /etc/nixos was empty.\n")
	case e.Git && e.FleetManager:
		fmt.Fprintf(&b, "It was a checkout of fleet-manager, %s, at commit %s%s. The machine was `nixosConfigurations.%s` of it; the rest of the checkout is fleet-manager at that commit.\n",
			orUnknown(e.Origin), orUnknown(e.Head), branchNote(e.Branch), hostname)
	case e.FleetManager:
		fmt.Fprintf(&b, "It was a copy of fleet-manager, not a git checkout. The machine was `nixosConfigurations.%s` of it.\n", hostname)
	case e.Git:
		fmt.Fprintf(&b, "It was a git checkout of %s, at commit %s%s.\n", orUnknown(e.Origin), orUnknown(e.Head), branchNote(e.Branch))
	}
	if kind != "none" {
		b.WriteString("\n")
		if len(kept) == 0 {
			b.WriteString("previous/ keeps nothing of it.\n")
		} else {
			b.WriteString("previous/ keeps:\n\n")
			for _, p := range kept {
				b.WriteString("- `" + p + "`\n")
			}
		}
	}
	list := func(title string, paths []string) {
		if len(paths) == 0 {
			return
		}
		b.WriteString("\n" + title + "\n\n")
		for _, p := range paths {
			b.WriteString("- `" + p + "`\n")
		}
	}
	list("Left out, because they look like secrets (move them into the secrets repository):", secrets)
	list("Left out, because git quoted their names:", odd)
	list("Deleted in the checkout, and so not kept:", deleted)
	return b.String()
}

func orUnknown(s string) string {
	if s == "" {
		return "an unknown commit"
	}
	return s
}

func branchNote(branch string) string {
	if branch == "" {
		return ""
	}
	return ", on " + branch
}

// readPrevious reads what of the machine's /etc/nixos the host keeps into
// f.Previous, with f.PreviousNote beside it when there is something to say.
func readPrevious(x *engine.Ctx, f *machineFacts, kind string) error {
	f.Previous = map[string][]byte{}
	var e etcFacts
	if kind != "none" {
		res, err := fleetmgr.OnAdmin(etcFactsScript(f.Hostname)).ReadOnly().Quiet().OK("looked at its /etc/nixos").Run(x)
		if err != nil {
			return fmt.Errorf("looking at %s's /etc/nixos: %w", f.Host, err)
		}
		e = parseEtcFacts(res.Stdout)
	}
	sel := selectPrevious(kind, e, f.Hostname)
	if cmd := previousTar(sel); cmd != "" {
		tarball, err := fleetmgr.OnAdmin(cmd).ReadOnly().Quiet().OK("read its /etc/nixos").Run(x)
		if err != nil {
			return fmt.Errorf("reading %s's /etc/nixos: %w", f.Host, err)
		}
		if f.Previous, err = untarFiles([]byte(tarball.Stdout)); err != nil {
			return fmt.Errorf("%s's /etc/nixos: %w", f.Host, err)
		}
	}
	secrets := leaveOutSecrets(f.Previous)
	for _, p := range secrets {
		x.UI.Skip("left out " + p + " of its /etc/nixos: it looks like a secret, which does not belong in the inventory")
	}
	if large := previousTooLarge(f.Previous); large != "" && x.Value("keep-previous") != "true" {
		if !x.UI.Interactive() || x.Options.Yes {
			return fmt.Errorf("%s's /etc/nixos would put %s into previous/: tidy it, or give --keep-previous to keep it all", f.Host, large)
		}
		ok, err := x.UI.Confirm(fmt.Sprintf("Its /etc/nixos would put %s into previous/. Keep them all?", large), false)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("previous/ would be too large: tidy the machine's /etc/nixos, or give --keep-previous to keep it all")
		}
	}
	_, deleted, odd := changedPaths(e.Changes)
	if e.Git || e.FleetManager || kind == "none" && f.OrgRevision != "" {
		kept := sortedPaths(f.Previous)
		o := previousOrigin{Revision: f.OrgRevision, FleetHost: f.OrgFleetHost, EtcNixosWas: kind}
		f.PreviousNote = previousNote(f.Host, f.Hostname, kind, e, o, kept, secrets, odd, deleted)
	}
	return nil
}
