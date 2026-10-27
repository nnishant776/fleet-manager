package flows

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// RepoSync fetches the tools' clones and lets the maintainer review what is
// new in them before the commits they trust move on.
var RepoSync = &engine.Flow{
	Path:  []string{"repo", "sync"},
	Title: "Review and accept the new commits of your repositories",
	Short: "Fetch the tools' clones, show the new commits and who signed them, and accept them",
	Long: "Fetches the tools' clone of each repository (or of the ones you name) and shows what\n" +
		"is new since the commits you trusted: for main, each host's branch and each user's\n" +
		"branch, the new commits with their date, author and the key that signed them. If you\n" +
		"accept, the commits you trust move to them and the clone moves to main.\n" +
		"\n" +
		"A repository is refused, and nothing is written for it, when a branch went backwards\n" +
		"or was rewritten, when the newest commit of main or of a host's branch is not signed\n" +
		"by a key you trust, or when its list of keys changed in a commit that no key you\n" +
		"already trust signed. A user's branch is signed by its user, with a key that only the\n" +
		"encrypted files name; if the newest commit is not signed by one of your keys, the\n" +
		"branch is left alone, and the secrets commands check it against the user's own key.\n" +
		"\n" +
		"If a history was rewritten on purpose and you know why, --accept-rewrite BRANCH\n" +
		"shows what was dropped and what is new, and takes it once you type the branch's name\n" +
		"at a terminal. For a user's branch it forgets the record instead, so that the next\n" +
		"secrets command takes the branch afresh after checking the user's key.",
	Example: "  fleet repo sync\n" +
		"  fleet repo sync inventory secrets\n" +
		"  fleet repo sync inventory --accept-rewrite host/nas",
	Args: []string{"name"},
	Rest: true,
	Inputs: []engine.Input{
		{Name: "name", Prompt: "Repositories", Optional: true, Many: true, Pattern: fleet.NamePattern,
			Help:    "The name of a repository to sync (see fleet repo list); may be given more than once. Every named repository by default",
			Default: func(*engine.Ctx) (string, error) { return "", nil }},
		{Name: "accept-rewrite", Prompt: "Branch whose rewrite to accept", Optional: true,
			Help:    "A branch that no longer follows the commit you trusted, which you know was rewritten on purpose; needs exactly one repository name and a terminal",
			Default: func(*engine.Ctx) (string, error) { return "", nil }},
	},
	Needs: []string{"git"},
	Steps: []engine.Step{{
		Title: "Fetch each repository and review what is new",
		Why: "Each clone is fetched and compared with the commits you trust. A repository with a refusal gets nothing written; for the others you are asked " +
			"before the commits you trust, your copy of its keys if they changed, and the clone move on.",
		Do: engine.Func(repoSync),
	}},
}

// syncBranch is what is to be done about one branch of one repository.
type syncBranch struct {
	name, rec, tip string
	write          string // the commit to record as trusted, or ""
	remove         bool   // the record goes
}

// syncRepo is one repository's review.
type syncRepo struct {
	name, id, clone, address string
	branches                 []*syncBranch
	mainTip                  string
	keys                     []byte // origin's signers, to keep as your copy; nil when they are the same
	keysPath                 string
	problems                 []string
	declined                 bool
	rewriteSeen              bool
}

func (p *syncRepo) fail(x *engine.Ctx, reason string) {
	p.problems = append(p.problems, reason)
	x.UI.Fail(reason)
}

func (p *syncRepo) changed() bool {
	if p.keys != nil {
		return true
	}
	for _, b := range p.branches {
		if b.write != "" || b.remove {
			return true
		}
	}
	return false
}

