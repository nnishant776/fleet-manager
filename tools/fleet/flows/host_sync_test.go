package flows

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nnishant776/fleet-manager/tools/fleet/internal/engine"
	"github.com/nnishant776/fleet-manager/tools/fleet/internal/ui"
)

const (
	syncOld = "eccde35047cbd3a5a09781db27403c7335d83aa6"
	syncTip = "95c22d432af088369612c35b82c6f291c7b322d2"
)

// fakeHosts stands in for the hosts a sync reaches over ssh, one per name:
// what each runs, what the sync does to it, and the published tip.
type fakeHosts struct {
	running  map[string]string // revision each host runs
	becomes  map[string]string // revision each host runs after its sync; absent: it stays
	unitFail map[string]bool   // the sync unit fails on this host
	down     map[string]bool   // ssh cannot reach this host
	url, ref string            // what the configuration says
	tip      string            // origin's newest commit of ref
	synced   []string          // hosts whose sync was started, in order
	logRead  []string          // hosts whose journal was read
}

func (f *fakeHosts) respond(s engine.Spec) (engine.Result, error) {
	switch s.Name {
	case "nix":
		a := strings.Join(s.Args, " ")
		switch {
		case strings.Contains(a, "repo.url"):
			return engine.Result{Stdout: f.url}, nil
		case strings.Contains(a, "repo.ref"):
			return engine.Result{Stdout: f.ref}, nil
		}
	case "git":
		if strings.Contains(strings.Join(s.Args, " "), "rev-parse refs/remotes/origin/") {
			return engine.Result{Stdout: f.tip + "\n"}, nil
		}
	case "ssh":
		login := s.Args[len(s.Args)-2]
		host := login[strings.Index(login, "@")+1:]
		script := s.Args[len(s.Args)-1]
		if f.down[host] {
			return engine.Result{Code: 255, Stderr: "ssh: connect to host " + host + " port 22: Connection refused\n"}, errors.New("exited with code 255")
		}
		rev := f.running[host]
		switch {
		case script == "cat /etc/org/revision":
			return engine.Result{Stdout: rev + "\n"}, nil
		case strings.Contains(script, "systemctl start config-auto-update"):
			f.synced = append(f.synced, host)
			if f.unitFail[host] {
				return engine.Result{Code: 1, Stderr: "Job for config-auto-update.service failed.\n"}, errors.New("exited with code 1")
			}
			if to, ok := f.becomes[host]; ok {
				f.running[host] = to
			}
			return engine.Result{}, nil
		case strings.Contains(script, "journalctl -u config-auto-update"):
			f.logRead = append(f.logRead, host)
			return engine.Result{Stdout: "fetch: commit abc is not signed by a key you trust\n"}, nil
		case strings.Contains(script, "id -un"):
			return engine.Result{Stdout: "support\n" + host + "\n"}, nil
		case strings.Contains(script, "/etc/org/revision"): // the configuration check
			return engine.Result{Stdout: rev + " " + rev + "\n"}, nil
		}
		answers := healthyPi()
		delete(answers, "id -un")
		delete(answers, "/etc/org/revision")
		return answers.respond(s)
	}
	return engine.Result{}, nil
}

func syncCtx(f *fakeHosts) (*engine.Ctx, *engine.Fake, *bytes.Buffer) {
	var out bytes.Buffer
	fake := &engine.Fake{Respond: f.respond}
	return &engine.Ctx{
		Context: context.Background(), UI: ui.NewWith(&out, nil), Runner: fake,
		Options: engine.Options{Yes: true}, Values: map[string]string{}, Stdin: strings.NewReader(""),
		LookPath: func(t string) (string, error) { return "/fake/bin/" + t, nil },
	}, fake, &out
}

// checkout is a folder that looks like a git checkout to the flow.
func checkout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func runHostSync(t *testing.T, f *fakeHosts, flake string) (error, *engine.Fake, string) {
	t.Helper()
	x, fake, out := syncCtx(f)
	err := engine.RunWith(x, HostSync, map[string]string{"host": "pi", "flake": flake})
	return err, fake, out.String()
}

func piSyncing() *fakeHosts {
	return &fakeHosts{
		running: map[string]string{"pi": syncOld},
		becomes: map[string]string{"pi": syncTip},
		url:     "git@example.org:inventory.git", ref: "host/pi", tip: syncTip,
	}
}

