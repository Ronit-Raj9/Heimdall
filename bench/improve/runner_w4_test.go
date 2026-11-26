package improve_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
)

// w4Spec is a spec for the wave 4 runner tests: gate alpha 0.10, three
// rounds, one gate trial per task.
func w4Spec(mut func(s *improve.Spec)) *improve.Spec {
	s := &improve.Spec{Name: "w4-loop", Agent: "agents/scripted-improver.yaml", Suite: "scripted@1.0.0", Seed: 7,
		Editable: improve.Editable{Files: []string{improvetest.NotesPath}},
		Commit:   improve.CommitSpec{Alpha: 0.10, Trials: 2}, Sealed: improve.PhaseSpec{Trials: 2}}
	if mut != nil {
		mut(s)
	}
	s.Defaults()
	return s
}

func TestRoundKCandidatesOneAuditLook(t *testing.T) {
	x := &improvetest.Exec{Base: 1, NTasks: 80, Slots: map[int]string{1: improvetest.Worse, 2: improvetest.Cheat, 3: improvetest.Honest}}
	r := newRunner(t, x)
	rec := createRun(t, r, w4Spec(func(s *improve.Spec) { s.Rounds = 1; s.Improver.Candidates = 3; s.Improver.Keep = 3 }))
	if rec.Status != improve.StatusDone {
		t.Fatalf("status %s: %s %s", rec.Status, rec.Reason, rec.Error)
	}
	rd := rec.Rounds[0]
	if len(rd.Candidates) != 3 || x.Count("improve") < 3 {
		t.Fatalf("candidates %+v, improver calls %d", rd.Candidates, x.Count("improve"))
	}
	if len(rd.Candidates[1].Tripwires) == 0 || rd.Candidates[1].Dropped == "" {
		t.Errorf("the cheating idea was not dropped: %+v", rd.Candidates[1])
	}
	if rd.Winner != rd.Candidates[2].ID || rd.Decision != improve.DecisionCommitted {
		t.Errorf("winner %q decision %s (%s), want %q", rd.Winner, rd.Decision, rd.Why, rd.Candidates[2].ID)
	}
	if x.Count(improve.PhaseAudit) != 1 || len(rec.Looks) != 1 {
		t.Errorf("audit calls %d looks %d, want 1 and 1", x.Count(improve.PhaseAudit), len(rec.Looks))
	}
}

func TestAuditLookSpentOnlyWhenTestRuns(t *testing.T) {
	for _, b := range []string{improvetest.Lazy, improvetest.Worse} {
		x := &improvetest.Exec{Behavior: b, Base: 3, NTasks: 40}
		rec := createRun(t, newRunner(t, x), w4Spec(nil))
		if len(rec.Looks) != 0 || rec.AuditLooks != 0 || x.Count(improve.PhaseAudit) != 0 {
			t.Errorf("%s: looks %v audit looks %d audit calls %d", b, rec.Looks, rec.AuditLooks, x.Count(improve.PhaseAudit))
		}
	}
}

func TestFallbackLevelsAcrossRounds(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120}
	rec := createRun(t, newRunner(t, x), w4Spec(nil))
	if len(rec.Looks) < 2 {
		t.Fatalf("looks %+v: %v", rec.Looks, decisions(rec))
	}
	want := []float64{0.1 / 3, 0.2 / 3}
	for i, w := range want {
		if math.Abs(rec.Looks[i].Level-w) > 1e-9 {
			t.Errorf("look %d level %v, want %v", i+1, rec.Looks[i].Level, w)
		}
	}
	if !rec.Looks[0].Committed || rec.Rounds[0].Commit == nil || rec.Rounds[0].Commit.Level != rec.Looks[0].Level {
		t.Errorf("round 1 commit %+v looks %+v", rec.Rounds[0].Commit, rec.Looks)
	}
	if !strings.HasPrefix(rec.Rounds[0].Why, "Kept: better on ") {
		t.Errorf("round why %q", rec.Rounds[0].Why)
	}
}

func TestHalvingChunksFixedBySeed(t *testing.T) {
	s := improve.DefaultScreener
	if s == nil {
		t.Fatal("no default screener")
	}
	dev := []string{}
	for i := 1; i <= 10; i++ {
		dev = append(dev, improvetest.TaskID(i))
	}
	if l := s.Levels(1, dev, 5); len(l) != 1 || !slices.Equal(l[0], dev) {
		t.Errorf("one idea: %v", l)
	}
	sizes := func(l [][]string) []int {
		out := []int{}
		for _, c := range l {
			out = append(out, len(c))
		}
		return out
	}
	for k, want := range map[int][]int{2: {10}, 3: {5, 5}, 4: {5, 5}, 5: {4, 3, 3}, 8: {4, 3, 3}} {
		a, b := s.Levels(k, dev, 7007), s.Levels(k, dev, 7007)
		if !slices.Equal(sizes(a), want) {
			t.Errorf("k %d sizes %v, want %v", k, sizes(a), want)
		}
		all := slices.Concat(a...)
		slices.Sort(all)
		if !slices.Equal(all, dev) {
			t.Errorf("k %d chunks do not partition the practice tasks: %v", k, a)
		}
		for i := range a {
			if !slices.Equal(a[i], b[i]) {
				t.Errorf("k %d not fixed by seed", k)
			}
		}
	}
	if a, b := s.Levels(4, dev, 1), s.Levels(4, dev, 2); slices.Equal(a[0], b[0]) {
		t.Error("two seeds gave the same chunks")
	}
}

