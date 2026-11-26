package improve

import (
	"encoding/json"
	"strings"
	"testing"
)

// viewLeakRecord is a record that carries an experiment id and a task id
// for every phase, so a test can look for each in the served report.
func viewLeakRecord() *Record {
	cand := Version{Ref: "agents/a.yaml@v1"}
	return &Record{
		ID: "imp_leak-1", Name: "leak", Status: StatusDone,
		Spec: Spec{Agent: "agents/a.yaml", Suite: "s", Rounds: 1},
		Splits: Splits{Dev: []string{"dev-task-01"}, Audit: []string{"gate-task-secret-01"}, Sealed: []string{"held-task-secret-01"},
			Hash: "h"},
		Baseline: &Baseline{DevExp: "exp-dev-base", SealedExp: "exp-sealed-base-leak"},
		Rounds: []*Round{{
			N: 1, Stage: StageDone, Decision: DecisionCommitted, Candidate: &cand,
			Gates: []GateResult{{Name: GateAnswerKey, Fired: true, Detail: "1 match(es) with gate or held-out task content",
				Evidence: []string{"task id held-task-secret-01", "task id gate-task-secret-01", "task id dev-task-01"}}},
			Screen: &Comparison{ExpID: "exp-screen-ok"},
			Audit:  &Comparison{ExpID: "exp-audit-leak"},
			Stages: []StageResult{{Level: 1, Tasks: 1, ExpID: "exp-stage-ok"}},
		}},
		Controls: &Controls{
			BudgetMatched: &BudgetMatched{ExpID: "exp-budget-leak"},
			Null:          &NullControl{ExpID: "exp-null-leak"},
		},
		Final:     &FinalClaim{ExpID: "exp-final-leak", N: 1},
		Extension: &FinalClaim{ExpID: "exp-extension-leak", N: 1},
	}
}

// TestViewHidesAuditExpIDs: the served report holds the practice screen's
// experiment id and practice task ids, never an experiment id of an audit,
// sealed, control, final or extension phase, and never a gate or held-out
// task id (A1.10).
func TestViewHidesAuditExpIDs(t *testing.T) {
	r := &Runner{Root: t.TempDir()}
	b, err := json.Marshal(r.View(viewLeakRecord()))
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, bad := range []string{"exp-audit-leak", "exp-sealed-base-leak", "exp-budget-leak", "exp-null-leak", "exp-final-leak",
		"exp-extension-leak", "gate-task-secret-01", "held-task-secret-01"} {
		if strings.Contains(out, bad) {
			t.Errorf("served report contains %q", bad)
		}
	}
	for _, ok := range []string{"exp-screen-ok", "dev-task-01"} {
		if !strings.Contains(out, ok) {
			t.Errorf("served report lost %q", ok)
		}
	}
}
