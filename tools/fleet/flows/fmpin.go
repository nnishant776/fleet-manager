package flows

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

// A fleet-manager commit that a pin brings in is not vouched for by its own
// signature alone: a signed commit can sit on unsigned ones, and what the pin
// moves over is built on every host. verifyFleetManagerPin checks the pin and
// each commit it brings.

const fmMain = "refs/remotes/origin/main"

// histories remembers, for each run, the history fleetManagerHistory made and
// how to remove it.
var (
	historiesMu sync.Mutex
	histories   = map[*engine.Ctx]func(){}
)

// fleetManagerHistory is a repository holding the whole history of
// fleet-manager's main as refs/remotes/origin/main, and your copy of
// fleet-manager's keys. It is the tools' own clone when there is one, fetched
// (its trusted record and checkout are not touched), else a scratch bare
// repository that cleanup removes.
func fleetManagerHistory(x *engine.Ctx) (dir, signers string, cleanup func(), err error) {
	signers, err = fleet.SignersCopy(fleet.UpstreamURL)
	if err != nil {
		return "", "", nil, err
	}
	if _, serr := os.Stat(signers); serr != nil {
		if !x.Options.DryRun {
			return "", "", nil, errors.New("you trust no keys for fleet-manager yet: run fleet repo add (with no address it adds fleet-manager)")
		}
		x.UI.Skip("dry run: you trust no keys for fleet-manager yet")
	}
	cleanup = func() {}
	id, err := fleet.RepoID(fleet.UpstreamURL)
	if err != nil {
		return "", "", nil, err
	}
	clone, err := fleet.ClonePathOfID(id)
	if err != nil {
		return "", "", nil, err
	}
	if fileExists(clone) {
		dir = clone
		if _, err := engine.Cmd("git", "-C", dir, "fetch", "-q", "origin").ReadOnly().Quiet().
			OK("fetched fleet-manager's history into " + dir).Run(x); err != nil {
			return "", "", nil, err
		}
	} else {
		tmp, err := os.MkdirTemp("", "fleet-manager-history-*")
		if err != nil {
			return "", "", nil, err
		}
		cleanup = func() { _ = os.RemoveAll(tmp) }
		dir = tmp
		for _, c := range []*engine.Command{
			engine.Cmd("git", "init", "-q", "--bare", dir).OK("a scratch repository to fetch fleet-manager's history into"),
			engine.Cmd("git", "-C", dir, "fetch", "-q", fleet.UpstreamURL, "+refs/heads/main:"+fmMain).
				OK("fetched fleet-manager's main from " + fleet.UpstreamURL),
		} {
			if _, err := c.ReadOnly().Quiet().Run(x); err != nil {
				cleanup()
				return "", "", nil, err
			}
		}
	}
	return dir, signers, cleanup, nil
}

// releaseFleetManagerHistory removes the scratch repository that
// verifyFleetManagerPin made in this run, if it made one. Callers that read
// from x.Values["fm_git"] do so before.
func releaseFleetManagerHistory(x *engine.Ctx) {
	historiesMu.Lock()
	cleanup := histories[x]
	delete(histories, x)
	historiesMu.Unlock()
	if cleanup != nil {
		cleanup()
	}
	delete(x.Values, "fm_hist")
}

// verifyFleetManagerPin checks that the fleet-manager commit pin is one to
// build on: a commit of fleet-manager's main, signed by a key you trust, and
// so is each commit it brings that base did not have. base is what was pinned
// before; when it is "", or not in the history, the commit you trusted last on
// main stands in. The history is left in x.Values["fm_git"], to read files
// from; release it with releaseFleetManagerHistory.
func verifyFleetManagerPin(x *engine.Ctx, pin, base string) error {
	dir, signers := x.Value("fm_hist"), x.Value("fm_hist_signers")
	if dir == "" {
		var cleanup func()
		var err error
		if dir, signers, cleanup, err = fleetManagerHistory(x); err != nil {
			return err
		}
		historiesMu.Lock()
		histories[x] = cleanup
		historiesMu.Unlock()
		x.Values["fm_hist"], x.Values["fm_hist_signers"] = dir, signers
	}
	x.Values["fm_git"] = dir
	return verifyPinIn(x, dir, signers, pin, base)
}

