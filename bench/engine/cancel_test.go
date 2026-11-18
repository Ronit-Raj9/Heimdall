package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Jobs area: a cancelled scheduler leases nothing more, its in-flight runs
// seal as aborted_infra (never counted), and a resume runs those slots again.
func TestSchedulerCancelSealsAbortedThenResumes(t *testing.T) {
	b := newBench(t)
	b.w.execDelay = 10 * time.Second // a run ends only when its context does
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if err := b.store.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	parent, cancel := context.WithCancelCause(ctx)
	rctx, stop := CancelContext(parent)
	defer stop()
	go func() {
		for b.w.count("exec") < 1 {
			time.Sleep(2 * time.Millisecond)
		}
		cancel(jobs.ErrCancelled)
	}()
	start := time.Now()
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(rctx, m, RunOptions{Resume: true, Concurrency: 1})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run error %v", err)
	}
	if !errors.Is(context.Cause(rctx), ErrRunCancelled) || !errors.Is(context.Cause(rctx), jobs.ErrCancelled) {
		t.Fatalf("cause %v", context.Cause(rctx))
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("the in-flight run was not stopped")
	}
	first := m.Slots[0]
	for _, s := range m.Slots {
		if s.OrderIndex < first.OrderIndex {
			first = s
		}
	}
	runs := b.store.slotRuns(first.SlotID)
	if len(runs) != 1 || !runs[0].sealed || runs[0].run.Outcome != schemas.OutcomeAbortedInfra {
		t.Fatalf("cancelled slot runs %+v", runs)
	}
	found := false
	for _, f := range runs[0].run.Failures {
		found = found || strings.Contains(f.Error, "experiment run cancelled") && strings.Contains(f.Error, "a resume runs the slot again")
	}
	if !found {
		t.Fatalf("no cancel failure in %+v", runs[0].run.Failures)
	}
	if sum.Pending != len(m.Slots)-1 || sum.Sealed != 1 || b.w.count("exec") != 1 {
		t.Fatalf("summary %+v exec %d", sum, b.w.count("exec"))
	}

	b.w.mu.Lock()
	b.w.execDelay = 0
	b.w.mu.Unlock()
	again, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if again.Sealed != len(m.Slots) || again.Pending != 0 {
		t.Fatalf("resume %+v", again)
	}
	runs = b.store.slotRuns(first.SlotID)
	if len(runs) != 2 || runs[1].run.Outcome != schemas.OutcomePass || runs[1].run.Attempt != 2 {
		t.Fatalf("resumed slot runs %d, last %+v", len(runs), runs[len(runs)-1].run)
	}
}

// experimentRunKind is experiment.run as serve registers it, reduced to its
// engine part: the scheduler in resume mode under the job's context.
func experimentRunKind(b *bench, m *schemas.Manifest, attempts *atomic.Int32) jobs.Handler {
	return func(ctx context.Context, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
		attempts.Add(1)
		rctx, stop := CancelContext(ctx)
		defer stop()
		log.Printf("attempt %d resume %v", j.Attempt, j.Resume())
		sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(rctx, m, RunOptions{Resume: true, Concurrency: 1})
		progress(sum)
		if cause := context.Cause(ctx); cause != nil {
			return sum, cause
		}
		return sum, err
	}
}