func repoSync(x *engine.Ctx) error {
	names, err := fleet.ReadNames()
	if err != nil {
		return err
	}
	var asked []string
	seen := map[string]bool{}
	for _, n := range strings.Split(x.Value("name"), "\n") {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] {
			continue
		}
		if _, ok := names[n]; !ok {
			return fmt.Errorf("%q is not the name of a repository: fleet repo list shows the names, fleet repo add makes one", n)
		}
		seen[n] = true
		asked = append(asked, n)
	}
	named := len(asked)
	if len(asked) == 0 {
		for n := range names {
			asked = append(asked, n)
		}
		sort.Strings(asked)
	}
	rewrite := x.Value("accept-rewrite")
	if rewrite != "" && named != 1 {
		return errors.New("--accept-rewrite needs exactly one repository name: fleet repo sync NAME --accept-rewrite BRANCH")
	}
	if len(asked) == 0 {
		x.UI.Note("No names yet: fleet repo add ADDRESS makes one.")
		return nil
	}
	// A repository with keys of its own first: the others borrow some
	// repository's keys, which may change in this same run.
	var own, borrowing []string
	for _, n := range asked {
		if hasOwnSigners(x, names[n]) {
			own = append(own, n)
		} else {
			borrowing = append(borrowing, n)
		}
	}
	var done []*syncRepo
	var summary []string
	refused := 0
	for _, n := range append(own, borrowing...) {
		p, status, err := syncOne(x, n, names[n], rewrite)
		if err != nil {
			return err
		}
		if len(p.problems) > 0 {
			refused++
		}
		done = append(done, p)
		summary = append(summary, status)
		x.UI.Blank()
	}
	if rewrite != "" && !done[0].rewriteSeen && len(done[0].problems) == 0 {
		x.UI.Note("--accept-rewrite names " + rewrite + ", which has nothing new to accept in " + asked[0] + ": it is treated as any other branch")
	}
	for i, s := range summary {
		line := append(own, borrowing...)[i] + ": " + s
		switch {
		case len(done[i].problems) > 0:
			x.UI.Fail(line)
		case strings.HasPrefix(s, "accepted"):
			x.UI.OK(line)
		default:
			x.UI.Skip(line)
		}
	}
	if refused > 0 {
		return fmt.Errorf("%d of %d repositories refused", refused, len(done))
	}
	return nil
}

// hasOwnSigners says whether the repository has its own fleet/allowed_signers
// (or a copy of it kept), judging by what the clone has before it is fetched.
func hasOwnSigners(x *engine.Ctx, id string) bool {
	clone, err := fleet.ClonePathOfID(id)
	if err != nil || !fileExists(clone) {
		return false
	}
	if origin, err := fleet.OriginOf(x, clone); err == nil {
		if p, err := fleet.SignersCopy(origin); err == nil && fileExists(p) {
			return true
		}
	}
	_, err = fleet.GitIn(x, clone, "cat-file", "-e", "refs/remotes/origin/main:fleet/allowed_signers")
	return err == nil
}

// syncOne reviews one repository and, if it passes and you accept, brings it
// forward. The error is for what stops the whole run; a refusal is in the
// repository's problems.
func syncOne(x *engine.Ctx, name, id, rewrite string) (*syncRepo, string, error) {
	p := &syncRepo{name: name, id: id}
	x.UI.Section("•", name, id)
	p.review(x, rewrite)
	if len(p.problems) > 0 {
		return p, "refused: " + strings.Join(p.problems, "; ") + " (nothing was written for it)", nil
	}
	if p.declined {
		return p, "left as is, because you did not type the branch's name", nil
	}
	if !p.changed() {
		return p, "up to date", nil
	}
	ok, err := x.Approve("Accept these for " + name + "?")
	switch {
	case x.Options.DryRun:
		return p, "dry run: nothing was written", nil
	case errors.Is(err, engine.ErrStopped):
		return p, "left as is, because you said no", nil
	case err != nil:
		return p, "", err
	case !ok:
		return p, "left as is, because you said no", nil
	}
	if err := p.accept(x); err != nil {
		return p, "", err
	}
	return p, "accepted", nil
}

func (p *syncRepo) accept(x *engine.Ctx) error {
	if p.keys != nil {
		if err := fleet.WriteSignersCopy(p.keysPath, p.keys); err != nil {
			return err
		}
		x.UI.OK("kept the new keys in " + p.keysPath)
	}
	for _, b := range p.branches {
		switch {
		case b.write != "":
			if err := fleet.WriteTrusted(p.address, b.name, b.write); err != nil {
				return err
			}
			x.UI.OK("you now trust " + b.name + " at " + fleet.Short(b.write))
		case b.remove:
			if err := fleet.RemoveTrusted(p.address, b.name); err != nil {
				return err
			}
			x.UI.OK("the record of " + b.name + " is gone")
		}
	}
	return fleet.MoveClone(x, p.clone, p.mainTip)
}