func TestScreenSkipsAuditWithoutDevGain(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Worse, Base: 4, NTasks: 40}
	rec := createRun(t, newRunner(t, x), w4Spec(func(s *improve.Spec) { s.Rounds = 1 }))
	if x.Count(improve.PhaseAudit) != 0 {
		t.Errorf("audit ran %d times", x.Count(improve.PhaseAudit))
	}
	if rd := rec.Rounds[0]; rd.Decision != improve.DecisionRejected || !strings.HasPrefix(rd.Why, "Not tested on the gate: no idea beat the current version on the practice tasks") {
		t.Errorf("round %s: %q", rd.Decision, rd.Why)
	}
}

func TestSealedOnlyWhenIncumbentChanged(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120}
	rec := createRun(t, newRunner(t, x), w4Spec(nil))
	for _, rd := range rec.Rounds {
		if (rd.Decision == improve.DecisionCommitted) != (rd.Sealed != nil) {
			t.Errorf("round %d %s sealed %v", rd.N, rd.Decision, rd.Sealed != nil)
		}
	}
	lazy := &improvetest.Exec{Behavior: improvetest.Lazy, Base: 1}
	createRun(t, newRunner(t, lazy), w4Spec(func(s *improve.Spec) { s.Preset = improve.PresetSmall }))
	if n := lazy.Count(improve.PhaseSealed); n != 0 {
		t.Errorf("held-out curve off, but %d held-out scorings ran", n)
	}
}

func TestFinalExperimentInterleavesThreeArms(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120}
	rec := createRun(t, newRunner(t, x), w4Spec(nil))
	if x.Count(improve.PhaseFinal) != 1 || rec.Final == nil {
		t.Fatalf("final calls %d record %+v", x.Count(improve.PhaseFinal), rec.Final)
	}
	req := x.Request(improve.PhaseFinal)
	if len(req.Arms) != 3 || req.Arms[0].Ref != rec.Base.Ref || req.Arms[1].Ref != rec.Incumbent.Ref || req.Arms[2].Ref != rec.Controls.Null.Final.Ref {
		t.Fatalf("final arms %d", len(req.Arms))
	}
	if !slices.Equal(req.Tasks, rec.Splits.Sealed) || req.Trials != rec.Spec.Sealed.Trials || len(rec.Final.Arms) != 3 {
		t.Errorf("final tasks %v trials %d arms %v", req.Tasks, req.Trials, rec.Final.Arms)
	}
	v := rec.Verdict
	if v == nil || v.N != len(rec.Splits.Sealed) || v.Gain <= 0 || v.Null == nil || v.Retries == nil {
		t.Errorf("verdict %+v", v)
	}
}

func TestRetriesStopAtVisibleFailuresAndBudget(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120, TimeoutTries: 2}
	rec := createRun(t, newRunner(t, x), w4Spec(nil))
	rc := rec.Controls.Retries
	if rc == nil || x.Count(improve.PhaseRetries) != 2 || rc.K < 3 {
		t.Fatalf("retries %+v calls %d", rc, x.Count(improve.PhaseRetries))
	}
	if rc.UsedUSD <= 0 || rc.UsedUSD > rc.LoopUSD {
		t.Errorf("retry spend %v of %v", rc.UsedUSD, rc.LoopUSD)
	}
	capped := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120, TimeoutTries: 9}
	rec = createRun(t, newRunner(t, capped), w4Spec(func(s *improve.Spec) { s.Retries.MaxAttempts = 3 }))
	if capped.Count(improve.PhaseRetries) != 2 || rec.Controls.Retries.K != 3 {
		t.Errorf("capped retries calls %d k %d", capped.Count(improve.PhaseRetries), rec.Controls.Retries.K)
	}
}

func TestNullControlResumesFromCursor(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120, FailOn: improve.PhaseFinal}
	r := newRunner(t, x)
	rec, err := r.Create(context.Background(), w4Spec(nil), "ana")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Run(context.Background(), rec.ID)
	if err == nil || got.Status != improve.StatusFailed {
		t.Fatalf("want a failed final: %v %s", err, got.Status)
	}
	if c := got.Controls; c == nil || c.NullCursor == nil || c.NullCursor.Round != 3 {
		t.Fatalf("null cursor %+v", got.Controls)
	}
	n := x.Count("improve")
	got, err = r.Run(context.Background(), rec.ID)
	if err != nil || got.Status != improve.StatusDone {
		t.Fatalf("resume: %v %s", err, got.Status)
	}
	if x.Count("improve") != n {
		t.Errorf("the null improver ran again: %d calls after %d", x.Count("improve"), n)
	}
}

