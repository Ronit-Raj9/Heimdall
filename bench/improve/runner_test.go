package improve_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
)

func testSpec() *improve.Spec {
	s := &improve.Spec{Name: "notes-loop", Agent: "agents/scripted-improver.yaml", Suite: "scripted@1.0.0", Seed: 7,
		Editable: improve.Editable{Files: []string{improvetest.NotesPath}},
		Commit:   improve.CommitSpec{Trials: 5}, Sealed: improve.PhaseSpec{Trials: 2}}
	s.Defaults()
	return s
}

func newRunner(t *testing.T, x *improvetest.Exec) *improve.Runner {
	t.Helper()
	return &improve.Runner{Root: filepath.Join(t.TempDir(), improve.DefaultDir), Exec: x}
}

func createRun(t *testing.T, r *improve.Runner, s *improve.Spec) *improve.Record {
	t.Helper()
	rec, err := r.Create(context.Background(), s, "ana")
	if err != nil {
		t.Fatal(err)
	}
	out, err := r.Run(context.Background(), rec.ID)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return out
}

func decisions(rec *improve.Record) []string {
	out := []string{}
	for _, rd := range rec.Rounds {
		out = append(out, rd.Decision+":"+rd.Why)
	}
	return out
}

func TestHonestImproverCommitsAndReports(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40}
	r := newRunner(t, x)
	rec := createRun(t, r, testSpec())
	if rec.Status != improve.StatusDone || len(rec.Rounds) != 3 {
		t.Fatalf("status %s rounds %d: %s %s", rec.Status, len(rec.Rounds), rec.Reason, rec.Error)
	}
	if rec.Rounds[0].Decision != improve.DecisionCommitted || rec.Incumbent.Number < 2 {
		t.Fatalf("round 1 not committed: %v", decisions(rec))
	}
	if rec.Rounds[0].Audit.EValue == nil || !rec.Rounds[0].Audit.EValue.Better {
		t.Errorf("commit without an e-value crossing: %+v", rec.Rounds[0].Audit)
	}
	v := rec.Verdict
	if v == nil || v.Commits == 0 || v.SealedGain <= 0 || v.TotalUSD <= 0 {
		t.Fatalf("verdict: %+v", v)
	}
	if rec.Controls == nil || rec.Controls.BudgetMatched == nil || rec.Controls.Null == nil {
		t.Fatalf("controls missing: %+v", rec.Controls)
	}
	if rec.Controls.BudgetMatched.Trials < 1 || rec.Controls.Null.Final.Number == rec.Base.Number {
		t.Errorf("controls: %+v %+v", rec.Controls.BudgetMatched, rec.Controls.Null)
	}
	// The improver read only dev results: never an audit or sealed task.
	for i, ev := range x.Evidence {
		if i >= 3 {
			break // null improver rounds get none
		}
		if len(ev) == 0 {
			t.Errorf("round %d improver got no evidence", i+1)
		}
		for _, e := range ev {
			if !slices.Contains(rec.Splits.Dev, e.Task) {
				t.Errorf("round %d evidence names non-dev task %s", i+1, e.Task)
			}
		}
	}
	for _, ev := range x.Evidence[3:] {
		if len(ev) != 0 {
			t.Error("null improver got evidence")
		}
	}
	d := r.View(rec)
	if len(d.Curve) != 4 || d.Curve[0].Event != improve.NodeBase || d.Curve[1].Sealed == nil || d.Band == nil || d.Band.NullMean == nil {
		t.Errorf("view curve %+v band %+v", d.Curve, d.Band)
	}
	if !d.Lineage.ChainOK || len(d.Lineage.Nodes) < 3 || d.Splits.Audit == 0 || d.Spec.Tasks != nil {
		t.Errorf("lineage %+v splits %+v", d.Lineage, d.Splits)
	}
	var control bool
	for _, n := range d.Lineage.Nodes {
		control = control || n.Status == improve.NodeControl
	}
	if !control {
		t.Error("no null improver node in the lineage")
	}
	if len(d.Rounds[0].Diff) != 1 || d.Rounds[0].Diff[0].Path != improvetest.NotesPath {
		t.Errorf("diff %+v", d.Rounds[0].Diff)
	}
	if _, err := r.Run(context.Background(), rec.ID); !errors.Is(err, improve.ErrState) {
		t.Errorf("rerun of a finished improvement: %v", err)
	}
}

// TestSealedNeverDecides: moving only the sealed scores changes no
// decision of the loop.
func TestSealedNeverDecides(t *testing.T) {
	a := createRun(t, newRunner(t, &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40}), testSpec())
	b := createRun(t, newRunner(t, &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40, SealedBonus: 5}), testSpec())
	if !slices.Equal(decisions(a), decisions(b)) {
		t.Fatalf("sealed scores moved decisions:\n%v\n%v", decisions(a), decisions(b))
	}
	if a.Rounds[0].Sealed.Mean == b.Rounds[0].Sealed.Mean {
		t.Error("the sealed bonus did not reach the sealed scores")
	}
}

func TestLazyImproverChangesNothing(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Lazy, Base: 2}
	rec := createRun(t, newRunner(t, x), testSpec())
	if rec.Verdict.State != improve.VerdictNoChange || x.Count(improve.PhaseScreen) != 0 || x.Count(improve.PhaseAudit) != 0 {
		t.Fatalf("verdict %s screen %d audit %d", rec.Verdict.State, x.Count(improve.PhaseScreen), x.Count(improve.PhaseAudit))
	}
	for _, rd := range rec.Rounds {
		if rd.Why != "the improver changed nothing" || rd.Sealed == nil {
			t.Errorf("round %d: %s", rd.N, rd.Why)
		}
	}
}