// review fetches the clone and checks every branch, filling in the plan and
// the problems. Nothing is written.
func (p *syncRepo) review(x *engine.Ctx, rewrite string) {
	clone, err := fleet.ClonePathOfID(p.id)
	if err != nil {
		p.fail(x, err.Error())
		return
	}
	p.clone = clone
	if !fileExists(clone) {
		p.fail(x, "its clone is not in "+clone+": run fleet repo add again with its address")
		return
	}
	if p.address, err = fleet.OriginOf(x, clone); err != nil {
		p.fail(x, err.Error())
		return
	}
	if _, err := engine.Cmd("git", "-C", clone, "fetch", "-q", "--prune", "origin").
		ReadOnly().Quiet().OK("fetched origin into " + clone).Run(x); err != nil {
		p.fail(x, "origin could not be fetched")
		return
	}
	if p.mainTip, err = fleet.CommitOf(x, clone, "refs/remotes/origin/main"); err != nil {
		p.fail(x, "origin has no main")
		return
	}
	recs, err := fleet.Records(p.id)
	if err != nil {
		p.fail(x, err.Error())
		return
	}
	file, cleanup, ok := p.signersFile(x, recs["main"])
	defer cleanup()
	if !ok {
		return
	}

	// The branches: those origin has, and those it has dropped.
	res, err := fleet.GitIn(x, clone, "for-each-ref", "--format=%(refname)",
		"refs/remotes/origin/main", "refs/remotes/origin/host", "refs/remotes/origin/user")
	if err != nil {
		p.fail(x, err.Error())
		return
	}
	tips := map[string]string{}
	var branches []string
	for _, ref := range strings.Fields(res.Stdout) {
		b := strings.TrimPrefix(ref, "refs/remotes/origin/")
		if tips[b], err = fleet.CommitOf(x, clone, ref); err != nil {
			p.fail(x, err.Error())
			return
		}
		branches = append(branches, b)
	}
	for b := range recs {
		if _, ok := tips[b]; ok {
			continue
		}
		if _, err := fleet.CommitOf(x, clone, "refs/remotes/origin/"+b); err != nil {
			branches = append(branches, b) // gone from origin
		}
	}
	sort.Slice(branches, func(i, j int) bool {
		if (branches[i] == "main") != (branches[j] == "main") {
			return branches[i] == "main"
		}
		return branches[i] < branches[j]
	})
	var same []string
	for _, b := range branches {
		bp := &syncBranch{name: b, rec: recs[b], tip: tips[b]}
		if bp.tip != "" && bp.tip == bp.rec {
			same = append(same, b)
			continue
		}
		p.branches = append(p.branches, bp)
		p.branch(x, bp, file, rewrite)
	}
	if len(same) > 0 {
		x.UI.Skip(fmt.Sprintf("unchanged: %s", strings.Join(same, ", ")))
	}
}