// verifyPinIn is verifyFleetManagerPin in a history already fetched.
func verifyPinIn(x *engine.Ctx, dir, signers, pin, base string) error {
	short := fleet.Short

	if _, err := fleet.CommitOf(x, dir, pin); err != nil {
		return fmt.Errorf("%s is not in the history of fleet-manager's main, as fetched: pin only commits that are on main", pin)
	}
	if _, err := engine.Cmd("git", "-C", dir, "-c", "gpg.ssh.allowedSignersFile="+signers, "verify-commit", pin).ReadOnly().Quiet().
		Explain(func(engine.Result) string {
			return "fleet-manager's " + pin + " is not signed by a key you trust for fleet-manager (" + signers + ")"
		}).OK(pin + " is signed by a key you trust").Run(x); err != nil {
		return err
	}
	mainTip, err := fleet.CommitOf(x, dir, fmMain)
	if err != nil {
		return err
	}
	onMain, err := fleet.Follows(x, dir, pin, mainTip)
	if err != nil {
		return err
	}
	if !onMain {
		return fmt.Errorf("%s is not on fleet-manager's main: pin only commits that are on main", pin)
	}

	from, nothingNew, err := pinRangeStart(x, dir, pin, base)
	if err != nil {
		return err
	}
	switch {
	case nothingNew != "":
		x.UI.OK(nothingNew)
	default:
		bad, total, err := fleet.RangeSignatures(x, dir, signers, from, pin)
		if err != nil {
			return err
		}
		if len(bad) > 0 {
			return fmt.Errorf("%d of the %s from %s to %s are not signed by a key you trust (%s), so the signature on %s does not vouch for them:\n  %s",
				len(bad), commits(total), short(from), short(pin), signers, short(pin), strings.Join(bad, "\n  "))
		}
		x.UI.OK(fmt.Sprintf("%s from %s to %s, each signed by a key you trust", commits(total), short(from), short(pin)))
	}
	return fleet.CheckTrustedLine(x, dir, fleet.UpstreamURL, "main", pin)
}

// pinRangeStart is the commit that the commits to check start after: base, or
// where base and pin part, or the commit you trusted last. When the pin brings
// nothing that was not checked before, from is "" and nothingNew says so.
func pinRangeStart(x *engine.Ctx, dir, pin, base string) (from, nothingNew string, err error) {
	short := fleet.Short
	if base != "" {
		if b, err := fleet.CommitOf(x, dir, base); err == nil {
			switch up, err := fleet.Follows(x, dir, b, pin); {
			case err != nil:
				return "", "", err
			case up:
				return b, "", nil
			}
			down, err := fleet.Follows(x, dir, pin, b)
			if err != nil {
				return "", "", err
			}
			if down {
				return "", "an older commit than " + short(b) + ": it brings no commit that was not pinned before", nil
			}
			res, err := fleet.GitIn(x, dir, "merge-base", b, pin)
			mb := strings.TrimSpace(res.Stdout)
			if err != nil || mb == "" {
				return "", "", fmt.Errorf("%s and %s have no commit in common in fleet-manager's history", short(b), short(pin))
			}
			return mb, "", nil
		}
		x.UI.Note("the commit pinned before, " + short(base) + ", is not in fleet-manager's history as fetched: the commits are checked from the one you trusted last")
	}
	rec, err := fleet.ReadTrusted(fleet.UpstreamURL, "main")
	if err != nil {
		return "", "", err
	}
	if rec == "" {
		return "", "", errors.New("you have no trusted commit for fleet-manager's main yet: run fleet repo sync fleet-manager, or fleet repo add, first")
	}
	if rec == pin {
		return "", "it is the commit you trusted on fleet-manager's main: its commits were checked as the record moved to it", nil
	}
	apart := fmt.Errorf("the commit you trusted on fleet-manager's main, %s, and %s have gone apart, or %s is not in its history as fetched: "+
		"neither can be checked against the other. Review it with fleet repo sync fleet-manager", short(rec), short(pin), short(rec))
	if _, err := fleet.CommitOf(x, dir, rec); err != nil {
		return "", "", apart
	}
	up, err := fleet.Follows(x, dir, rec, pin)
	if err != nil {
		return "", "", err
	}
	if up {
		return rec, "", nil
	}
	down, err := fleet.Follows(x, dir, pin, rec)
	if err != nil {
		return "", "", err
	}
	if down {
		return "", "behind " + short(rec) + ", the commit you trusted on fleet-manager's main: its commits were checked as the record moved on", nil
	}
	return "", "", apart
}

// fleetManagerInLock reads the commit that the text of a flake.lock pins
// fleet-manager at. found is false when there is no such input (fleet-manager's
// own checkout, or an empty lock).
func fleetManagerInLock(lock string) (rev string, found bool, err error) {
	if strings.TrimSpace(lock) == "" {
		return "", false, nil
	}
	dir, err := os.MkdirTemp("", "fleet-lock-*")
	if err != nil {
		return "", false, err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(dir+"/flake.lock", []byte(lock), 0o600); err != nil {
		return "", false, err
	}
	rev, url, err := pinnedFleetManager(dir)
	switch {
	case err == nil:
	case strings.Contains(err.Error(), "has no fleet-manager input"):
		return "", false, nil
	default:
		return "", false, err
	}
	have, err1 := fleet.RepoID(url)
	want, err2 := fleet.RepoID(fleet.UpstreamURL)
	if err1 != nil || err2 != nil || have != want {
		return "", false, fmt.Errorf("flake.lock pins fleet-manager from %s, not %s", url, fleet.UpstreamURL)
	}
	if !fullHash.MatchString(rev) {
		return "", false, errors.New("flake.lock pins fleet-manager by no commit")
	}
	return rev, true, nil
}