func TestHardStopBeforePhase(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40}
	r := newRunner(t, x)
	var mu sync.Mutex
	events := []string{}
	r.OnEvent = func(kind string, rec *improve.Record) {
		mu.Lock()
		events = append(events, kind+":"+rec.Status)
		mu.Unlock()
	}
	rec, err := r.Create(context.Background(), w4Spec(func(s *improve.Spec) { s.Budget.TotalUSD = 1 }), "ana")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Run(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != improve.StatusStopped || !strings.HasPrefix(got.Reason, "Stopped before the improver: the next step would pass the spending limit of $1.00 (spent $") {
		t.Fatalf("status %s reason %q", got.Status, got.Reason)
	}
	if x.Count("improve") != 0 || got.SpentUSD <= 0 || got.SpentUSD > 1 {
		t.Errorf("improver calls %d spent %v", x.Count("improve"), got.SpentUSD)
	}
	if !slices.Equal(events, []string{"improvement.finished:stopped"}) {
		t.Errorf("events %v", events)
	}
}

func TestStopIsNotOverwritten(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40, Hold: improve.PhaseScreen,
		Held: make(chan struct{}), Release: make(chan struct{})}
	r := newRunner(t, x)
	rec, err := r.Create(context.Background(), w4Spec(nil), "ana")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _, _ = r.Run(context.Background(), rec.ID); close(done) }()
	<-x.Held
	if _, err := r.Stop(rec.ID, "bo"); err != nil {
		t.Fatal(err)
	}
	close(x.Release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("run did not end")
	}
	got, err := r.Get(rec.ID)
	if err != nil || got.Status != improve.StatusStopped {
		t.Fatalf("status %s after stop: %v", got.Status, err)
	}
}

func TestExtendUsesReserveAlpha(t *testing.T) {
	if improve.RunExtend == nil {
		t.Fatal("RunExtend is not set")
	}
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 0, NTasks: 120}
	r := newRunner(t, x)
	pool := []string{}
	for i := 1; i <= 100; i++ {
		pool = append(pool, improvetest.TaskID(i))
	}
	rec := createRun(t, r, w4Spec(func(s *improve.Spec) {
		s.Tasks = pool
		s.Splits = improve.SplitSpec{Dev: 0.48, Audit: 0.48, Sealed: 0.04}
		s.Claim.Extension = true
		s.Budget.TotalUSD = 50
	}))
	if rec.Verdict == nil || rec.Verdict.State != improve.VerdictInsufficientSample {
		t.Fatalf("verdict %+v (%v)", rec.Verdict, decisions(rec))
	}
	got, err := improve.RunExtend(r, context.Background(), rec.ID, 2, true, 5)
	if err != nil {
		t.Fatal(err)
	}
	e := got.Extension
	if e == nil || math.Abs(e.Level-improve.DefaultExtensionAlpha) > 1e-12 || got.Revision != 1 || got.Status != improve.StatusDone {
		t.Fatalf("extension %+v revision %d status %s", e, got.Revision, got.Status)
	}
	if e.N <= len(rec.Splits.Sealed) || got.Verdict.Level != improve.DefaultExtensionAlpha || got.Final == nil {
		t.Errorf("second look n %d verdict level %v", e.N, got.Verdict.Level)
	}
	if got.Spec.Budget.TotalUSD != rec.Spec.Budget.TotalUSD+5 {
		t.Errorf("limit %v", got.Spec.Budget.TotalUSD)
	}
	if _, err := improve.RunExtend(r, context.Background(), rec.ID, 2, false, 1); !errors.Is(err, improve.ErrState) {
		t.Errorf("second extension: %v", err)
	}
}

func TestNoCommitSkipsFinal(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Lazy, Base: 1}
	rec := createRun(t, newRunner(t, x), w4Spec(nil))
	if x.Count(improve.PhaseFinal) != 0 || x.Count(improve.PhaseRetries) != 0 || rec.Final != nil {
		t.Errorf("final %d retries %d", x.Count(improve.PhaseFinal), x.Count(improve.PhaseRetries))
	}
	if rec.Verdict == nil || rec.Verdict.State != improve.VerdictNoChange {
		t.Errorf("verdict %+v", rec.Verdict)
	}
}

// TestNullLoopsFalseCommitRate runs 200 loops whose ideas never change the
// chance of success: the share of loops that keep any version stays near
// the gate alpha 0.10.
func TestNullLoopsFalseCommitRate(t *testing.T) {
	loops, commits := 200, 0
	for i := range loops {
		x := &improvetest.Exec{Behavior: improvetest.Noise, Noise: 0.5, NoiseSeed: int64(i + 1), NTasks: 40}
		rec := createRun(t, newRunner(t, x), w4Spec(func(s *improve.Spec) { s.Commit.Trials = 1; s.Controls = []string{} }))
		for _, rd := range rec.Rounds {
			if rd.Decision == improve.DecisionCommitted {
				commits++
				break
			}
		}
	}
	rate := float64(commits) / float64(loops)
	t.Logf("null loops %d, loops with a kept version %d (%.3f)", loops, commits, rate)
	if rate > 0.15 {
		t.Errorf("false commit rate %.3f above 0.15", rate)
	}
}
