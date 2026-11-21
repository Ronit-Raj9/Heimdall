package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestSchedulerResumeFinishesInterruptedSeal is 2-A-01: a crash after
// seal.json is written but before its seals.log line leaves a run that looks
// sealed. Resume must finish the chain append so the run verifies, instead of
// skipping the slot with a seal that never verifies.
func TestSchedulerResumeFinishesInterruptedSeal(t *testing.T) {
	side := newRemoteSide(t)
	b := side.b
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if _, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(side.st.Root(), m.ExpID, "seals.log")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	last := strings.Fields(lines[len(lines)-1])[0]
	// The crash: the last seal's line is missing and its folder never went read-only.
	if err := os.WriteFile(logPath, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var dir string
	for _, slot := range m.Slots {
		recs, _ := side.st.SlotRuns(ctx, m.ExpID, slot.SlotID)
		for _, rec := range recs {
			if rec.Run.RunID == last {
				dir = rec.Dir
			}
		}
	}
	if dir == "" {
		t.Fatalf("run %s not found", last)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := side.st.Verify(ctx, m.ExpID, last); err == nil {
		t.Fatal("crash simulation: run still verifies")
	}
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := side.st.Verify(ctx, m.ExpID, last); err != nil {
		t.Fatalf("after resume the interrupted seal still fails: %v (summary %+v)", err, sum)
	}
	if sum.Recovered != 1 || sum.Errors != 0 || sum.Skipped != len(m.Slots) {
		t.Fatalf("summary %+v", sum)
	}
}

// TestPipelineCancelDuringCleanupStillSeals is 2-A-05: an owner cancel that
// arrives while cleanup runs must still let cleanup and the abort seal
// finish, so the run is an owner-stopped aborted_infra, not an unsealed run
// that resume would count as infra_error.
func TestPipelineCancelDuringCleanupStillSeals(t *testing.T) {
	side := newRemoteSide(t)
	b := side.b
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if err := side.st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	parent, cancelParent := context.WithCancel(ctx)
	rctx, stop := CancelContext(parent)
	defer stop()
	b.w.onHit = map[string]func(){"destroy": func() {
		cancelParent()
		<-rctx.Done()
	}}
	res, err := NewPipeline(b.reg, b.root, nil).RunSlot(rctx, SlotInput{Manifest: m, Suite: b.suite, Slot: &m.Slots[0], Attempt: 1})
	if err != nil {
		t.Fatalf("run slot: %v", err)
	}
	if res.Seal == nil || res.Run.Outcome != "aborted_infra" || !OwnerStopped(res.Run) {
		t.Fatalf("sealed=%v outcome=%s owner_stopped=%v", res.Seal != nil, res.Run.Outcome, OwnerStopped(res.Run))
	}
	if err := side.st.Verify(ctx, m.ExpID, res.Run.RunID); err != nil {
		t.Fatal(err)
	}
}

// verifyGate is a store whose Verify waits for release, so a test can hold a
// remote Complete call right after it sealed the uploaded run.
type verifyGate struct {
	schemas.Store
	entered chan struct{}
	release chan struct{}
}

func (g *verifyGate) Verify(ctx context.Context, expID, runID string) error {
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.release
	return g.Store.Verify(ctx, expID, runID)
}

// TestRemoteCompleteDuringDetachSettlesOnce is 2-A-07: detach while a
// Complete call is settling its lease must leave that lease to the call and
// wait for it, so the lease is finalized once and the in-flight count never
// goes negative.
func TestRemoteCompleteDuringDetachSettlesOnce(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	gate := &verifyGate{Store: rw.c.store, entered: make(chan struct{}, 1), release: make(chan struct{})}
	rw.c.store = gate
	done := rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	worked := make(chan *schemas.WorkerResult, 1)
	go func() { worked <- w.Work(context.Background(), l) }()
	<-gate.entered
	detached := make(chan *Summary, 1)
	go func() { detached <- done() }()
	select {
	case <-detached:
		t.Fatal("detach returned while a completion was still settling its lease")
	case <-time.After(200 * time.Millisecond):
	}
	close(gate.release)
	sum := <-detached
	res := <-worked
	if res == nil || !res.Accepted {
		t.Fatalf("completion: %+v", res)
	}
	rw.r.queue.mu.Lock()
	out := rw.r.queue.remote
	rw.r.queue.mu.Unlock()
	if out != 0 || sum.Remote != 1 || sum.Pending != 0 {
		t.Fatalf("remote in flight %d, summary %+v", out, sum)
	}
}

// TestPipelineNotLaunchedIsRetried is 2-A-12: an Exec failure the
// environment marks as certain pre-launch (schemas.ErrNotLaunched) is an
// abort before agent start, which the scheduler retries, not a counted
// infra_error. An unmarked Exec failure stays infra_error.
func TestPipelineNotLaunchedIsRetried(t *testing.T) {
	for _, notLaunched := range []bool{true, false} {
		b := newBench(t)
		m := b.plan(t, testExperiment())
		b.w.fail["exec"] = true
		b.w.notLaunched = notLaunched
		res, err := NewPipeline(b.reg, b.root, nil).RunSlot(context.Background(), SlotInput{Manifest: m, Suite: b.suite, Slot: &m.Slots[0], Attempt: 1})
		if err != nil {
			t.Fatal(err)
		}
		want := schemas.OutcomeInfraError
		if notLaunched {
			want = schemas.OutcomeAbortedInfra
		}
		if res.Run.Outcome != want || (notLaunched && res.Run.Timing.AgentStarted != nil) {
			t.Fatalf("not_launched=%v: outcome %s, agent started %v", notLaunched, res.Run.Outcome, res.Run.Timing.AgentStarted)
		}
	}
}

// TestSchedulerStrandedSealIsNotSkipped is QC2-06-04: a pending seal whose
// log line is missing while a successor already moved the chain tail cannot
// be finished. Resume must report it as a slot error and neither count nor
// skip that slot as done.
func TestSchedulerStrandedSealIsNotSkipped(t *testing.T) {
	side := newRemoteSide(t)
	b := side.b
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if _, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{}); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(side.st.Root(), m.ExpID, "seals.log")
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		t.Fatalf("need two seals, have %d", len(lines))
	}
	stranded := strings.Fields(lines[len(lines)-2])[0]
	kept := append(append([]string{}, lines[:len(lines)-2]...), lines[len(lines)-1])
	if err := os.WriteFile(logPath, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, slot := range m.Slots {
		recs, _ := side.st.SlotRuns(ctx, m.ExpID, slot.SlotID)
		for _, rec := range recs {
			if rec.Run.RunID == stranded {
				if err := os.Chmod(rec.Dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	sum, _ := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true})
	if sum == nil || sum.Errors != 1 || sum.Skipped != len(m.Slots)-1 {
		t.Fatalf("summary %+v, want 1 error and %d skipped", sum, len(m.Slots)-1)
	}
}

// transitionHook is a store that calls hook before every transition append
// and fails the append when hook returns an error.
type transitionHook struct {
	schemas.Store
	hook func(schemas.Transition) error
}

func (h *transitionHook) AppendTransition(ctx context.Context, run *schemas.Run, tr schemas.Transition) error {
	if err := h.hook(tr); err != nil {
		return err
	}
	return h.Store.AppendTransition(ctx, run, tr)
}

// TestPipelineSlowCleanupStillSeals is QC2-06-01: a cleanup that uses its
// whole deadline must not leave the seal without time of its own.
func TestPipelineSlowCleanupStillSeals(t *testing.T) {
	side := newRemoteSide(t)
	b := side.b
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if err := side.st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	old := cleanupTimeout
	cleanupTimeout = 300 * time.Millisecond
	defer func() { cleanupTimeout = old }()
	b.w.onHit = map[string]func(){"destroy": func() { time.Sleep(400 * time.Millisecond) }}
	res, err := NewPipeline(b.reg, b.root, nil).RunSlot(ctx, SlotInput{Manifest: m, Suite: b.suite, Slot: &m.Slots[0], Attempt: 1})
	if err != nil || res.Seal == nil {
		t.Fatalf("run slot: sealed=%v err=%v", res != nil && res.Seal != nil, err)
	}
	if err := side.st.Verify(ctx, m.ExpID, res.Run.RunID); err != nil {
		t.Fatal(err)
	}
}

// TestPipelineCancelAtSealCommitAborts is QC2-06-02: an owner cancel that
// arrives after cleanup, while the final artifacts are written, still seals
// the run as owner-stopped aborted_infra.
func TestPipelineCancelAtSealCommitAborts(t *testing.T) {
	side := newRemoteSide(t)
	b := side.b
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if err := side.st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	parent, cancelParent := context.WithCancel(ctx)
	rctx, stop := CancelContext(parent)
	defer stop()
	b.reg.Store = &transitionHook{Store: side.st, hook: func(tr schemas.Transition) error {
		if tr.Stage == schemas.StageSeal && tr.Status == schemas.TransitionStarted {
			cancelParent()
			<-rctx.Done()
		}
		return nil
	}}
	res, err := NewPipeline(b.reg, b.root, nil).RunSlot(rctx, SlotInput{Manifest: m, Suite: b.suite, Slot: &m.Slots[0], Attempt: 1})
	if err != nil {
		t.Fatalf("run slot: %v", err)
	}
	if res.Seal == nil || res.Run.Outcome != schemas.OutcomeAbortedInfra || !OwnerStopped(res.Run) {
		t.Fatalf("sealed=%v outcome=%s owner_stopped=%v", res.Seal != nil, res.Run.Outcome, OwnerStopped(res.Run))
	}
	if err := side.st.Verify(ctx, m.ExpID, res.Run.RunID); err != nil {
		t.Fatal(err)
	}
}

// TestPipelineCleanupRunsWhenItsLogFails is QC2-06-09: a failed append of
// cleanup's started transition must not skip teardown.
func TestPipelineCleanupRunsWhenItsLogFails(t *testing.T) {
	side := newRemoteSide(t)
	b := side.b
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if err := side.st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	b.reg.Store = &transitionHook{Store: side.st, hook: func(tr schemas.Transition) error {
		if tr.Stage == schemas.StageCleanup && tr.Status == schemas.TransitionStarted {
			return errors.New("injected transition write failure")
		}
		return nil
	}}
	res, _ := NewPipeline(b.reg, b.root, nil).RunSlot(ctx, SlotInput{Manifest: m, Suite: b.suite, Slot: &m.Slots[0], Attempt: 1})
	if n := b.w.count("destroy"); n != 1 {
		t.Fatalf("destroy calls = %d, want 1", n)
	}
	if res == nil || res.Run.Outcome != schemas.OutcomeInfraError {
		t.Fatalf("result %+v", res)
	}
}

// sealCrash is a store whose Seal fails, standing in for a process that
// stopped after the final artifacts were written but before Store.Seal.
type sealCrash struct{ schemas.Store }

func (sealCrash) Seal(context.Context, *schemas.Run) (*schemas.SealRecord, error) {
	return nil, errors.New("process stopped")
}

// TestResumeKeepsArtifactsWrittenBeforeSeal is QC2-06-03: recovery of a run
// whose final artifacts were all written must seal them as written, not
// replace its grade and gates with empty ones.
func TestResumeKeepsArtifactsWrittenBeforeSeal(t *testing.T) {
	side := newRemoteSide(t)
	b := side.b
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if err := side.st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	b.reg.Store = sealCrash{side.st}
	res, err := NewPipeline(b.reg, b.root, nil).RunSlot(ctx, SlotInput{Manifest: m, Suite: b.suite, Slot: &m.Slots[0], Attempt: 1})
	if err == nil {
		t.Fatal("crash simulation sealed")
	}
	before, err := os.ReadFile(filepath.Join(res.Dir, "grade.json"))
	if err != nil || strings.TrimSpace(string(before)) == "null" {
		t.Fatalf("grade before resume: %q %v", before, err)
	}
	b.reg.Store = side.st
	recs, err := side.st.SlotRuns(ctx, m.ExpID, m.Slots[0].SlotID)
	if err != nil || len(recs) != 1 {
		t.Fatalf("runs %d %v", len(recs), err)
	}
	out, err := NewPipeline(b.reg, b.root, nil).SealInterrupted(ctx, SlotInput{Manifest: m, Suite: b.suite, Slot: &m.Slots[0], Attempt: 1}, recs[0])
	if err != nil {
		t.Fatal(err)
	}
	if out.Run.Outcome != schemas.OutcomePass {
		t.Fatalf("outcome %s, want the written pass", out.Run.Outcome)
	}
	after, err := os.ReadFile(filepath.Join(res.Dir, "grade.json"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("grade.json changed on resume: %q", after)
	}
	if err := side.st.Verify(ctx, m.ExpID, out.Run.RunID); err != nil {
		t.Fatal(err)
	}
}

// TestLeaseLostLeavesSlotUnsettled is QC2-06-06: a slot whose lease this
// scheduler lost mid-run is still the new holder's to finish; the adaptive
// replay must not read it as settled.
func TestLeaseLostLeavesSlotUnsettled(t *testing.T) {
	r := &schedRun{s: &Scheduler{logger: schemas.NoopLogger{}}, m: &schemas.Manifest{ExpID: "e1"}}
	sctx, cancel := context.WithCancelCause(context.Background())
	cancel(ErrLeaseLost)
	r.leaseLost(sctx, &schemas.Slot{SlotID: "s1"})
	if !r.unsettled["s1"] || r.sum.LeaseLost != 1 {
		t.Fatalf("unsettled=%v lease_lost=%d", r.unsettled, r.sum.LeaseLost)
	}
}
