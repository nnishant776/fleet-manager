package engine

import (
	"os"
	"strings"
	"testing"
	"time"
)

func adoptFlow() *Flow {
	return &Flow{Path: []string{"host", "adopt"}, Title: "Adopt", Args: []string{"host"}, Inputs: []Input{
		{Name: "host"}, {Name: "address"}, {Name: "skeleton", Bool: true},
		{Name: "tag", Many: true}, {Name: "password", Secret: true},
	}}
}

func TestGoOnLineBuildsTheCommand(t *testing.T) {
	f := adoptFlow()
	answers := map[string]string{"host": "laptop3", "address": "192.0.2.10", "skeleton": "true", "tag": "a\nb"}
	want := "fleetmgr host adopt laptop3 --address 192.0.2.10 --skeleton --tag a --tag b"
	if got := GoOnLine(f, answers, Options{}); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}

	answers["skeleton"] = "false"
	if got := GoOnLine(f, answers, Options{}); !strings.Contains(got, " --skeleton=false ") {
		t.Errorf("false bool: %s", got)
	}

	answers["address"] = "a b"
	if got := GoOnLine(f, answers, Options{}); !strings.Contains(got, "--address 'a b'") {
		t.Errorf("quoting: %s", got)
	}

	answers["host"] = "-odd"
	got := GoOnLine(f, answers, Options{})
	// Not a positional argument, nor a flag's separate word: either would read
	// as a flag of its own.
	if !strings.HasPrefix(got, "fleetmgr host adopt --host=-odd ") {
		t.Errorf("argument starting with -: %s", got)
	}

	answers["host"] = "h"
	if got := GoOnLine(f, answers, Options{Yes: true, Verbose: true}); !strings.HasSuffix(got, " --yes --verbose") {
		t.Errorf("options: %s", got)
	}
}

// failFlow's second step fails; its answers are what the run was given.
func failFlow(inputs ...Input) *Flow {
	return &Flow{Path: []string{"host", "check"}, Title: "Check", Args: []string{"host"},
		Inputs: append([]Input{{Name: "host"}, {Name: "address"}}, inputs...),
		Steps: []Step{
			{Title: "one", Do: Cmd("ok").ReadOnly()},
			{Title: "two", Do: Cmd("fails").ReadOnly()},
		}}
}

func readRecord(t *testing.T, f *Flow, subject string) (*Unfinished, string) {
	t.Helper()
	path, err := runFile(f.Path, subject)
	if err != nil {
		t.Fatal(err)
	}
	u, err := loadUnfinished(path, time.Now())
	if err != nil {
		t.Fatalf("no record: %v", err)
	}
	return u, path
}

func TestOnlyFlagsAndPromptsAreRecorded(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f := &Flow{Path: []string{"t", "answers"}, Title: "t", Args: []string{"a"},
		Inputs: []Input{
			{Name: "a"},
			{Name: "b", Default: func(*Ctx) (string, error) { return "dflt", nil }},
			{Name: "c", Optional: true},
			{Name: "s", Secret: true},
			{Name: "d"},
		},
		Steps: []Step{{Title: "one", Do: Cmd("fails").ReadOnly()}}}
	x, _, _ := newCtx(Options{}, failing("fails"))
	x.Resumable = true
	x.Stdin = strings.NewReader("pw\n")
	err := RunWith(x, f, map[string]string{"a": "va", "s-stdin": "true", "d": "-"})
	if err == nil {
		t.Fatal("expected a failure")
	}
	u, _ := readRecord(t, f, "va")
	if len(u.Answers) != 1 || u.Answers["a"] != "va" {
		t.Errorf("recorded %v, want only a=va", u.Answers)
	}
	if len(x.answers) != 1 || x.answers["a"] != "va" {
		t.Errorf("c.answers %v", x.answers)
	}
}

func TestAFailedRunPrintsTheLineAndKeepsTheAnswers(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f := failFlow()
	flags := map[string]string{"host": "nas", "address": "10.0.0.1"}
	x, _, out := newCtx(Options{}, failing("fails"))
	x.Resumable = true
	if err := RunWith(x, f, flags); err == nil {
		t.Fatal("expected a failure")
	}
	line := "fleetmgr host check nas --address 10.0.0.1"
	for _, want := range []string{"To go on from here, run:", line} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	u, path := readRecord(t, f, "nas")
	if !strings.HasSuffix(path, "host-check-nas.json") {
		t.Errorf("path %s", path)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v, %v", st.Mode(), err)
	}
	if d, _ := os.Stat(path[:strings.LastIndex(path, "/")]); d.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", d.Mode())
	}
	if u.Outcome != "failed" || u.Step != 2 || u.Of != 2 || u.Title != "two" || u.Subject != "nas" || u.Flow != "host check" {
		t.Errorf("record %+v", u)
	}

	// The same command and subject, finished: the record goes.
	x, _, out = newCtx(Options{}, nil)
	x.Resumable = true
	if err := RunWith(x, f, flags); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("record remains after success: %v", err)
	}
	if strings.Contains(out.String(), "To go on") {
		t.Errorf("success printed the line:\n%s", out)
	}
}

func TestANonResumableRunPrintsAndKeepsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	f := failFlow()
	x, _, out := newCtx(Options{}, failing("fails"))
	_ = RunWith(x, f, map[string]string{"host": "nas", "address": "10.0.0.1"})
	if strings.Contains(out.String(), "To go on") {
		t.Errorf("printed the line:\n%s", out)
	}
	if _, err := os.Stat(dir + "/fleet"); !os.IsNotExist(err) {
		t.Errorf("state written: %v", err)
	}
}

