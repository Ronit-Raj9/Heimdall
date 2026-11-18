package canary

import (
	"os"
	"testing"
)

// TestSimulatedFalseAlarmRate holds the alarm rule to its documented
// false-alarm rate: with no drift, a normal-sensitivity canary on 8 tasks
// alarms on at most 0.5% of triggers (docs/drift-canary.md quotes the
// larger runs of `canary simulate`).
func TestSimulatedFalseAlarmRate(t *testing.T) {
	for _, spread := range []bool{false, true} {
		for _, trials := range []int{1} {
			r := Simulate(SimConfig{Tasks: 8, Trials: trials, Triggers: 50, Reps: 100, Sensitivity: SensitivityNormal, Spread: spread, Seed: 7})
			t.Logf("spread %v trials %d: alarm/trigger %.4f, flag/look %.4f, confirm/trigger %.4f", spread, trials, r.AlarmPerTrigger, r.FlagPerLook, r.ConfirmPerTrigger)
			if r.AlarmPerTrigger > 0.005 {
				t.Errorf("spread %v trials %d: false alarm per trigger %.4f, want <= 0.005", spread, trials, r.AlarmPerTrigger)
			}
			// Two families each at alpha: a single look flags at most 2 alpha.
			if r.FlagPerLook > 2*r.Alpha {
				t.Errorf("spread %v trials %d: single-look flag rate %.4f above twice the family level %.2f", spread, trials, r.FlagPerLook, r.Alpha)
			}
		}
	}
}

// TestSimulatedPower checks that a large drift is caught on the trigger it
// starts: every task losing 60 points of success.
func TestSimulatedPower(t *testing.T) {
	r := Simulate(SimConfig{Tasks: 8, Trials: 1, Triggers: 3, Reps: 200, Sensitivity: SensitivityNormal, DropPoints: 0.6, Seed: 11})
	t.Logf("drop 60 points: first %.3f, by 3 %.3f", r.DetectFirst, r.DetectBy3)
	if r.DetectBy3 < 0.5 {
		t.Errorf("a 60-point drop was caught within 3 triggers in %.2f of replications, want >= 0.5", r.DetectBy3)
	}
	lat := Simulate(SimConfig{Tasks: 8, Trials: 1, Triggers: 3, Reps: 200, Sensitivity: SensitivityNormal, ShiftRel: 1.0, Seed: 12})
	t.Logf("latency doubled: first %.3f, by 3 %.3f", lat.DetectFirst, lat.DetectBy3)
	if lat.DetectFirst < 0.9 {
		t.Errorf("a doubled latency was caught on the first trigger in %.2f of replications, want >= 0.9", lat.DetectFirst)
	}
}

// TestSimulateTable prints the table the docs quote when CANARY_SIM=1.
func TestSimulateTable(t *testing.T) {
	if os.Getenv("CANARY_SIM") == "" {
		t.Skip("set CANARY_SIM=1 to print the docs table")
	}
	for _, s := range Sensitivities {
		for _, spread := range []bool{false, true} {
			for _, trials := range []int{1, 3} {
				r := Simulate(SimConfig{Tasks: 8, Trials: trials, Triggers: 365, Reps: 400, Sensitivity: s, Spread: spread, Seed: 1})
				t.Logf("null %s spread %v trials %d: alpha %.2f flag/look %.4f alarm/trigger %.5f confirm/trigger %.4f any30 %.3f any365 %.3f",
					s, spread, trials, r.Alpha, r.FlagPerLook, r.AlarmPerTrigger, r.ConfirmPerTrigger, r.AnyAlarm30, r.AnyAlarm365)
			}
		}
		for _, drop := range []float64{0.2, 0.3, 0.45, 0.6} {
			for _, trials := range []int{1, 3} {
				r := Simulate(SimConfig{Tasks: 8, Trials: trials, Triggers: 3, Reps: 400, Sensitivity: s, DropPoints: drop, Seed: 2})
				t.Logf("drop %.2f %s trials %d: first %.3f by3 %.3f", drop, s, trials, r.DetectFirst, r.DetectBy3)
			}
		}
	}
}
