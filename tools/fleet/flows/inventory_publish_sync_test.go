package flows

import (
	"strings"
	"testing"
)

const (
	pushAlpha = "1111111111111111111111111111111111111111"
	pushBeta  = "2222222222222222222222222222222222222222"
)

// syncPushed runs the step that follows a push, as publish leaves the values:
// alpha and beta were pushed, gamma did not change.
func syncPushed(t *testing.T, f *fakeHosts, refs string) (error, string) {
	t.Helper()
	x, _, out := syncCtx(f)
	x.Values["sync"], x.Values["host"] = "true", "alpha beta gamma"
	x.Values["publish_refs"], x.Values["pushed_refs"] = refs, refs
	err := syncPublished(x)
	if x.Value("host") != "alpha beta gamma" {
		t.Errorf("left host as %q", x.Value("host"))
	}
	return err, out.String()
}

func twoPushed() (*fakeHosts, string) {
	f := &fakeHosts{
		running: map[string]string{"alpha": syncOld, "beta": syncOld, "gamma": syncOld},
		becomes: map[string]string{"alpha": pushAlpha, "beta": pushBeta},
	}
	return f, pushAlpha + ":refs/heads/host/alpha\n" + pushBeta + ":refs/heads/host/beta"
}

func TestPublishSyncSyncsOnlyTheHostsPushedEachAgainstItsOwnCommit(t *testing.T) {
	f, refs := twoPushed()
	err, out := syncPushed(t, f, refs)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if strings.Join(f.synced, " ") != "alpha beta" {
		t.Errorf("synced %v", f.synced)
	}
	for _, want := range []string{
		"alpha now runs 111111111111, the commit just pushed to host/alpha",
		"beta now runs 222222222222, the commit just pushed to host/beta",
	} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestPublishSyncGoesOnToTheNextHostAfterAFailure(t *testing.T) {
	f, refs := twoPushed()
	f.unitFail = map[string]bool{"alpha": true}
	err, out := syncPushed(t, f, refs)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 hosts") || !strings.HasSuffix(err.Error(), ": alpha") {
		t.Fatalf("got %v\n%s", err, out)
	}
	if strings.Join(f.synced, " ") != "alpha beta" {
		t.Errorf("synced %v", f.synced)
	}
	if len(f.logRead) != 1 || f.logRead[0] != "alpha" || !strings.Contains(flat(out), "beta now runs 222222222222") {
		t.Errorf("log read %v:\n%s", f.logRead, out)
	}
}

func TestPublishSyncReportsAHostItCannotReachAndGoesOn(t *testing.T) {
	f, refs := twoPushed()
	f.down = map[string]bool{"alpha": true}
	err, out := syncPushed(t, f, refs)
	if err == nil || !strings.HasSuffix(err.Error(), ": alpha") {
		t.Fatalf("got %v\n%s", err, out)
	}
	if strings.Join(f.synced, " ") != "beta" {
		t.Errorf("synced %v", f.synced)
	}
}

func TestPublishSyncFailsAHostThatStaysOnItsOldRevision(t *testing.T) {
	f, refs := twoPushed()
	f.becomes = map[string]string{"beta": pushBeta}
	err, out := syncPushed(t, f, refs)
	if err == nil || !strings.HasSuffix(err.Error(), ": alpha") {
		t.Fatalf("got %v\n%s", err, out)
	}
	if !strings.Contains(flat(out), "alpha now runs eccde35047cb, not 111111111111") && !strings.Contains(flat(out), "alpha still runs eccde35047cb, not 111111111111") {
		t.Errorf("output:\n%s", out)
	}
}

func TestPublishSyncSyncsNoneWhenNoBranchChanged(t *testing.T) {
	f, _ := twoPushed()
	err, out := syncPushed(t, f, "")
	if err != nil || len(f.synced) != 0 {
		t.Fatalf("%v, synced %v\n%s", err, f.synced, out)
	}
	if !strings.Contains(flat(out), "no host's branch changed, so none is synced") {
		t.Errorf("output:\n%s", out)
	}
}

func TestPublishSyncDryRunShowsAndRunsNothing(t *testing.T) {
	f, refs := twoPushed()
	x, fake, out := syncCtx(f)
	x.Options.DryRun = true
	x.Values["publish_refs"] = refs
	if err := syncPublished(x); err != nil || len(fake.Ran) != 0 {
		t.Fatalf("%v, ran %v\n%s", err, fake.Commands(), out)
	}
}

func TestPublishWithoutSyncHasNoSyncStep(t *testing.T) {
	x, _, _ := syncCtx(&fakeHosts{})
	x.Values["sync"] = "false"
	if !syncPublishedStep.Hide(x) {
		t.Error("the step is shown without --sync")
	}
	x.Values["sync"] = "true"
	if syncPublishedStep.Hide(x) {
		t.Error("the step is hidden with --sync")
	}
}
