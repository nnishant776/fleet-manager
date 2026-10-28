package fleet

// A signature on the newest commit of a range does not vouch for the commits
// under it: a signed commit can sit on unsigned ones. These check every commit
// of a range.

import (
	"fmt"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// RangeSignatures checks each commit of base..tip in the checkout dir. signers
// is the file of keys to check against, or "" for the repository's own
// gpg.ssh.allowedSignersFile setting. It returns, for each commit that no key
// in the file signed, a line "short-hash ✗ <fingerprint, or unsigned> subject",
// newest first, and how many commits the range holds.
func RangeSignatures(x *engine.Ctx, dir, signers, base, tip string) (bad []string, total int, err error) {
	args := []string{}
	if signers != "" {
		args = append(args, "-c", "gpg.ssh.allowedSignersFile="+signers)
	}
	args = append(args, "log", "--format=%H%x1f%G?%x1f%GK%x1f%s", base+".."+tip)
	res, err := gitIn(x, dir, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("listing the commits from %s to %s in %s: %w", short(base), short(tip), dir, err)
	}
	for _, l := range strings.Split(strings.TrimSpace(res.Stdout), "\n") {
		if l == "" {
			continue
		}
		f := strings.SplitN(l, "\x1f", 4)
		if len(f) != 4 {
			continue
		}
		total++
		hash, status, key, subject := f[0], f[1], f[2], f[3]
		if status == "G" {
			continue
		}
		who := "unsigned"
		switch {
		case key != "":
			who = key
		case status != "N":
			who = "not verified (" + status + ")"
		}
		bad = append(bad, short(hash)+" ✗ "+who+" "+subject)
	}
	return bad, total, nil
}

// UnverifiedCommits lists the commits of base..tip that no key in the signers
// file signed; see RangeSignatures.
func UnverifiedCommits(x *engine.Ctx, dir, signers, base, tip string) ([]string, error) {
	bad, _, err := RangeSignatures(x, dir, signers, base, tip)
	return bad, err
}

// CountCommits is how many commits base..tip holds.
func CountCommits(x *engine.Ctx, dir, base, tip string) (int, error) {
	_, n, err := RangeSignatures(x, dir, "", base, tip)
	return n, err
}

// checkRangeSigned refuses to take origin's main, at tip, over commits no key of
// the clone's own signers file signed. The commit you trusted last is where the
// range starts; without one (first use) or when it is not an ancestor of tip,
// there is no range, and CheckTrusted says what it says.
func checkRangeSigned(x *engine.Ctx, dir, address, tip string) error {
	rec, err := ReadTrusted(address, "main")
	if err != nil {
		return err
	}
	tipHash, err := commitOf(x, dir, tip)
	if err != nil {
		return err
	}
	if rec == "" || rec == tipHash {
		return nil
	}
	ok, err := follows(x, dir, rec, tipHash)
	if err != nil || !ok {
		return nil
	}
	bad, _, err := RangeSignatures(x, dir, "", rec, tipHash)
	if err != nil {
		return err
	}
	if len(bad) == 0 {
		return nil
	}
	where := dir
	if id, err := RepoID(address); err == nil {
		if n := NameOf(id); n != "" {
			where = n
		}
	}
	return fmt.Errorf("%d of the commits on main since %s, the commit you trusted, are not signed by a key you trust:\n  %s\n"+
		"Review them with fleet repo sync %s", len(bad), short(rec), strings.Join(bad, "\n  "), where)
}
