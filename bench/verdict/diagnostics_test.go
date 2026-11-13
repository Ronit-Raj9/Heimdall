package verdict

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// withReceipt attaches a receipt for probe to every run.
func withReceipt(runs []schemas.SealedRun, probe string, steps map[schemas.ReceiptStep]schemas.StepState) []schemas.SealedRun {
	out := append([]schemas.SealedRun(nil), runs...)
	for i := range out {
		rc := schemas.Receipt{ProbeID: probe, Layer: schemas.LayerFixture, Steps: map[schemas.ReceiptStep]schemas.ReceiptEntry{}}
		for step, state := range steps {
			rc.Steps[step] = schemas.ReceiptEntry{State: state}
		}
		out[i].Receipts = []schemas.Receipt{rc}
	}
	return out
}

func TestDiagnostics(t *testing.T) {
	clean := schemas.ConditionClean
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: clean, Trials: 3}, {ID: condStale, Trials: 3}}
	steps := map[schemas.ReceiptStep]schemas.StepState{
		schemas.StepArmed:   schemas.StepYes,
		schemas.StepFired:   schemas.StepNotApplicable,
		schemas.StepVisible: schemas.StepUnknown,
	}
	runs := concat(
		withReceipt(passes(condStale, ArmBaseline, 3, 0, 1, 2), condStale, steps),
		withReceipt(passes(condStale, ArmCandidate, 3, 3, 3, 3), condStale, steps),
		passes(clean, ArmBaseline, 3, 3, 3, 3),
		passes(clean, ArmCandidate, 3, 3, 3, 2),
	)
	rows := diagnostics(m, newRunIndex(runs), m.Experiment.Primary)
	if len(rows) != 2 || rows[0].Condition != clean || rows[1].Condition != condStale {
		t.Fatalf("rows %+v, want clean then stale-notes", rows)
	}
	cleanRow, staleRow := rows[0], rows[1]
	tests := []struct {
		name string
		ok   bool
	}{
		{"clean axis is solve", cleanRow.Axis == axisSolve},
		{"probe axis falls back to the probe id", staleRow.Axis == condStale},
		{"task counts", cleanRow.NTasks == 3 && staleRow.NTasks == 3},
		{"per-arm success", staleRow.PerArm[ArmBaseline] == schemas.Ratio{Num: 3, Den: 9} && staleRow.PerArm[ArmCandidate] == schemas.Ratio{Num: 9, Den: 9}},
		{"primary row is inferential", !cleanRow.Descriptive && cleanRow.Effect != nil && cleanRow.Effect.PValue != nil},
		{"probe row is descriptive", staleRow.Descriptive && staleRow.Effect != nil && staleRow.Effect.PValue == nil && staleRow.Effect.Descriptive},
		{"probe effect d_bar", staleRow.Effect != nil && near(staleRow.Effect.DBar, 2.0/3, 1e-12)},
		{"clean has no receipts", cleanRow.Receipts == nil},
		{"armed step counted", staleRow.Receipts[ArmBaseline][schemas.StepArmed] == schemas.Ratio{Num: 9, Den: 9}},
		{"unknown step kept apart", staleRow.Receipts[ArmCandidate][schemas.StepVisible] == schemas.Ratio{Num: 0, Den: 9, Unknown: 9}},
		{"not applicable step left out", !hasStep(staleRow.Receipts[ArmBaseline], schemas.StepFired)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Fatalf("clean %+v; stale %+v", cleanRow, staleRow)
			}
		})
	}
}

func hasStep(steps map[schemas.ReceiptStep]schemas.Ratio, step schemas.ReceiptStep) bool {
	_, ok := steps[step]
	return ok
}

func TestConditionOrder(t *testing.T) {
	m := testManifest(ArmBaseline, ArmCandidate)
	m.Experiment.Conditions = []schemas.ConditionSpec{{ID: "stale-notes"}, {ID: "clean"}, {ID: "model-outage"}}
	idx := runIndex{"clean": nil, "stale-notes": nil, "test-crash": nil}
	got := conditionOrder(m, idx)
	want := []string{"stale-notes", "clean", "test-crash"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		t.Run(want[i], func(t *testing.T) {
			if got[i] != want[i] {
				t.Fatalf("position %d: got %s", i, got[i])
			}
		})
	}
}

func TestCountStep(t *testing.T) {
	tests := []struct {
		state schemas.StepState
		want  schemas.Ratio
	}{
		{schemas.StepYes, schemas.Ratio{Num: 1, Den: 1}},
		{schemas.StepNo, schemas.Ratio{Den: 1}},
		{schemas.StepUnknown, schemas.Ratio{Den: 1, Unknown: 1}},
		{"", schemas.Ratio{Den: 1, Unknown: 1}},
		{schemas.StepNotApplicable, schemas.Ratio{}},
	}
	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			steps := map[schemas.ReceiptStep]schemas.Ratio{}
			countStep(steps, schemas.StepFired, tt.state)
			if got := steps[schemas.StepFired]; got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
