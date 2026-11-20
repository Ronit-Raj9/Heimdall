package engine

import (
	"context"
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
