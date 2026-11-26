package findings

import (
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var t0 = time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

func bp(b bool) *bool { return &b }

func newFinding(key string, tasks ...TaskRef) Finding {
	return Finding{Key: key, Title: "Agent forgets the tests", Source: "cluster", Origin: "verified", Tasks: tasks}
}

func addOne(t *testing.T, st Store, f Finding) *Finding {
	t.Helper()
	got, _, err := Add(st, f, "Ann", t0)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestAddIsIdempotentByKey(t *testing.T) {
	st := Open(t.TempDir())
	a, created, err := Add(st, newFinding("cluster:abc", TaskRef{"s", "t1"}), "Ann", t0)
	if err != nil || !created {
		t.Fatalf("first add: created %v err %v", created, err)
	}
	if !strings.HasPrefix(a.ID, "fnd-") || len(a.ID) != 16 || a.State != StateOpen {
		t.Fatalf("finding %+v", a)
	}
	b, created, err := Add(st, newFinding("cluster:abc"), "Bob", t0.Add(time.Hour))
	if err != nil || created || b.ID != a.ID {
		t.Fatalf("second add: created %v id %s err %v", created, b.ID, err)
	}
	all, _ := st.List()
	if len(all) != 1 {
		t.Fatalf("%d findings, want 1", len(all))
	}
}

// rerun rows: fixed arm passes n runs over the tasks, old arm fails one.
func rerunRows(exp string, fixedPass []bool, oldPass []bool, tasks ...string) []RunRow {
	var rows []RunRow
	for i, p := range fixedPass {
		rows = append(rows, RunRow{Experiment: exp, RunID: exp + "-f" + string(rune('a'+i)), Suite: "s", Task: tasks[i%len(tasks)], Arm: "fixed", Agent: "bot", ConfigHash: "new", Passed: bp(p)})
	}
	for i, p := range oldPass {
		rows = append(rows, RunRow{Experiment: exp, RunID: exp + "-o" + string(rune('a'+i)), Suite: "s", Task: tasks[i%len(tasks)], Arm: "old", Agent: "bot", ConfigHash: "old", Passed: bp(p)})
	}
	return rows
}

func TestJudgeFixed(t *testing.T) {
	st := Open(t.TempDir())
	f := addOne(t, st, newFinding("cluster:a", TaskRef{"s", "t1"}, TaskRef{"s", "t2"}))
	rows := rerunRows("exp-r", []bool{true, true, true, true}, []bool{false, true}, "t1", "t2")
	got, err := Judge(st, f.ID, "exp-r", "old", "fixed", rows, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateFixed || got.Fix == nil {
		t.Fatalf("state %s fix %+v", got.State, got.Fix)
	}
	fx := got.Fix
	if fx.Experiment != "exp-r" || fx.Agent != "bot" || fx.FixedHash != "new" || fx.OldHash != "old" || fx.FixedPassed != 4 || fx.FixedRuns != 4 || fx.OldPassed != 1 || fx.OldRuns != 2 {
		t.Fatalf("proof %+v", fx)
	}
	last := got.History[len(got.History)-1]
	if last.To != StateFixed || last.By != "Heimdall" || last.Experiment != "exp-r" {
		t.Fatalf("event %+v", last)
	}
}

func TestJudgeOldAlsoPasses(t *testing.T) {
	st := Open(t.TempDir())
	f := addOne(t, st, newFinding("cluster:a", TaskRef{"s", "t1"}))
	rows := rerunRows("exp-r", []bool{true, true, true}, []bool{true, true, true}, "t1")
	got, err := Judge(st, f.ID, "exp-r", "old", "fixed", rows, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateOpen || got.Fix != nil {
		t.Fatalf("state %s", got.State)
	}
	want := "The old agent also passed these tasks, so they may not show the failure. Add a task that fails without the fix."
	if r := got.History[len(got.History)-1].Reason; r != want {
		t.Fatalf("reason %q", r)
	}
}

func TestJudgeFixedStillFails(t *testing.T) {
	st := Open(t.TempDir())
	f := addOne(t, st, newFinding("cluster:a", TaskRef{"s", "t1"}))
	rows := rerunRows("exp-r", []bool{true, false, true}, []bool{false}, "t1")
	got, err := Judge(st, f.ID, "exp-r", "old", "fixed", rows, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateOpen {
		t.Fatalf("state %s", got.State)
	}
	if r := got.History[len(got.History)-1].Reason; r != "The fixed agent still fails 1 of 3 runs." {
		t.Fatalf("reason %q", r)
	}
}

func TestJudgeNeedsThreeRuns(t *testing.T) {
	st := Open(t.TempDir())
	f := addOne(t, st, newFinding("cluster:a", TaskRef{"s", "t1"}))
	rows := rerunRows("exp-r", []bool{true, true}, []bool{false}, "t1")
	got, err := Judge(st, f.ID, "exp-r", "old", "fixed", rows, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateOpen || got.Fix != nil {
		t.Fatalf("state %s", got.State)
	}
	// A linked task with no fixed-arm run also keeps it open.
	f2 := addOne(t, st, newFinding("cluster:b", TaskRef{"s", "t1"}, TaskRef{"s", "t9"}))
	rows = rerunRows("exp-r", []bool{true, true, true}, []bool{false}, "t1")
	got, err = Judge(st, f2.ID, "exp-r", "old", "fixed", rows, t0.Add(time.Hour))
	if err != nil || got.State != StateOpen {
		t.Fatalf("state %s err %v", got.State, err)
	}
}

func fixedFinding(t *testing.T, st Store, key string) *Finding {
	t.Helper()
	f := addOne(t, st, newFinding(key, TaskRef{"s", "t1"}))
	if _, err := Update(st, f.ID, func(f *Finding) error { f.Reruns = []string{"exp-r"}; return nil }); err != nil {
		t.Fatal(err)
	}
	got, err := Judge(st, f.ID, "exp-r", "old", "fixed", rerunRows("exp-r", []bool{true, true, true}, []bool{false}, "t1"), t0.Add(time.Hour))
	if err != nil || got.State != StateFixed {
		t.Fatalf("setup: state %s err %v", got.State, err)
	}
	return got
}

func rowsFor(rows ...RunRow) func(string) ([]RunRow, error) {
	return func(task string) ([]RunRow, error) {
		var out []RunRow
		for _, r := range rows {
			if r.Suite+"/"+r.Task == task {
				out = append(out, r)
			}
		}
		return out, nil
	}
}

func noInbox(string) (*schemas.InboxItem, error) { return nil, nil }

func TestRefreshReopensOnLaterFailure(t *testing.T) {
	st := Open(t.TempDir())
	f := fixedFinding(t, st, "cluster:a")
	later := RunRow{Experiment: "exp-later", ExperimentCreated: t0.Add(2 * time.Hour), RunID: "run-9", Suite: "s", Task: "t1", Arm: "a", Agent: "bot", Passed: bp(false)}
	n, err := Refresh(st, rowsFor(later), noInbox, t0.Add(3*time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("changed %d err %v", n, err)
	}
	got, _ := st.Get(f.ID)
	if got.State != StateReopened {
		t.Fatalf("state %s", got.State)
	}
	if r := got.History[len(got.History)-1].Reason; r != "Task s/t1 failed again in exp-later (run run-9)" {
		t.Fatalf("reason %q", r)
	}
	// Idempotent: a second refresh changes nothing.
	if n, _ := Refresh(st, rowsFor(later), noInbox, t0.Add(4*time.Hour)); n != 0 {
		t.Fatalf("second refresh changed %d", n)
	}
}

func TestRefreshIgnoresOwnRerunAndSynthetic(t *testing.T) {
	st := Open(t.TempDir())
	f := fixedFinding(t, st, "cluster:a")
	rows := []RunRow{
		{Experiment: "exp-r", ExperimentCreated: t0.Add(2 * time.Hour), RunID: "r1", Suite: "s", Task: "t1", Arm: "old", Agent: "bot", Passed: bp(false)},
		{Experiment: "exp-old", ExperimentCreated: t0.Add(-time.Hour), RunID: "r2", Suite: "s", Task: "t1", Agent: "bot", Passed: bp(false)},
		{Experiment: "exp-other", ExperimentCreated: t0.Add(2 * time.Hour), RunID: "r3", Suite: "s", Task: "t1", Agent: "someone-else", Passed: bp(false)},
		{Experiment: "exp-infra", ExperimentCreated: t0.Add(2 * time.Hour), RunID: "r4", Suite: "s", Task: "t1", Agent: "bot", Passed: nil},
	}
	if n, err := Refresh(st, rowsFor(rows...), noInbox, t0.Add(3*time.Hour)); err != nil || n != 0 {
		t.Fatalf("changed %d err %v", n, err)
	}
	got, _ := st.Get(f.ID)
	if got.State != StateFixed {
		t.Fatalf("state %s", got.State)
	}
}

func TestRefreshInboxRecurrence(t *testing.T) {
	st := Open(t.TempDir())
	f := addOne(t, st, Finding{Key: "inbox:inc-000000000001", Title: "x", Source: "inbox", Origin: "verified", Tasks: []TaskRef{{"s", "t1"}}})
	if _, err := Judge(st, f.ID, "exp-r", "old", "fixed", rerunRows("exp-r", []bool{true, true, true}, []bool{false}, "t1"), t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	inbox := func(id string) (*schemas.InboxItem, error) {
		if id != "inc-000000000001" {
			t.Fatalf("item %s", id)
		}
		return &schemas.InboxItem{ID: id, LastRecurrence: &when}, nil
	}
	n, err := Refresh(st, rowsFor(), inbox, when.Add(time.Hour))
	if err != nil || n != 1 {
		t.Fatalf("changed %d err %v", n, err)
	}
	got, _ := st.Get(f.ID)
	if got.State != StateReopened || got.History[len(got.History)-1].Reason != "The same failure came back in production on 3 Nov 2026." {
		t.Fatalf("state %s reason %q", got.State, got.History[len(got.History)-1].Reason)
	}
}

func TestExpectedNeverReopens(t *testing.T) {
	st := Open(t.TempDir())
	f := addOne(t, st, newFinding("cluster:a", TaskRef{"s", "t1"}))
	if _, err := MarkExpected(st, f.ID, "no", "Ann", t0); err == nil {
		t.Fatal("a 2 char reason was taken")
	}
	if _, err := MarkExpected(st, f.ID, "Known flaky upstream API", "Ann", t0); err != nil {
		t.Fatal(err)
	}
	later := RunRow{Experiment: "exp-later", ExperimentCreated: t0.Add(2 * time.Hour), RunID: "run-9", Suite: "s", Task: "t1", Agent: "bot", Passed: bp(false)}
	if n, _ := Refresh(st, rowsFor(later), noInbox, t0.Add(3*time.Hour)); n != 0 {
		t.Fatalf("refresh changed %d", n)
	}
	got, _ := st.Get(f.ID)
	if got.State != StateExpected {
		t.Fatalf("state %s", got.State)
	}
	// A person may reopen it.
	got, err := Reopen(st, f.ID, "It matters now", "Ann", t0.Add(time.Hour))
	if err != nil || got.State != StateReopened {
		t.Fatalf("reopen: state %s err %v", got.State, err)
	}
}
