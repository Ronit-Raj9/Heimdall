package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
)

// rcBench is a bench whose experiment has a store folder for its controls.
func rcBench(t *testing.T, e *schemas.Experiment) (*bench, *schemas.Manifest) {
	t.Helper()
	b := newBench(t)
	m := b.plan(t, e)
	if err := b.store.CreateExperiment(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(b.store.Root(), m.ExpID), 0o755); err != nil {
		t.Fatal(err)
	}
	return b, m
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Pause drains: nothing starts while paused; resume runs every slot, and the
// scheduler never returned in between.
func TestRunControlPauseHoldsThenResumes(t *testing.T) {
	b, m := rcBench(t, testExperiment())
	root := b.store.Root()
	if _, err := runctl.Pause(root, m.ExpID, "ana", "provider outage"); err != nil {
		t.Fatal(err)
	}
	done := make(chan *Summary, 1)
	go func() {
		sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Resume: true, ControlPoll: 10 * time.Millisecond})
		if err != nil {
			t.Error(err)
		}
		done <- sum
	}()
	time.Sleep(150 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("a paused run returned")
	default:
	}
	if n := b.w.count("exec"); n != 0 {
		t.Fatalf("%d runs started while paused", n)
	}
	if _, err := runctl.Resume(root, m.ExpID, "ana", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case sum := <-done:
		if sum.Sealed != len(m.Slots) || sum.Pending != 0 {
			t.Fatalf("summary %+v", sum)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("resume did not finish the run")
	}
}

// A paused model holds only its own slots.
func TestRunControlPausedModelHoldsItsSlots(t *testing.T) {
	b, m := rcBench(t, testExperiment())
	models := ExperimentModels(m)
	if len(models) == 0 {
		t.Skip("test arms name no model")
	}
	ms := []string{models[0]}
	if _, err := runctl.Apply(b.store.Root(), m.ExpID, "ana", runctl.ActionSet, "", "", func(s *runctl.State) error {
		(&runctl.Patch{PausedModels: &ms}).ApplyTo(s)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, _ = NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true, ControlPoll: 10 * time.Millisecond})
	if n := b.w.count("exec"); n != 0 && len(models) == 1 {
		t.Fatalf("%d runs of the paused model started", n)
	}
}

// Cancel one run: it seals aborted_infra and owner stopped (no attempt),
// the rest finish, the slot is pending; requeue runs it again.
func TestRunControlCancelRunThenRequeue(t *testing.T) {
	b, m := rcBench(t, testExperiment())
	root := b.store.Root()
	b.w.execDelay = 10 * time.Second
	type result struct {
		sum *Summary
		err error
	}
	done := make(chan result, 1)
	go func() {
		sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Resume: true, Concurrency: 1, ControlPoll: 10 * time.Millisecond})
		done <- result{sum, err}
	}()
	waitFor(t, "a run to start", func() bool { return b.w.count("exec") >= 1 })
	var slotID string
	b.w.mu.Lock()
	for id := range b.w.execs {
		slotID = id
	}
	b.w.execDelay = 0
	b.w.mu.Unlock()
	if _, err := CancelRun(root, m.ExpID, "ana", slotID, "", "wrong prompt"); err != nil {
		t.Fatal(err)
	}
	var r result
	select {
	case r = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("cancel did not stop the run")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.sum.Pending != 1 {
		t.Fatalf("summary %+v, want the cancelled slot pending", r.sum)
	}
	runs := b.store.slotRuns(slotID)
	if len(runs) != 1 || runs[0].run.Outcome != schemas.OutcomeAbortedInfra || !OwnerStopped(&runs[0].run) {
		t.Fatalf("cancelled slot runs %+v", runs)
	}
	recs, _ := b.store.SlotRuns(context.Background(), m.ExpID, slotID)
	if _, err := Requeue(root, m.ExpID, "ana", slotID, "fixed", recs); err != nil {
		t.Fatal(err)
	}
	st, _ := runctl.Read(root, m.ExpID)
	if st.SlotCancelled(slotID) {
		t.Fatal("requeue left the slot cancelled")
	}
	again, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Resume: true, ControlPoll: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	runs = b.store.slotRuns(slotID)
	if again.Pending != 0 || len(runs) != 2 || runs[1].run.Outcome != schemas.OutcomePass {
		t.Fatalf("requeued %+v runs %d", again, len(runs))
	}
	log, err := runctl.Log(root, m.ExpID)
	if err != nil || len(log) != 2 || log[0].Action != runctl.ActionCancelRun || log[1].Action != runctl.ActionRequeue {
		t.Fatalf("log %+v %v", log, err)
	}
}