// Jobs area: experiment.run through the job runner with the fake engine. A
// cancel mid-job ends the job cancelled with its in-flight run aborted; a
// runner shut down mid-job hands it back and a second runner on the same
// database (the restarted server) resumes it to the end.
func TestExperimentRunJobCancelAndRestartResume(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	opts := jobs.Options{Workers: 2, LeaseFor: time.Second, HeartbeatEvery: 50 * time.Millisecond, PollEvery: 20 * time.Millisecond,
		ProgressEvery: 20 * time.Millisecond, ShutdownGrace: 10 * time.Second}
	waitStatus := func(r *jobs.Runner, id string, want jobs.Status) *jobs.Job {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			j, err := r.Get(ctx, "local", id)
			if err != nil {
				t.Fatal(err)
			}
			if j.Status == want {
				return j
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("job %s never reached %s", id, want)
		return nil
	}
	params, _ := json.Marshal(map[string]string{"exp_id": "x"})

	// Cancel mid-job.
	b := newBench(t)
	b.w.execDelay = 10 * time.Second
	m := b.plan(t, testExperiment())
	if err := b.store.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	var attempts atomic.Int32
	opts.Owner = "one"
	r := jobs.New(d, opts)
	r.Register("experiment.run", experimentRunKind(b, m, &attempts))
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := r.Create(ctx, jobs.NewJob{OrgID: "local", Kind: "experiment.run", Params: params})
	if err != nil {
		t.Fatal(err)
	}
	for b.w.count("exec") < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	if _, err := r.Cancel(ctx, "local", j.ID); err != nil {
		t.Fatal(err)
	}
	done := waitStatus(r, j.ID, jobs.StatusCancelled)
	if !strings.Contains(done.Error, "job cancelled") || done.Result == nil {
		t.Fatalf("cancelled job %+v", done)
	}
	aborted := 0
	for _, s := range m.Slots {
		for _, run := range b.store.slotRuns(s.SlotID) {
			if run.run.Outcome == schemas.OutcomeAbortedInfra && run.sealed {
				aborted++
			}
		}
	}
	if aborted != 1 || b.w.count("exec") != 1 {
		t.Fatalf("aborted runs %d exec %d", aborted, b.w.count("exec"))
	}
	r.Stop(ctx)

	// Shutdown mid-job, then a second runner resumes.
	b2 := newBench(t)
	b2.w.execDelay = 10 * time.Second
	m2 := b2.plan(t, testExperiment())
	if err := b2.store.CreateExperiment(ctx, m2); err != nil {
		t.Fatal(err)
	}
	var attempts2 atomic.Int32
	opts.Owner = "first-server"
	first := jobs.New(d, opts)
	first.Register("experiment.run", experimentRunKind(b2, m2, &attempts2))
	if err := first.Start(ctx); err != nil {
		t.Fatal(err)
	}
	j2, err := first.Create(ctx, jobs.NewJob{OrgID: "local", Kind: "experiment.run", Params: params})
	if err != nil {
		t.Fatal(err)
	}
	for b2.w.count("exec") < 1 {
		time.Sleep(5 * time.Millisecond)
	}
	first.Stop(ctx)
	if q := waitStatus(first, j2.ID, jobs.StatusQueued); q.Attempt != 1 {
		t.Fatalf("handed back job %+v", q)
	}
	b2.w.mu.Lock()
	b2.w.execDelay = 0
	b2.w.mu.Unlock()
	opts.Owner = "second-server"
	second := jobs.New(d, opts)
	second.Register("experiment.run", experimentRunKind(b2, m2, &attempts2))
	if err := second.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer second.Stop(ctx)
	ok := waitStatus(second, j2.ID, jobs.StatusSucceeded)
	if ok.Attempt != 2 || attempts2.Load() != 2 {
		t.Fatalf("resumed job %+v, handler calls %d", ok, attempts2.Load())
	}
	var sum Summary
	if err := json.Unmarshal(ok.Result, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != len(m2.Slots) || sum.Pending != 0 {
		t.Fatalf("resumed summary %+v", sum)
	}
	for _, s := range m2.Slots {
		runs := b2.store.slotRuns(s.SlotID)
		last := runs[len(runs)-1]
		if !last.sealed || last.run.Outcome != schemas.OutcomePass {
			t.Fatalf("slot %s ended %+v", s.SlotID, last.run)
		}
	}
	logs, err := second.Logs(ctx, "local", j2.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range logs.Lines {
		lines = append(lines, l.Line)
	}
	got := strings.Join(lines, "\n")
	for _, want := range []string{"attempt 1 resume false", "runner shutting down", "resuming: attempt 2", "attempt 2 resume true"} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs %q lack %q", got, want)
		}
	}
}

// Jobs area: cancelling (or stopping the server) more often than a slot has
// attempts never exhausts it: owner-stopped runs use no attempt.
func TestOwnerStoppedRunsUseNoAttempt(t *testing.T) {
	b := newBench(t)
	b.w.execDelay = 10 * time.Second
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if err := b.store.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= maxAttempts+1; round++ {
		parent, cancel := context.WithCancelCause(ctx)
		rctx, stop := CancelContext(parent)
		go func(want int) {
			for b.w.count("exec") < want {
				time.Sleep(2 * time.Millisecond)
			}
			cancel(jobs.ErrShutdown)
		}(round)
		if _, err := NewScheduler(b.reg, b.root, nil, nil).Run(rctx, m, RunOptions{Resume: true, Concurrency: 1}); !errors.Is(err, context.Canceled) {
			t.Fatalf("round %d: %v", round, err)
		}
		stop()
	}
	b.w.mu.Lock()
	b.w.execDelay = 0
	b.w.mu.Unlock()
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Skipped != 0 || sum.Sealed != len(m.Slots) {
		t.Fatalf("resume after %d stops: %+v", maxAttempts+1, sum)
	}
	for _, s := range m.Slots {
		runs := b.store.slotRuns(s.SlotID)
		last := runs[len(runs)-1]
		if last.run.Outcome != schemas.OutcomePass {
			t.Fatalf("slot %s: %d runs, last %s", s.SlotID, len(runs), last.run.Outcome)
		}
		for _, r := range runs[:len(runs)-1] {
			if !OwnerStopped(&r.run) {
				t.Fatalf("slot %s: an earlier run is not owner-stopped: %+v", s.SlotID, r.run.Failures)
			}
		}
	}
}
