package flows

import (
	"errors"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

const (
	invAddress = "git@github.com:example-org/inventory.git"
	oldCommit  = "cccccccccccccccccccccccccccccccccccccccc"
	newCommit  = "dddddddddddddddddddddddddddddddddddddddd"
)

func trustedOf(t *testing.T, address, branch string) string {
	t.Helper()
	r, err := fleet.ReadTrusted(address, branch)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func notAncestor(s engine.Spec, older, newer string) bool {
	args := strings.Join(s.Args, " ")
	return s.Name == "git" && strings.HasSuffix(args, "merge-base --is-ancestor "+older+" "+newer)
}

// A host's tip that does not follow the commit you trusted last stops a
// secrets command before anything is changed, naming the record to remove;
// with the record gone the same tip is taken, and recorded.
func TestHostSecretRefusesATipThatDoesNotFollowTheRecord(t *testing.T) {
	run := func() (error, *engine.Fake, string) {
		x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
			args := strings.Join(s.Args, " ")
			if res, ok := hostFile(s); ok {
				return res, nil
			}
			switch {
			case s.Name == "yq" && strings.HasPrefix(args, "-o=json"):
				return engine.Result{Stdout: `{"sops": {}}`}, nil
			case s.Name == "git" && strings.HasSuffix(args, "rev-parse --verify -q refs/heads/host/nas^{commit}"):
				return engine.Result{Stdout: newCommit + "\n"}, nil
			case notAncestor(s, oldCommit, newCommit):
				return engine.Result{Code: 1}, errors.New("exited with code 1")
			}
			return engine.Result{}, nil
		})
		x.Stdin = strings.NewReader("the passphrase")
		err := engine.RunWith(x, HostSecret, map[string]string{
			"repo": t.TempDir(), "recovery": "", "host": "nas", "name": "token", "value-stdin": "true",
		})
		return err, fake, out.String()
	}
	cacheRecovery(t)
	if err := fleet.WriteTrusted(invAddress, "host/nas", oldCommit); err != nil {
		t.Fatal(err)
	}
	err, fake, out := run()
	if err == nil || !strings.Contains(out, "host/nas at "+newCommit+" does not follow "+oldCommit) {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	p, _ := fleet.TrustedPath(invAddress, "host/nas")
	if !strings.Contains(out, "rm "+p) {
		t.Errorf("did not say what to remove:\n%s", out)
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "sops set") || strings.HasPrefix(c, "git commit") || strings.HasPrefix(c, "git push") {
			t.Errorf("changed something: %s", c)
		}
	}
	if r := trustedOf(t, invAddress, "host/nas"); r != oldCommit {
		t.Errorf("the record moved to %s", r)
	}
	if err := fleet.RemoveTrusted(invAddress, "host/nas"); err != nil {
		t.Fatal(err)
	}
	if err, _, out := run(); err != nil {
		t.Fatalf("after the record was removed: %v\n%s", err, out)
	}
}