func TestADryRunPrintsTheLineButKeepsNothing(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	f := failFlow()
	x, _, out := newCtx(Options{DryRun: true}, failing("fails"))
	x.Resumable = true
	if err := RunWith(x, f, map[string]string{"host": "nas", "address": "10.0.0.1"}); err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(out.String(), "To go on from here, run:") || !strings.Contains(out.String(), "--dry-run") {
		t.Errorf("no line:\n%s", out)
	}
	if _, err := os.Stat(dir + "/fleet"); !os.IsNotExist(err) {
		t.Errorf("state written: %v", err)
	}
}

func keep(t *testing.T, f *Flow, subject string, when time.Time, answers map[string]string) string {
	t.Helper()
	path, err := runFile(f.Path, subject)
	if err != nil {
		t.Fatal(err)
	}
	u := Unfinished{Flow: strings.Join(f.Path, " "), Subject: subject, When: when, Outcome: "failed",
		Step: 2, Of: 3, Title: "two", Answers: answers}
	if err := saveUnfinished(path, u); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOfferUnfinished(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f := failFlow()
	saved := map[string]string{"host": "h", "address": "x", "admin": "y"}

	// --resume: flags given now win; the rest comes from the record.
	keep(t, f, "h", time.Now(), saved)
	x, _, _ := newCtx(Options{Resume: true}, nil)
	x.flags = map[string]string{"host": "h", "address": "z"}
	if err := x.offerUnfinished(f); err != nil {
		t.Fatal(err)
	}
	if x.flags["address"] != "z" || x.flags["admin"] != "y" {
		t.Errorf("flags %v", x.flags)
	}

	// --forget: the record goes and nothing is merged.
	path := keep(t, f, "h", time.Now(), saved)
	x, _, _ = newCtx(Options{Forget: true}, nil)
	x.flags = map[string]string{"host": "h"}
	if err := x.offerUnfinished(f); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("record remains: %v", err)
	}
	if len(x.flags) != 1 {
		t.Errorf("flags %v", x.flags)
	}

	// No terminal, no --resume: only a note.
	keep(t, f, "h", time.Now(), saved)
	x, _, out := newCtx(Options{}, nil)
	x.flags = map[string]string{"host": "h"}
	if err := x.offerUnfinished(f); err != nil {
		t.Fatal(err)
	}
	if len(x.flags) != 1 || !strings.Contains(out.String(), "--resume") {
		t.Errorf("flags %v\n%s", x.flags, out)
	}

	// Too old: not offered, and removed.
	path = keep(t, f, "h", time.Now().Add(-15*24*time.Hour), saved)
	x, _, out = newCtx(Options{Resume: true}, nil)
	x.flags = map[string]string{"host": "h"}
	if err := x.offerUnfinished(f); err != nil {
		t.Fatal(err)
	}
	if len(x.flags) != 1 || x.flags["admin"] != "" {
		t.Errorf("old record merged: %v", x.flags)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("old record remains: %v", err)
	}
}

func TestMenuRowsPutUnfinishedRunsFirst(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f := failFlow()
	other := &Flow{Path: []string{"host", "list"}, Title: "List"}
	byPath := map[string]*Flow{"host check": f, "host list": other}
	keep(t, f, "nas", time.Now(), map[string]string{"host": "nas"})
	if err := saveUnfinished(mustPath(t, []string{"gone", "flow"}, "x"), Unfinished{
		Flow: "gone flow", Subject: "x", When: time.Now(), Answers: map[string]string{}}); err != nil {
		t.Fatal(err)
	}

	rows, listed := menuRows([]Section{{Name: "Hosts", Flows: []*Flow{f, other}}}, byPath, time.Now())
	if len(rows) != 3 || len(listed) != 3 {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].Section != "Unfinished" || rows[0].Label != "Go on: Check, nas" {
		t.Errorf("first row %+v", rows[0])
	}
	if listed[0].flow != f || listed[0].unfinished == nil || listed[0].unfinished.Subject != "nas" {
		t.Errorf("first entry %+v", listed[0])
	}
	if rows[1].Section != "Hosts" || rows[1].Label != "Check" || listed[1].unfinished != nil {
		t.Errorf("second row %+v", rows[1])
	}
	if rows[2].Section != "" || rows[2].Label != "List" {
		t.Errorf("third row %+v", rows[2])
	}
}

func mustPath(t *testing.T, flow []string, subject string) string {
	t.Helper()
	p, err := runFile(flow, subject)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The test binary stands in for fleetmgr: Exec runs os.Executable for it.
func TestSubrunHelper(t *testing.T) {
	if os.Getenv("FLEETMGR_TEST_HELPER") != "1" {
		t.Skip("run by TestExecMarksAFleetmgrCommandAsPartOfTheFlow")
	}
	os.Stdout.WriteString("subrun=" + os.Getenv(SubrunEnv) + "\n")
}

func TestExecMarksAFleetmgrCommandAsPartOfTheFlow(t *testing.T) {
	t.Setenv(SubrunEnv, "")
	res, err := Exec{}.Run(t.Context(), Spec{Name: "fleetmgr",
		Args: []string{"-test.run=^TestSubrunHelper$"}, Env: []string{"FLEETMGR_TEST_HELPER=1"}})
	if err != nil || !strings.Contains(res.Stdout, "subrun=1\n") {
		t.Errorf("fleetmgr: %v %q %q", err, res.Stdout, res.Stderr)
	}
	res, err = Exec{}.Run(t.Context(), Spec{Name: "sh", Args: []string{"-c", "echo subrun=$FLEETMGR_SUBRUN"}})
	if err != nil || strings.TrimSpace(res.Stdout) != "subrun=" {
		t.Errorf("other command: %v %q", err, res.Stdout)
	}
}
