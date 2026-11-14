package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// seedRun writes an unsealed run for a slot as a crashed process would have
// left it: run.json plus the transitions it got through.
func seedRun(t *testing.T, b *bench, slot *schemas.Slot, runID string, agentStarted bool, stages ...schemas.Stage) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	run := &schemas.Run{RunID: runID, SlotID: slot.SlotID, PairID: slot.PairID, ExpID: slot.ExpID, Attempt: 1,
		Stage: schemas.StageLeased, Outcome: schemas.OutcomeUnknown, Timing: schemas.RunTiming{Started: now}}
	if agentStarted {
		run.Timing.AgentStarted = &now
	}
	if _, err := b.store.NewRun(ctx, slot, run); err != nil {
		t.Fatal(err)
	}
	for i, s := range stages {
		if err := b.store.AppendTransition(ctx, run, schemas.Transition{Stage: s, Status: schemas.TransitionStarted, At: now}); err != nil {
			t.Fatal(err)
		}
		if i < len(stages)-1 {
			if err := b.store.AppendTransition(ctx, run, schemas.Transition{Stage: s, Status: schemas.TransitionCompleted, At: now}); err != nil {
				t.Fatal(err)
			}
			run.Stage = s
		}
	}
	raw, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.store.WriteRunFile(ctx, run, "run.json", raw); err != nil {
		t.Fatal(err)
	}
}

func drain(ch chan Event) map[string]int {
	out := map[string]int{}
	for {
		select {
		case ev := <-ch:
			out[ev.Kind]++
		default:
			return out
		}
	}
}

func TestSchedulerResume(t *testing.T) {
	b := newBench(t)
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if err := b.store.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	s := m.Slots
	if _, err := NewPipeline(b.reg, b.root, nil).RunSlot(ctx, SlotInput{Manifest: m, Suite: b.suite, Slot: &s[0], Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	seedRun(t, b, &s[1], "r_started", true, schemas.StagePrepare, schemas.StageArm, schemas.StageRun)
	seedRun(t, b, &s[2], "r_early", false, schemas.StagePrepare)
	events := make(chan Event, 256)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if got := []int{b.w.execs[s[0].SlotID], b.w.execs[s[1].SlotID], b.w.execs[s[2].SlotID], b.w.execs[s[3].SlotID]}; got[0] != 1 || got[1] != 0 || got[2] != 1 || got[3] != 1 {
		t.Fatalf("agent runs per slot %v, want [1 0 1 1]", got)
	}
	started := b.store.slotRuns(s[1].SlotID)
	if len(started) != 1 || !started[0].sealed || started[0].run.Outcome != schemas.OutcomeInfraError ||
		!hasTransition(started[0], schemas.StageRun, schemas.TransitionFailed) {
		t.Fatalf("started agent: %d runs, %+v %v", len(started), started[0].run.Outcome, transitionsOf(started[0]))
	}
	early := b.store.slotRuns(s[2].SlotID)
	if len(early) != 2 || early[0].run.Outcome != schemas.OutcomeAbortedInfra || !early[0].sealed ||
		early[1].run.Outcome != schemas.OutcomePass || early[1].run.Attempt != 2 {
		t.Fatalf("early abort: %d runs", len(early))
	}
	if sum.Skipped != 1 || sum.Recovered != 2 || sum.Sealed != 4 || sum.Errors != 0 {
		t.Fatalf("summary %+v", sum)
	}
	ev := drain(events)
	if ev[EventSlotSkipped] != 1 || ev[EventRunRecovered] != 2 || ev[EventDone] != 1 || ev[EventStage] == 0 {
		t.Fatalf("events %v", ev)
	}
	again, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true})
	if err != nil {
		t.Fatal(err)
	}
	if again.Skipped != 4 || again.Sealed != 0 || b.w.count("exec") != 3 {
		t.Fatalf("second resume reran something: %+v, exec %d", again, b.w.count("exec"))
	}
}

func TestSchedulerSpendCap(t *testing.T) {
	b := newBench(t)
	b.w.cost = 0.6
	e := testExperiment()
	e.Budget.TotalUSD = 1.0
	m := b.plan(t, e)
	ctx := context.Background()
	events := make(chan Event, 256)
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Concurrency: 1, Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != 2 || sum.Pending != 2 || !sum.BudgetStopped || sum.SpentUSD < 1.19 || sum.SpentUSD > 1.21 {
		t.Fatalf("summary %+v", sum)
	}
	if b.w.count("exec") != 2 {
		t.Fatalf("leased past the cap: %d agent runs", b.w.count("exec"))
	}
	if drain(events)[EventBudgetStop] != 1 {
		t.Fatal("no budget_stop event")
	}
	resumed, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if resumed.SpentUSD < 1.19 || resumed.Skipped != 2 || resumed.Pending != 2 || b.w.count("exec") != 2 {
		t.Fatalf("resume ignored the spend in metrics.json: %+v", resumed)
	}
}

