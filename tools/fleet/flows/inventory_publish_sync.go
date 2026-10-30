package flows

import (
	"fmt"
	"strings"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
)

// syncInput is publish's --sync.
var syncInput = engine.Input{
	Name: "sync", Prompt: "Sync each host whose branch changed?",
	Help: "Sync each host whose branch changed, once it is pushed", Bool: true,
}

// syncPublishedStep syncs the hosts whose branches were pushed; left out of
// the run unless asked for.
var syncPublishedStep = engine.Step{
	Title: "Sync the hosts whose branch changed",
	Why:   "Each host is told to sync now, instead of at its next timer, and its revision is checked against the commit just pushed. A host that fails does not stop the others.",
	Hide:  func(x *engine.Ctx) bool { return x.Value("sync") != "true" },
	Do:    engine.Func(syncPublished),
}

func syncPublished(x *engine.Ctx) error {
	if x.Options.DryRun {
		x.UI.Skip("dry run: each host whose branch changed would be synced once pushed")
		return nil
	}
	refs := strings.Fields(x.Value("pushed_refs"))
	if len(refs) == 0 {
		if len(strings.Fields(x.Value("publish_refs"))) == 0 {
			x.UI.Skip("no host's branch changed, so none is synced")
		} else {
			x.UI.Skip("nothing was pushed, so none is synced")
		}
		return nil
	}
	// The host's values are the flow's own for the length of each sync.
	saved := map[string]string{}
	for _, k := range []string{"host", "address", "user"} {
		saved[k] = x.Values[k]
	}
	defer func() {
		for k, v := range saved {
			x.Values[k] = v
		}
	}()
	var failed []string
	for _, r := range refs {
		commit, ref, _ := strings.Cut(r, ":")
		host := strings.TrimPrefix(ref, "refs/heads/host/")
		x.Values["host"], x.Values["address"], x.Values["user"] = host, host, "support"
		x.UI.Text(host + ":")
		if err := syncPublishedHost(x, commit, "the commit just pushed to host/"+host); err != nil {
			x.UI.Fail(host + ": " + err.Error())
			failed = append(failed, host)
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d hosts did not sync to what was pushed: %s", len(failed), len(refs), strings.Join(failed, ", "))
	}
	return nil
}

// syncPublishedHost reaches the host, has it sync, and checks that it then
// runs commit.
func syncPublishedHost(x *engine.Ctx, commit, what string) error {
	res, err := hostReachProbe.Cmd.RunQuiet(x)
	if err != nil {
		return err
	}
	detail, err := hostReachProbe.Expect(res.Stdout)
	if err != nil {
		return err
	}
	x.UI.OK(x.Value("user") + " at " + x.Value("address") + ": " + detail)
	if err := startHostSync(x); err != nil {
		return err
	}
	return checkRunning(x, "", commit, what)
}
