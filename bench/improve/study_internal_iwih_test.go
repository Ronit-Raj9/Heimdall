package improve

import (
	"context"
	"os"
	"strings"
	"testing"
)

// countExec counts Score calls; the loops it serves are already done.
type countExec struct{ scores int }

func (x *countExec) Load(context.Context, string, Editable) (Version, error) { return Version{}, nil }
func (x *countExec) Freeze(context.Context, Version, []byte, Lineage) (Version, error) {
	return Version{}, nil
}
func (x *countExec) Tasks(context.Context, *Spec) ([]TaskInfo, error) {
	out := []TaskInfo{}
	for _, id := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7", "t8", "t9", "t10"} {
		out = append(out, TaskInfo{ID: id})
	}
	return out, nil
}
func (x *countExec) Protect(context.Context, *Spec, []string) (Protected, error) {
	return Protected{}, nil
}
func (x *countExec) Improve(context.Context, ImproveRequest) (*ImproveResult, error) {
	return &ImproveResult{}, nil
}
func (x *countExec) Score(context.Context, ScoreRequest) (*ScoreResult, error) {
	x.scores++
	return &ScoreResult{}, nil
}
func (x *countExec) CheckImprover(context.Context, *Spec) error     { return nil }
func (x *countExec) Runs(context.Context, string) ([]RunRef, error) { return nil, nil }

// Security review (A2.10): the study's own held-out check stops before it
// would pass the study's limit (loops times the spec's limit), like every
// other scoring call, and does not score.
func TestStudyFinalHasAHardStop(t *testing.T) {
	x := &countExec{}
	r := &Runner{Root: t.TempDir(), Exec: x}
	st := &Study{ID: "ims_cap-abc123", Name: "cap", Status: StatusRunning, Sealed: []string{"t1", "t2", "t3", "t4"},
		Spec: Spec{Replicate: ReplicationPlan{Loops: 2}, Budget: BudgetSpec{TotalUSD: 10}, Sealed: PhaseSpec{Trials: 2}}}
	for i, id := range []string{"imp_cap-loop-1", "imp_cap-loop-2"} {
		rec := &Record{ID: id, Name: id, Status: StatusDone, Study: st.ID, InStudy: true, Rounds: []*Round{},
			Base: Version{Ref: "agents/a.yaml@v1"}, Incumbent: Version{Ref: "agents/a.yaml@v" + string(rune('2'+i))},
			Baseline: &Baseline{CostUSD: 9.5, Dev: ArmScore{Runs: 95}}}
		if err := os.MkdirAll(r.dir(id), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := r.save(rec); err != nil {
			t.Fatal(err)
		}
		st.Loops = append(st.Loops, id)
	}
	if err := r.saveStudy(st); err != nil {
		t.Fatal(err)
	}
	got, err := r.RunStudy(context.Background(), st.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Spent 19 of 20; the final would be 3 arms x 4 tasks x 2 trials x 0.10.
	if x.scores != 0 || got.Status != StatusStopped || !strings.HasPrefix(got.Reason, "Stopped before the study's held-out check: the next step would pass the spending limit of $20.00 (spent $19.00).") {
		t.Fatalf("scores %d status %s reason %q", x.scores, got.Status, got.Reason)
	}
}