func TestSchedulerRetries(t *testing.T) {
	b := newBench(t)
	b.w.fail["create"] = true
	m := b.plan(t, testExperiment())
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != 4*maxAttempts || sum.Retried != 4*(maxAttempts-1) || b.w.count("exec") != 0 {
		t.Fatalf("summary %+v", sum)
	}
	for _, s := range m.Slots {
		runs := b.store.slotRuns(s.SlotID)
		if len(runs) != maxAttempts {
			t.Fatalf("slot %s has %d runs", s.SlotID, len(runs))
		}
		for i, r := range runs {
			if !r.sealed || r.run.Outcome != schemas.OutcomeAbortedInfra || r.run.Attempt != i+1 {
				t.Fatalf("run %d: %+v", i, r.run)
			}
		}
	}
	again, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Resume: true})
	if err != nil || again.Skipped != 4 || again.Sealed != 0 {
		t.Fatalf("exhausted slots retried again: %+v %v", again, err)
	}
}

// TestSchedulerRetriesUnreachableProvider runs a slot whose first attempt
// never reached the provider: it is sealed as aborted_infra although its agent
// started, and the slot gets a new run that is the counted one.
func TestSchedulerRetriesUnreachableProvider(t *testing.T) {
	b := newBench(t)
	b.w.ledgers = [][]schemas.LedgerEntry{{ledgerLine(1, false), ledgerLine(2, false)}}
	m := b.plan(t, testExperiment())
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != len(m.Slots)+1 || sum.Retried != 1 {
		t.Fatalf("summary %+v", sum)
	}
	retried := 0
	for _, s := range m.Slots {
		runs := b.store.slotRuns(s.SlotID)
		if len(runs) == 1 {
			continue
		}
		retried++
		first, second := runs[0].run, runs[1].run
		if first.Outcome != schemas.OutcomeAbortedInfra || first.Timing.AgentStarted == nil ||
			second.Outcome != schemas.OutcomePass || second.Attempt != 2 {
			t.Fatalf("slot %s runs %+v then %+v", s.SlotID, first, second)
		}
	}
	if retried != 1 {
		t.Fatalf("%d slots retried, want 1", retried)
	}
}

func TestSchedulerRefuses(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(b *bench, m *schemas.Manifest) RunOptions
		want   string
	}{
		{"edited manifest", func(_ *bench, m *schemas.Manifest) RunOptions {
			m.Slots[0].Trial = 9
			return RunOptions{}
		}, "hash mismatch"},
		{"experiment exists", func(b *bench, m *schemas.Manifest) RunOptions {
			if err := b.store.CreateExperiment(context.Background(), m); err != nil {
				panic(err)
			}
			return RunOptions{}
		}, "already exists"},
		{"resume unknown experiment", func(*bench, *schemas.Manifest) RunOptions { return RunOptions{Resume: true} }, "not found"},
		{"suite lock changed", func(b *bench, _ *schemas.Manifest) RunOptions {
			b.suite.LockHash = "lock-new"
			return RunOptions{}
		}, "lock hash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := newBench(t)
			m := b.plan(t, testExperiment())
			opts := tt.mutate(b, m)
			_, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, opts)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSchedulerLeaseHeld(t *testing.T) {
	b := newBench(t)
	m := b.plan(t, testExperiment())
	if ok, err := b.store.Lease(context.Background(), m.Slots[0].SlotID, "other", time.Minute); !ok || err != nil {
		t.Fatal("seed lease")
	}
	sum, err := NewScheduler(b.reg, b.root, nil, nil).Run(context.Background(), m, RunOptions{Owner: "me"})
	if err != nil {
		t.Fatal(err)
	}
	if sum.LeaseHeld != 1 || sum.Sealed != 3 || b.w.execs[m.Slots[0].SlotID] != 0 {
		t.Fatalf("summary %+v", sum)
	}
}

func TestReadCost(t *testing.T) {
	dir := t.TempDir()
	if c, err := readCost(dir); c != 0 || err != nil {
		t.Fatalf("missing metrics: %v %v", c, err)
	}
	b := newBench(t)
	b.w.cost = 0.25
	m := b.plan(t, testExperiment())
	res, err := NewPipeline(b.reg, b.root, nil).RunSlot(context.Background(), SlotInput{Manifest: m, Suite: b.suite, Slot: &m.Slots[0], Attempt: 1})
	if err != nil {
		t.Fatal(err)
	}
	if c, err := readCost(res.Dir); c != 0.25 || err != nil {
		t.Fatalf("cost %v err %v", c, err)
	}
}