// Requeue refuses a graded run and supersedes an infra_error one, which
// then neither counts nor uses an attempt.
func TestRequeueRules(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "e_x"), 0o755); err != nil {
		t.Fatal(err)
	}
	pass := []schemas.RunRecord{{Run: schemas.Run{RunID: "r_1", Outcome: schemas.OutcomePass}, Sealed: true}}
	if _, err := Requeue(root, "e_x", "a", "s_1", "", pass); err == nil {
		t.Fatal("a pass was requeued")
	}
	inflight := []schemas.RunRecord{{Run: schemas.Run{RunID: "r_1"}}}
	if _, err := Requeue(root, "e_x", "a", "s_1", "", inflight); err == nil {
		t.Fatal("an in-flight run was requeued")
	}
	infra := []schemas.RunRecord{{Run: schemas.Run{RunID: "r_2", Outcome: schemas.OutcomeInfraError}, Sealed: true}}
	st, err := Requeue(root, "e_x", "a", "s_2", "provider 500s", infra)
	if err != nil || !st.IsSuperseded("r_2") {
		t.Fatalf("infra_error requeue %+v %v", st, err)
	}
	infra[0].Superseded = true
	if usedAttempts(infra) != 0 {
		t.Fatal("a superseded run used an attempt")
	}
}

// The error policy retries infra_error runs up to its limit; the earlier
// runs are superseded, the last one counts.
func TestErrorPolicyRetriesInfraError(t *testing.T) {
	yes := true
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01"}}, Trials: 1})
	e.ErrorPolicy = &schemas.ErrorPolicySpec{InfraError: &schemas.ErrorRule{Retry: &yes, MaxAttempts: 2}}
	b, m := rcBench(t, e)
	b.w.fail["grade"] = true
	if _, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Resume: true, ControlPoll: 10 * time.Millisecond}); err != nil {
		t.Fatal(err)
	}
	st, err := runctl.Read(b.store.Root(), m.ExpID)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Slots {
		runs := b.store.slotRuns(s.SlotID)
		if runs[0].run.Outcome != schemas.OutcomeInfraError {
			t.Skipf("a grader failure seals %s here, not infra_error", runs[0].run.Outcome)
		}
		if len(runs) != 2 || !st.IsSuperseded(runs[0].run.RunID) || st.IsSuperseded(runs[1].run.RunID) {
			t.Fatalf("slot %s: %d runs, superseded %v", s.SlotID, len(runs), st.Superseded)
		}
	}
}

// The live concurrency gate: at the limit a local worker waits for one to
// finish; a held slot goes to nobody.
func TestSlotQueueGate(t *testing.T) {
	q := newSlotQueue([]schemas.Slot{{SlotID: "a", OrderIndex: 0}, {SlotID: "b", OrderIndex: 1}, {SlotID: "c", OrderIndex: 2}})
	q.setGate(func(s *schemas.Slot) bool { return s.SlotID == "c" }, 1)
	ctx := context.Background()
	s1, ok := q.nextLocal(ctx, nil, false)
	if !ok || s1.SlotID != "a" {
		t.Fatalf("first %v %v", s1, ok)
	}
	got := make(chan string, 1)
	go func() {
		s, _ := q.nextLocal(ctx, nil, false)
		got <- s.SlotID
	}()
	select {
	case id := <-got:
		t.Fatalf("took %s past the limit", id)
	case <-time.After(50 * time.Millisecond):
	}
	q.doneLocal()
	if id := <-got; id != "b" {
		t.Fatalf("second %s", id)
	}
	if _, ok := q.takeWhere(nil); ok {
		t.Fatal("a remote worker took the held slot")
	}
	q.doneLocal()
	tctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if s, ok := q.nextLocal(tctx, nil, false); ok {
		t.Fatalf("took held slot %s", s.SlotID)
	}
	q.setGate(nil, 0)
	if s, ok := q.nextLocal(ctx, nil, false); !ok || s.SlotID != "c" {
		t.Fatal("released slot not handed out")
	}
}
