package verdict

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// meterNames returns the meter names in order.
func meterNames(ms []schemas.Meter) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}

func TestStepFamilyMetersOnlyWhenRecorded(t *testing.T) {
	clean := schemas.ConditionClean
	runs := passes(clean, ArmBaseline, 1, 0, 1, 1)
	before := meterNames(meters(newRunIndex(runs)))
	for _, n := range before {
		if n == MeterModelTurnsFailedMedian || n == MeterAgentStepsMedian || n == MeterToolProofRate || n == MeterToolTrafficUnparsed {
			t.Fatalf("family meter %s present on runs that never recorded it: %v", n, before)
		}
	}
	// agent_steps alone (runs recorded before the family gate) changes nothing.
	older := withMetric(runs, "agent_steps", usd(4), usd(5), usd(6))
	if got := meterNames(meters(newRunIndex(older))); len(got) != len(before) {
		t.Fatalf("meters changed without the gate metric: %v vs %v", got, before)
	}

	withFamily := withMetric(older, "model_turns_failed", usd(0), usd(1), usd(2))
	withFamily = withMetric(withFamily, "model_attempts", usd(3), usd(4), usd(9))
	withFamily = withMetric(withFamily, "tool_proof_rate", usd(1), usd(0.5), usd(0))
	withFamily = withMetric(withFamily, "tool_proof_none", usd(0), usd(0.5), usd(1))
	withFamily = withMetric(withFamily, "tool_traffic_unparsed", usd(0), usd(0), usd(2))
	ms := meters(newRunIndex(withFamily))
	got := meterNames(ms)
	at := -1
	for i, n := range got {
		if n == MeterStepsMedian {
			at = i
		}
	}
	want := []string{MeterStepsMedian, MeterModelTurnsFailedMedian, MeterModelAttemptsMedian, MeterAgentStepsMedian, MeterToolProofRate, "tool_proof_none", MeterToolTrafficUnparsed, MeterToolCallsMedian}
	if at < 0 || at+len(want) > len(got) {
		t.Fatalf("meters %v", got)
	}
	for i, n := range want {
		if got[at+i] != n {
			t.Fatalf("meter %d = %s, want %s (all %v)", at+i, got[at+i], n, got)
		}
	}
	byName := map[string]schemas.Meter{}
	for _, m := range ms {
		byName[m.Name] = m
	}
	if x, ok := byName[MeterModelTurnsFailedMedian].PerArm[ArmBaseline].Float(); !ok || x != 1 {
		t.Fatalf("failed turns median %v", byName[MeterModelTurnsFailedMedian].PerArm[ArmBaseline])
	}
	if x, ok := byName[MeterToolProofRate].PerArm[ArmBaseline].Float(); !ok || !near(x, 0.5, 1e-12) {
		t.Fatalf("proof rate mean %v", byName[MeterToolProofRate].PerArm[ArmBaseline])
	}
}