// The first check records the tip; what the command pushes is what is
// recorded after the push.
func TestHostSecretAdvancesTheRecordToWhatItPushed(t *testing.T) {
	cacheRecovery(t)
	resolved := 0
	var atPush string
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		if res, ok := hostFile(s); ok {
			return res, nil
		}
		switch {
		case s.Name == "yq" && strings.HasPrefix(args, "-o=json"):
			return engine.Result{Stdout: `{"sops": {}}`}, nil
		case s.Name == "git" && strings.HasSuffix(args, "rev-parse --verify -q refs/heads/host/nas^{commit}"):
			resolved++
			if resolved == 1 {
				return engine.Result{Stdout: oldCommit + "\n"}, nil
			}
			return engine.Result{Stdout: newCommit + "\n"}, nil
		case s.Name == "git" && strings.HasSuffix(args, "push origin host/nas"):
			atPush = trustedOf(t, invAddress, "host/nas")
		}
		return engine.Result{}, nil
	})
	x.Stdin = strings.NewReader("the passphrase")
	err := engine.RunWith(x, HostSecret, map[string]string{
		"repo": t.TempDir(), "recovery": "", "host": "nas", "name": "token", "value-stdin": "true",
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(strings.Join(fake.Commands(), "\n"), "push origin host/nas") {
		t.Fatalf("did not push:\n%s", out)
	}
	if atPush != oldCommit {
		t.Errorf("before the push the record was %q, want the first tip %s", atPush, oldCommit)
	}
	if r := trustedOf(t, invAddress, "host/nas"); r != newCommit {
		t.Errorf("after the push the record is %q, want %s", r, newCommit)
	}
}

func TestHostRetireForgetsTheRecordAndKeepsOthers(t *testing.T) {
	cacheRecovery(t)
	for _, b := range []string{"host/nas", "host/pi"} {
		if err := fleet.WriteTrusted(invAddress, b, oldCommit); err != nil {
			t.Fatal(err)
		}
	}
	x, _, out := newFakeCtx(func(engine.Spec) (engine.Result, error) { return engine.Result{}, nil })
	if err := engine.RunWith(x, SecretsHostRetire, map[string]string{"repo": t.TempDir(), "host": "nas"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r := trustedOf(t, invAddress, "host/nas"); r != "" {
		t.Errorf("host/nas still has a record: %s", r)
	}
	if r := trustedOf(t, invAddress, "host/pi"); r != oldCommit {
		t.Errorf("host/pi lost its record: %q", r)
	}
}

func TestUserRetireForgetsTheRecord(t *testing.T) {
	r := &userRepo{hosts: []string{"nas"}, userBranch: true}
	cacheRecovery(t)
	if err := fleet.WriteTrusted(invAddress, "user/alice", oldCommit); err != nil {
		t.Fatal(err)
	}
	x, _, out := newFakeCtx(r.respond)
	if err := engine.RunWith(x, SecretsUserRetire, map[string]string{"repo": t.TempDir(), "recovery": "", "user": "alice"}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := trustedOf(t, invAddress, "user/alice"); got != "" {
		t.Errorf("user/alice still has a record: %s", got)
	}
}

// Install prep: the configuration commit and the fleet-manager pin are
// checked against the commits you trusted.
func TestPrepRecordsWhatItChecksOnFirstUse(t *testing.T) {
	err, _, out := runPrep(t, prepCheckout(t, pinnedFM), prepRepo(invAddress, ""))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if r := trustedOf(t, invAddress, "main"); r != pinnedINV {
		t.Errorf("the configuration's record is %q, want %s", r, pinnedINV)
	}
	// The fleet-manager pin is checked against the record, and makes none: a
	// pin is not the tip of fleet-manager's main.
	if r := trustedOf(t, fleet.UpstreamURL, "main"); r != prepRecord {
		t.Errorf("a pin made fleet-manager's record %q", r)
	}
}

func TestPrepRefusesAConfigurationCommitThatDoesNotFollowTheRecord(t *testing.T) {
	flake := prepCheckout(t, pinnedFM)
	if err := fleet.WriteTrusted(invAddress, "main", oldCommit); err != nil {
		t.Fatal(err)
	}
	err, _, out := runPrep(t, flake, func(s engine.Spec) (engine.Result, error) {
		if notAncestor(s, oldCommit, pinnedINV) {
			return engine.Result{Code: 1}, errors.New("exited with code 1")
		}
		return prepRepo(invAddress, "")(s)
	})
	if err == nil || !strings.Contains(out, "main at "+pinnedINV+" does not follow "+oldCommit) {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	if strings.Contains(out, "sh /tmp/bootstrap") {
		t.Error("printed the line anyway")
	}
}

// The pin may be the trusted commit, one before it, or one after it, and the
// record stays where it is; any other commit is off the line.
func TestPrepHoldsTheFleetManagerPinToTheTrustedLine(t *testing.T) {
	for _, tc := range []struct {
		name             string
		pinFollowsRec    bool // the record is an ancestor of the pin
		pinBehindRec     bool // the pin is an ancestor of the record
		wantErr, wantRec string
	}{
		{"after", true, false, "", oldCommit},
		{"before", false, true, "", oldCommit},
		{"off", false, false, "have gone apart", oldCommit},
	} {
		flake := prepCheckout(t, pinnedFM)
		if err := fleet.WriteTrusted(fleet.UpstreamURL, "main", oldCommit); err != nil {
			t.Fatal(err)
		}
		err, fake, out := runPrep(t, flake, func(s engine.Spec) (engine.Result, error) {
			switch {
			case notAncestor(s, oldCommit, pinnedFM) && !tc.pinFollowsRec,
				notAncestor(s, pinnedFM, oldCommit) && !tc.pinBehindRec:
				return engine.Result{Code: 1}, errors.New("exited with code 1")
			}
			return prepRepo(invAddress, "")(s)
		})
		if tc.wantErr == "" && err != nil || tc.wantErr != "" && (err == nil || !strings.Contains(out, tc.wantErr)) {
			t.Errorf("%s: got %v\n%s", tc.name, err, out)
		}
		if r := trustedOf(t, fleet.UpstreamURL, "main"); r != tc.wantRec {
			t.Errorf("%s: the record is %q, want %s", tc.name, r, tc.wantRec)
		}
		if !strings.Contains(strings.Join(fake.Commands(), "\n"), "fetch -q "+fleet.UpstreamURL+" +refs/heads/main:refs/remotes/origin/main") {
			t.Errorf("%s: did not fetch main's history to compare", tc.name)
		}
	}
}

// The pin is signed, and so is the commit on top, but one between them is not:
// the installer would be built from it, so prep stops.
func TestPrepRefusesAPinAheadOfTheRecordOverAnUnsignedCommit(t *testing.T) {
	flake := prepCheckout(t, pinnedFM)
	err, _, out := runPrep(t, flake, func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		if s.Name == "git" && strings.Contains(args, " log ") && strings.HasSuffix(args, prepRecord+".."+pinnedFM) {
			return engine.Result{Stdout: sigLine(pinnedFM, "G", "SHA256:good", "feat: on top") +
				sigLine("8888888888888888888888888888888888888888", "N", "", "fix: sneaked in")}, nil
		}
		return prepRepo(invAddress, "")(s)
	})
	if err == nil || !strings.Contains(flat(out), "1 of the 2 commits from "+fleet.Short(prepRecord)+" to "+fleet.Short(pinnedFM)) ||
		!strings.Contains(out, "✗ unsigned fix: sneaked in") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	if strings.Contains(out, "sh /tmp/bootstrap") {
		t.Error("printed the line anyway")
	}
	if r := trustedOf(t, fleet.UpstreamURL, "main"); r != prepRecord {
		t.Errorf("the record moved to %s", r)
	}
}

// Behind the record, the commits were checked when it moved there: nothing is
// listed, and the pin is accepted.
func TestPrepAcceptsAPinBehindTheRecordWithoutListingCommits(t *testing.T) {
	flake := prepCheckout(t, pinnedFM)
	err, fake, out := runPrep(t, flake, func(s engine.Spec) (engine.Result, error) {
		if notAncestor(s, prepRecord, pinnedFM) {
			return engine.Result{Code: 1}, errors.New("exited with code 1")
		}
		return prepRepo(invAddress, "")(s)
	})
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(flat(out), "its commits were checked as the record moved on") {
		t.Errorf("did not say so:\n%s", out)
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, " log ") && strings.Contains(c, "%G?") {
			t.Errorf("listed a range: %s", c)
		}
	}
}

// A pin with no commit trusted on fleet-manager's main to start from is
// refused, naming the command that makes one.
func TestPrepNeedsATrustedCommitOfFleetManagerToCheckThePinFrom(t *testing.T) {
	flake := prepCheckout(t, pinnedFM)
	if err := fleet.RemoveTrusted(fleet.UpstreamURL, "main"); err != nil {
		t.Fatal(err)
	}
	err, _, out := runPrep(t, flake, prepRepo(invAddress, ""))
	if err == nil || !strings.Contains(flat(out), "run fleet repo sync fleet-manager, or fleet repo add, first") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
}