var recordHash = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// signersFile finds the file of keys that this repository's commits are
// checked against. For a repository with its own fleet/allowed_signers that
// has changed, it is origin's, in a temporary file, once the commit that
// changed it is shown to be signed by a key in your copy.
func (p *syncRepo) signersFile(x *engine.Ctx, mainRec string) (string, func(), bool) {
	nothing := func() {}
	res, err := fleet.GitIn(x, p.clone, "config", "--get", "gpg.ssh.allowedSignersFile")
	file := strings.TrimSpace(res.Stdout)
	if err != nil || file == "" {
		p.fail(x, "the clone has no gpg.ssh.allowedSignersFile: run fleet repo add again")
		return "", nothing, false
	}
	if _, err := fleet.GitIn(x, p.clone, "cat-file", "-e", "refs/remotes/origin/main:fleet/allowed_signers"); err != nil {
		return file, nothing, true // it borrows the keys of another repository
	}
	res, err = fleet.GitIn(x, p.clone, "show", "refs/remotes/origin/main:fleet/allowed_signers")
	if err != nil {
		p.fail(x, err.Error())
		return "", nothing, false
	}
	data := []byte(res.Stdout)
	now, err := fleet.ParseSigners(data)
	if err != nil {
		p.fail(x, "its fleet/allowed_signers: "+err.Error())
		return "", nothing, false
	}
	copyPath, mine, err := fleet.TrustedSigners(p.address)
	if errors.Is(err, fleet.ErrNoSignersCopy) {
		p.fail(x, "you trust no keys for "+p.id+" yet: run fleet repo add again")
		return "", nothing, false
	}
	if err != nil {
		p.fail(x, err.Error())
		return "", nothing, false
	}
	added, removed := fleet.DiffSigners(mine, now)
	if len(added) == 0 && len(removed) == 0 {
		return file, nothing, true
	}
	x.UI.Note("Its keys differ from the ones you trust:")
	x.UI.Text(signersChange(added, removed))
	rng := []string{"refs/remotes/origin/main"}
	if mainRec != "" {
		if _, err := fleet.GitIn(x, p.clone, "cat-file", "-e", mainRec+"^{commit}"); err == nil {
			rng = []string{mainRec + "..refs/remotes/origin/main"}
		}
	}
	res, err = fleet.GitIn(x, p.clone, append(append([]string{"log", "-1", "--format=%H"}, rng...), "--", "fleet/allowed_signers")...)
	c := strings.TrimSpace(res.Stdout)
	if err != nil || c == "" {
		p.fail(x, "its keys differ from yours, but no commit after the one you trusted changed them")
		return "", nothing, false
	}
	if _, err := fleet.GitIn(x, p.clone, "-c", "gpg.ssh.allowedSignersFile="+copyPath, "verify-commit", c); err != nil {
		p.fail(x, "its keys changed in "+fleet.Short(c)+", which no key you trust signed")
		return "", nothing, false
	}
	x.UI.OK("the change to its keys, in " + fleet.Short(c) + ", is signed by a key you trust")
	tmp, err := os.CreateTemp("", "fleet-signers-*")
	if err != nil {
		p.fail(x, err.Error())
		return "", nothing, false
	}
	cleanup := func() { _ = os.Remove(tmp.Name()) }
	_, werr := tmp.Write(data)
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		cleanup()
		p.fail(x, werr.Error())
		return "", nothing, false
	}
	p.keys, p.keysPath = data, copyPath
	return tmp.Name(), cleanup, true
}

// branch checks one changed branch against the file of keys and plans what
// accepting does to it.
func (p *syncRepo) branch(x *engine.Ctx, b *syncBranch, file, rewrite string) {
	user := strings.HasPrefix(b.name, "user/")
	if b.tip == "" {
		x.UI.Skip(b.name + " is gone from origin: its record goes when you accept")
		b.remove = true
		return
	}
	if b.rec != "" && !recordHash.MatchString(b.rec) {
		p.fail(x, "the record of "+b.name+" does not hold a commit hash: remove it to trust the branch afresh")
		return
	}
	follows := false
	if b.rec != "" {
		var err error
		if follows, err = fleet.Follows(x, p.clone, b.rec, b.tip); err != nil {
			p.fail(x, b.name+": "+err.Error())
			return
		}
	}
	rewritten := b.rec != "" && !follows
	if b.name == rewrite {
		p.rewriteSeen = true
	}
	switch {
	case b.rec == "":
		x.UI.OK(b.name + " is new: first trusted commit " + fleet.Short(b.tip))
		p.show(x, file, "-1", b.tip)
	case follows:
		x.UI.OK(b.name + " follows " + fleet.Short(b.rec) + ", the commit you trusted")
		p.show(x, file, b.rec+".."+b.tip)
	default:
		msg := b.name + " does not follow " + fleet.Short(b.rec) + ", the commit you trusted"
		if b.name != rewrite {
			p.fail(x, msg+": if the history was rewritten on purpose, review it with fleet repo sync "+p.name+" --accept-rewrite "+b.name)
			return
		}
		x.UI.Note(msg + "; you asked to accept the rewrite")
	}
	// The tip check.
	_, err := fleet.GitIn(x, p.clone, "-c", "gpg.ssh.allowedSignersFile="+file, "verify-commit", b.tip)
	signed := err == nil
	left := false
	switch {
	case signed:
		x.UI.OK(b.name + "'s newest commit is signed by a key you trust")
	case user:
		fp := "no key"
		if res, err := fleet.GitIn(x, p.clone, "-c", "gpg.ssh.allowedSignersFile="+file, "log", "-1", "--format=%GK", b.tip); err == nil && strings.TrimSpace(res.Stdout) != "" {
			fp = strings.TrimSpace(res.Stdout)
		}
		x.UI.Note("left: " + b.name + " is signed by " + fp + ", which is not one of your keys; the next secrets command on " +
			b.name + " checks it against " + strings.TrimPrefix(b.name, "user/") + "'s own key")
		left = true
	default:
		p.fail(x, b.name+"'s newest commit, "+fleet.Short(b.tip)+", is not signed by a key you trust")
		return
	}
	if rewritten {
		if !p.confirmRewrite(x, b, file) {
			return
		}
		if user {
			b.remove = true
		} else {
			b.write = b.tip
		}
		return
	}
	if !left {
		b.write = b.tip
	}
}