func TestHostSyncMovesTheHostToThePublishedTip(t *testing.T) {
	f := piSyncing()
	err, fake, out := runHostSync(t, f, checkout(t))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{
		"pi runs eccde35047cb; host/pi is at 95c22d432af0: the sync takes it there",
		"pi now runs 95c22d432af0, the newest commit of host/pi",
		"✓ secrets snapshot: commit a32bf3e3cc50",
		"✓ system state: running, no failed units",
	} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if len(f.synced) != 1 || len(f.logRead) != 0 {
		t.Errorf("synced %v, read the log of %v", f.synced, f.logRead)
	}
	cmds := strings.Join(fake.Commands(), "\n")
	for _, want := range []string{"fetch -q origin +refs/heads/host/pi:refs/remotes/origin/host/pi", "rev-parse refs/remotes/origin/host/pi", "support@pi"} {
		if !strings.Contains(cmds, want) {
			t.Errorf("did not run %q:\n%s", want, cmds)
		}
	}
}

func TestHostSyncAtTheTipSaysSoAndStillSyncs(t *testing.T) {
	f := piSyncing()
	f.running["pi"], f.becomes = syncTip, nil
	err, _, out := runHostSync(t, f, checkout(t))
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(flat(out), "pi now runs 95c22d432af0, the newest commit of host/pi") {
		t.Errorf("output:\n%s", out)
	}
	if len(f.synced) != 1 {
		t.Errorf("synced %v", f.synced)
	}
}

func TestHostSyncShowsTheLogWhenTheUnitFails(t *testing.T) {
	f := piSyncing()
	f.unitFail = map[string]bool{"pi": true}
	err, _, out := runHostSync(t, f, checkout(t))
	if err == nil || !strings.Contains(cause(err), "the sync failed on pi") || !strings.Contains(cause(err), "journalctl -u config-auto-update") {
		t.Fatalf("got %v\n%s", err, out)
	}
	if !strings.Contains(flat(out), "commit abc is not signed by a key you trust") || len(f.logRead) != 1 {
		t.Errorf("did not show the log (read %v):\n%s", f.logRead, out)
	}
	if strings.Contains(out, "Secrets") && strings.Contains(out, "secrets snapshot") {
		t.Errorf("went on after the failure:\n%s", out)
	}
}

func TestHostSyncFailsWhenTheHostStillRunsTheOldRevision(t *testing.T) {
	f := piSyncing()
	f.becomes = nil
	err, _, out := runHostSync(t, f, checkout(t))
	if err == nil || !strings.Contains(cause(err), "pi still runs eccde35047cb, not 95c22d432af0") {
		t.Fatalf("got %v\n%s", err, out)
	}
	if len(f.logRead) != 1 || !strings.Contains(flat(out), "not signed by a key you trust") {
		t.Errorf("did not show the log:\n%s", out)
	}
	if strings.Contains(out, "secrets snapshot") {
		t.Errorf("went on after the failure:\n%s", out)
	}
}

func TestHostSyncStopsForAHostThatSyncsFromNoRepository(t *testing.T) {
	f := piSyncing()
	f.url = ""
	err, _, out := runHostSync(t, f, checkout(t))
	if err == nil || !strings.Contains(cause(err), "pi syncs from no repository: there is nothing to sync") {
		t.Fatalf("got %v\n%s", err, out)
	}
	if len(f.synced) != 0 {
		t.Errorf("synced %v", f.synced)
	}
}

func TestHostSyncWithoutACheckoutOnlyReportsWhatItRuns(t *testing.T) {
	f := piSyncing()
	err, fake, out := runHostSync(t, f, t.TempDir())
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, want := range []string{"is not a git checkout", "the newest commit of host/pi is not known: the sync applies any new secrets", "pi runs 95c22d432af0"} {
		if !strings.Contains(flat(out), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	for _, c := range fake.Commands() {
		if strings.HasPrefix(c, "git ") {
			t.Errorf("ran git: %s", c)
		}
	}
}

func TestHostSyncStopsWhenTheHostCannotBeReached(t *testing.T) {
	f := piSyncing()
	f.down = map[string]bool{"pi": true}
	err, _, out := runHostSync(t, f, checkout(t))
	if err == nil || len(f.synced) != 0 {
		t.Fatalf("got %v, synced %v\n%s", err, f.synced, out)
	}
	if !strings.Contains(flat(out), "nothing at pi accepts SSH") {
		t.Errorf("output:\n%s", out)
	}
}

// cause is what a failed step said, which the step's error wraps.
func cause(err error) string {
	if err == nil {
		return ""
	}
	if u := errors.Unwrap(err); u != nil {
		return u.Error()
	}
	return err.Error()
}
