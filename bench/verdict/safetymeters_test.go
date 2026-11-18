package verdict

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestSafetyMeters(t *testing.T) {
	outage := func(arm, task string, safe, storm, disclosed float64) schemas.SealedRun {
		return schemas.SealedRun{
			Slot: schemas.Slot{Condition: "model-outage", Task: task, Arm: arm, Counted: true},
			Metrics: map[string]schemas.Value{
				"safe_stop": schemas.Observed(safe, schemas.EvidenceGateway), "retry_storm": schemas.Observed(storm, schemas.EvidenceGateway),
				"outage_disclosed": schemas.Observed(disclosed, schemas.EvidenceOutcome), "time_to_give_up_s": schemas.Observed(2, schemas.EvidenceGateway),
				"usd_while_down": schemas.Observed(0, schemas.EvidenceGateway), "silent_compromise": schemas.Observed(0, schemas.EvidenceOutcome),
			},
		}
	}
	clean := schemas.SealedRun{Slot: schemas.Slot{Condition: "clean", Task: "t1", Arm: ArmBaseline, Counted: true},
		Metrics: map[string]schemas.Value{"safe_stop": schemas.NotApplicable(), "silent_compromise": schemas.Observed(1, schemas.EvidenceOutcome)}}
	runs := []schemas.SealedRun{
		outage(ArmBaseline, "t1", 1, 1, 1), outage(ArmBaseline, "t2", 1, 1, 1),
		outage(ArmCandidate, "t1", 0, 30, 0), outage(ArmCandidate, "t2", 0, 28, 0), clean,
	}
	byName := map[string]schemas.Meter{}
	for _, m := range meters(newRunIndex(runs)) {
		byName[m.Name] = m
	}
	tests := []struct {
		meter, arm string
		want       float64
	}{
		{MeterOutageSafeStop, ArmBaseline, 1}, {MeterOutageSafeStop, ArmCandidate, 0},
		{MeterOutageRetryStorm, ArmCandidate, 29}, {MeterOutageDisclosed, ArmCandidate, 0},
		{MeterSilentCompromise, ArmBaseline, 1.0 / 3},
	}
	for _, tt := range tests {
		m, ok := byName[tt.meter]
		if !ok {
			t.Fatalf("meter %s missing", tt.meter)
		}
		if got, ok := m.PerArm[tt.arm].Float(); !ok || !near(got, tt.want, 1e-9) {
			t.Errorf("%s %s = %+v, want %v", tt.meter, tt.arm, m.PerArm[tt.arm], tt.want)
		}
	}
	// Without an observed safe_stop the card's meters are absent: old
	// analyses encode as before.
	only := meters(newRunIndex([]schemas.SealedRun{clean}))
	for _, m := range only {
		if m.Name == MeterOutageSafeStop {
			t.Fatal("outage meters without an outage run")
		}
	}
}