// confirmRewrite shows what a rewrite dropped and added, and asks for the
// branch's name to be typed. It says whether the rewrite is taken.
func (p *syncRepo) confirmRewrite(x *engine.Ctx, b *syncBranch, file string) bool {
	if _, err := fleet.GitIn(x, p.clone, "cat-file", "-e", b.rec+"^{commit}"); err == nil {
		x.UI.Note("Dropped, in the commits you trusted and not in the branch now:")
		p.show(x, file, b.tip+".."+b.rec)
		x.UI.Note("New, in the branch and not in the commits you trusted:")
		p.show(x, file, b.rec+".."+b.tip)
	} else {
		x.UI.Note("The commit you trusted, " + fleet.Short(b.rec) + ", is not in the clone, so what was dropped cannot be shown. The branch's newest commit:")
		p.show(x, file, "-1", b.tip)
	}
	switch {
	case x.Options.DryRun:
		x.UI.Skip("dry run: you would be asked to type " + b.name)
		return true
	case !x.UI.Interactive():
		p.fail(x, "--accept-rewrite needs you at a terminal to type the branch's name")
		return false
	}
	ok, err := x.UI.Typed("Accept the rewrite of "+b.name+"?", b.name)
	if err != nil {
		p.fail(x, err.Error())
		return false
	}
	if !ok {
		x.UI.Skip("that is not the branch's name: the rewrite is not accepted")
		p.declined = true
		return false
	}
	return true
}

const syncShown = 30

// show lists commits, newest first, one line each: hash, date, author,
// subject, and who signed it.
func (p *syncRepo) show(x *engine.Ctx, file string, rng ...string) {
	args := append([]string{"-c", "gpg.ssh.allowedSignersFile=" + file, "log",
		"--format=%H%x1f%G?%x1f%GS%x1f%GK%x1f%ad%x1f%an%x1f%s", "--date=short"}, rng...)
	res, err := fleet.GitIn(x, p.clone, args...)
	if err != nil {
		x.UI.Note("the commits could not be listed: " + err.Error())
		return
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	var out []string
	for i, l := range lines {
		if i == syncShown {
			out = append(out, fmt.Sprintf("… and %d more", len(lines)-syncShown))
			break
		}
		f := strings.SplitN(l, "\x1f", 7)
		if len(f) != 7 {
			continue
		}
		hash, status, signer, key, date, author, subject := f[0], f[1], f[2], f[3], f[4], f[5], f[6]
		var sig string
		switch {
		case status == "G":
			sig = "✓ " + signer + " " + key
		case key != "":
			sig = "✗ " + key
		case status == "N":
			sig = "✗ unsigned"
		default:
			sig = "✗ not verified (" + status + ")"
		}
		out = append(out, fleet.Short(hash)+" "+date+" "+author+" "+subject+"  "+sig)
	}
	x.UI.Text(strings.Join(out, "\n"))
}
