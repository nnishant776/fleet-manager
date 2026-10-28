package flows

import (
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/fleet"
)

const (
	pubMain   = "1212121212121212121212121212121212121212"
	pubParent = "3434343434343434343434343434343434343434"
	pubNew    = "5656565656565656565656565656565656565656" // fleet-manager's pin on main
	pubOld    = "7878787878787878787878787878787878787878" // and on host/alpha
	pubSneak  = "9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a9a"
)

func lockPinning(rev string) string {
	return `{"nodes":{"fleet-manager":{"locked":{"type":"git","url":"` + fleet.UpstreamURL + `","rev":"` + rev + `"}},"root":{}},"root":"root","version":7}`
}

// publishRun cuts the branches of an inventory whose main pins fleet-manager at
// mainLock, where host/alpha exists and pins parentLock, and host/beta does
// not exist; sigs is what git log says of the range from the old pin to the
// new.
func publishRun(t *testing.T, hosts, mainLock, parentLock, sigs string) (error, *engine.Fake, string) {
	t.Helper()
	trustCopies(t, invAddress, fleet.UpstreamURL)
	if err := fleet.WriteTrusted(fleet.UpstreamURL, "main", pubOld); err != nil {
		t.Fatal(err)
	}
	x, fake, out := newFakeCtx(func(s engine.Spec) (engine.Result, error) {
		args := strings.Join(s.Args, " ")
		switch {
		case s.Name != "git":
		case strings.HasSuffix(args, "show "+pubMain+":flake.lock"):
			return engine.Result{Stdout: mainLock}, nil
		case strings.HasSuffix(args, "show "+pubParent+":flake.lock"):
			return engine.Result{Stdout: parentLock}, nil
		case strings.Contains(args, "rev-parse -q --verify refs/remotes/origin/host/alpha^{commit}"):
			return engine.Result{Stdout: pubParent + "\n"}, nil
		case strings.HasSuffix(args, "write-tree"):
			return engine.Result{Stdout: "tree-new\n"}, nil
		case strings.HasSuffix(args, "rev-parse "+pubParent+"^{tree}"):
			return engine.Result{Stdout: "tree-old\n"}, nil
		case strings.Contains(args, " log ") && strings.Contains(args, "%G?"):
			return engine.Result{Stdout: sigs}, nil
		}
		return engine.Result{}, nil
	})
	x.Values["flake"], x.Values["main_rev"], x.Values["origin"], x.Values["signers"] = t.TempDir(), pubMain, invAddress, "/signers"
	x.Values["host"], x.Values["no-check"] = hosts, "true"
	x.Values["layout"] = `{"hostBranches":true,"hosts":"config/hosts","users":null,"shared":[]}`
	x.Values["nixos_accounts"] = `{"alpha":["alice"],"beta":["bob"]}`
	x.Values["darwin_accounts"] = `{}`
	err := cutHostBranches(x)
	return err, fake, out.String()
}

func committed(fake *engine.Fake) bool {
	for _, c := range fake.Commands() {
		if strings.Contains(c, " commit-tree ") {
			return true
		}
	}
	return false
}

// A branch whose pin moves over an unsigned commit is not cut, nor is any
// other: the refusal comes before the first commit.
func TestPublishRefusesABranchWhosePinMovesOverAnUnsignedCommit(t *testing.T) {
	sigs := sigLine(pubNew, "G", "SHA256:good", "feat: on top") + sigLine(pubSneak, "N", "", "fix: sneaked in")
	err, fake, out := publishRun(t, "beta alpha", lockPinning(pubNew), lockPinning(pubOld), sigs)
	if err == nil || !strings.Contains(err.Error(), "1 of the 2 commits") || !strings.Contains(err.Error(), "✗ unsigned fix: sneaked in") {
		t.Fatalf("went ahead, or without saying why: %v\n%s", err, out)
	}
	if committed(fake) {
		t.Errorf("made a commit:\n%s", strings.Join(fake.Commands(), "\n"))
	}
}

func TestPublishGoesOnWhenEveryCommitOfThePinIsSigned(t *testing.T) {
	sigs := sigLine(pubNew, "G", "SHA256:good", "feat: on top") + sigLine(pubSneak, "G", "SHA256:good", "fix: also signed")
	err, fake, out := publishRun(t, "alpha", lockPinning(pubNew), lockPinning(pubOld), sigs)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !committed(fake) {
		t.Errorf("made no commit\n%s", out)
	}
	if !strings.Contains(flat(out), "2 commits from "+fleet.Short(pubOld)+" to "+fleet.Short(pubNew)+", each signed by a key you trust") {
		t.Errorf("did not say so:\n%s", out)
	}
}

// A new branch has no pin of its own to start from: the commit you trusted
// last on fleet-manager's main stands in.
func TestPublishChecksANewBranchFromTheRecord(t *testing.T) {
	sigs := sigLine(pubNew, "N", "", "unsigned")
	err, fake, _ := publishRun(t, "beta", lockPinning(pubNew), lockPinning(pubOld), sigs)
	if err == nil || !strings.Contains(err.Error(), "host/beta would pin fleet-manager at "+fleet.Short(pubNew)) {
		t.Fatalf("err = %v", err)
	}
	if committed(fake) {
		t.Error("made a commit")
	}
}

func TestPublishChecksNothingWhenThePinIsUnchanged(t *testing.T) {
	// A listing that would be refused if it were read.
	err, fake, out := publishRun(t, "alpha", lockPinning(pubNew), lockPinning(pubNew), sigLine(pubNew, "N", "", "unsigned"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, "%G?") || strings.Contains(c, "fetch -q") {
			t.Errorf("looked at fleet-manager: %s", c)
		}
	}
	if !committed(fake) {
		t.Error("made no commit")
	}
}

// fleet-manager's own checkout pins nothing.
func TestPublishChecksNothingWithoutAFleetManagerInput(t *testing.T) {
	err, fake, out := publishRun(t, "alpha", `{"nodes":{"root":{}},"root":"root","version":7}`, `{ }`, sigLine(pubNew, "N", "", "unsigned"))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, c := range fake.Commands() {
		if strings.Contains(c, "%G?") {
			t.Errorf("looked at fleet-manager: %s", c)
		}
	}
}