func TestTripwiresPause(t *testing.T) {
	cases := []struct {
		behavior, gate string
	}{
		{improvetest.Cheat, improve.GateAnswerKey},
		{improvetest.Frozen, improve.GateFrozenField},
		{improvetest.Spender, improve.TripSpend},
		{improvetest.Quiet, improve.GateMonitorParity},
	}
	for _, c := range cases {
		t.Run(c.behavior, func(t *testing.T) {
			x := &improvetest.Exec{Behavior: c.behavior, Base: 1, NTasks: 40}
			r := newRunner(t, x)
			rec := createRun(t, r, testSpec())
			if rec.Status != improve.StatusAwaiting || !slices.Contains(rec.Rounds[0].Tripwires, c.gate) {
				t.Fatalf("status %s tripwires %v", rec.Status, rec.Rounds[0].Tripwires)
			}
			if x.Count(improve.PhaseAudit) != 0 {
				t.Error("a tripped candidate reached the audit split")
			}
			if _, err := r.Run(context.Background(), rec.ID); !errors.Is(err, improve.ErrState) {
				t.Errorf("run while awaiting approval: %v", err)
			}
			if _, err := r.Approve(rec.ID, 1, "bo", " "); err == nil {
				t.Error("approval without a reason accepted")
			}
		})
	}
}

func TestStopAtTripwireRecordsLineage(t *testing.T) {
	r := newRunner(t, &improvetest.Exec{Behavior: improvetest.Cheat, Base: 1})
	rec := createRun(t, r, testSpec())
	stopped, err := r.Stop(rec.ID, "bo")
	if err != nil || stopped.Status != improve.StatusStopped {
		t.Fatalf("stop: %v %+v", err, stopped)
	}
	lv, err := r.Lineage(rec.ID)
	if err != nil || !lv.ChainOK || lv.Nodes[len(lv.Nodes)-1].Status != improve.NodeTripped {
		t.Fatalf("lineage %+v %v", lv, err)
	}
	if _, err := r.Stop(rec.ID, "bo"); !errors.Is(err, improve.ErrState) {
		t.Errorf("second stop: %v", err)
	}
}

func TestApproveContinuesAndBlocksTheClaim(t *testing.T) {
	r := newRunner(t, &improvetest.Exec{Behavior: improvetest.Quiet, Base: 1, NTasks: 40})
	rec := createRun(t, r, testSpec())
	for rec.Status == improve.StatusAwaiting {
		n := len(rec.Rounds)
		if _, err := r.Approve(rec.ID, n, "bo", "logging change reviewed"); err != nil {
			t.Fatal(err)
		}
		var err error
		if rec, err = r.Run(context.Background(), rec.ID); err != nil {
			t.Fatal(err)
		}
	}
	if rec.Status != improve.StatusDone || rec.Verdict.Overrides == 0 || rec.Verdict.State == improve.VerdictSupported {
		t.Fatalf("status %s verdict %+v", rec.Status, rec.Verdict)
	}
}

func TestFailedExperimentResumes(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40, FailOn: improve.PhaseAudit}
	r := newRunner(t, x)
	rec, err := r.Create(context.Background(), testSpec(), "ana")
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Run(context.Background(), rec.ID)
	if err == nil || got.Status != improve.StatusFailed || !strings.Contains(got.Error, "gate check") {
		t.Fatalf("expected a failed audit: %v %+v", err, got)
	}
	improves := x.Count("improve")
	got, err = r.Run(context.Background(), rec.ID)
	if err != nil || got.Status != improve.StatusDone {
		t.Fatalf("resume: %v %s", err, got.Status)
	}
	if x.Count(improve.PhaseScreen) != 3 {
		t.Errorf("resume re-ran the screen: %d", x.Count(improve.PhaseScreen))
	}
	if x.Count("improve") != improves+2+3 { // rounds 2 and 3, then 3 null rounds
		t.Errorf("improver calls %d after %d", x.Count("improve"), improves)
	}
}

func TestLineageTamperDetected(t *testing.T) {
	r := newRunner(t, &improvetest.Exec{Behavior: improvetest.Honest, Base: 1, NTasks: 40})
	rec := createRun(t, r, testSpec())
	p := filepath.Join(r.Root, rec.ID, improve.LineageFile)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Replace(string(b), `"rejected"`, `"committed"`, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), `"rejected"`) == 0 {
		if err := os.WriteFile(p, []byte(strings.Replace(string(b), `"version 0"`, `"version zero"`, 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	lv, err := r.Lineage(rec.ID)
	if err != nil || lv.ChainOK || lv.ChainError == "" {
		t.Fatalf("tampered chain verified: %+v %v", lv, err)
	}
}

func TestListAndBusy(t *testing.T) {
	r := newRunner(t, &improvetest.Exec{Behavior: improvetest.Lazy, Base: 1})
	if l, err := r.List(); err != nil || len(l) != 0 {
		t.Fatalf("empty list: %v %v", l, err)
	}
	rec := createRun(t, r, testSpec())
	l, err := r.List()
	if err != nil || len(l) != 1 || l[0].ID != rec.ID {
		t.Fatalf("list %v %v", l, err)
	}
	s := improve.Summarize(rec, false)
	if s.RoundsDone != 3 || s.Verdict != improve.VerdictNoChange {
		t.Errorf("summary %+v", s)
	}
	if _, err := r.Get("imp_nope-x"); !errors.Is(err, improve.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}
