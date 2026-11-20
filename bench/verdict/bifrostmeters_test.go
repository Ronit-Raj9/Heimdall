package verdict

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func mcpRun(arm, task string, mismatch schemas.Value, overhead float64) schemas.SealedRun {
	return schemas.SealedRun{Slot: schemas.Slot{Arm: arm, Task: task, Condition: "clean", Counted: true},
		Metrics: map[string]schemas.Value{"bifrost_mcp_witness_mismatch": mismatch, "gateway_overhead_ms": schemas.Observed(overhead, schemas.EvidenceGateway)}}
}

func TestBifrostMCPWitness(t *testing.T) {
	na := schemas.NotApplicable()
	zero := schemas.Observed(0, schemas.EvidenceBoundary)
	two := schemas.Observed(2, schemas.EvidenceBoundary)
	if _, ok := bifrostMCPWitness(nil, newRunIndex([]schemas.SealedRun{mcpRun("A", "t1", na, 1), mcpRun("B", "t1", na, 1)})); ok {
		t.Fatal("no run served MCP through Bifrost: the instrument must not appear")
	}
	c, ok := bifrostMCPWitness(nil, newRunIndex([]schemas.SealedRun{mcpRun("A", "t1", na, 1), mcpRun("B", "t1", zero, 1), mcpRun("B", "t2", zero, 1)}))
	if !ok || c.Status != schemas.InstrumentPass || c.PerArm["B"].Den != 2 {
		t.Fatalf("agreeing witnesses: %+v", c)
	}
	c, _ = bifrostMCPWitness(nil, newRunIndex([]schemas.SealedRun{mcpRun("B", "t1", zero, 1), mcpRun("B", "t2", two, 1)}))
	if c.Status != schemas.InstrumentFail || c.PerArm["B"].Num != 1 {
		t.Fatalf("a mismatch must fail the instrument: %+v", c)
	}
	c, _ = bifrostMCPWitness(nil, newRunIndex([]schemas.SealedRun{mcpRun("B", "t1", zero, 1), mcpRun("B", "t2", schemas.Unknown("log incomplete", schemas.EvidenceGateway), 1)}))
	if c.Status != schemas.InstrumentUnknown {
		t.Fatalf("an unread log must leave it unknown: %+v", c)
	}
}

func TestBifrostMeters(t *testing.T) {
	idx := newRunIndex([]schemas.SealedRun{mcpRun("A", "t1", schemas.NotApplicable(), 1), mcpRun("B", "t1", schemas.Observed(0, schemas.EvidenceBoundary), 3)})
	defs := withBifrostFamily(idx, nil)
	if len(defs) != 3 || defs[0].name != MeterGatewayOverheadMedian || defs[2].name != MeterBifrostMCPMismatch {
		t.Fatalf("meters = %+v", defs)
	}
	if v, _ := defs[0].compute(idx.armRuns("", "B")).Float(); v != 3 {
		t.Fatalf("overhead median of B = %v", v)
	}
	old := newRunIndex([]schemas.SealedRun{{Slot: schemas.Slot{Arm: "A", Task: "t1", Condition: "clean", Counted: true}}})
	if got := withBifrostFamily(old, nil); len(got) != 0 {
		t.Fatalf("runs sealed before B2 gained meters: %+v", got)
	}
}

// The frozen manifest, not an optional metric, decides that the MCP witness
// check exists (audit 1-C-10).
func TestBifrostMCPWitnessDeclaredArm(t *testing.T) {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{
		"B": {Config: schemas.AgentConfig{Gateway: schemas.GatewayPolicy{Bifrost: &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{}}}}},
	}}
	run := schemas.SealedRun{Slot: schemas.Slot{Arm: "B", Task: "t1", Condition: "clean", Counted: true}, Metrics: map[string]schemas.Value{}}
	if _, ok := bifrostMCPWitness(nil, newRunIndex([]schemas.SealedRun{run})); ok {
		t.Fatal("undeclared arm without the metric: no instrument")
	}
	c, ok := bifrostMCPWitness(m, newRunIndex([]schemas.SealedRun{run}))
	if !ok || c.Status != schemas.InstrumentUnknown {
		t.Fatalf("declared arm without the metric must leave the check unknown: ok=%v %+v", ok, c)
	}
}

// The naive scoreboard counts verified_success passes whatever the primary
// metric: 10 seconds is not a pass (audit 1-C-07).
func TestNaiveScoreboardUsesVerifiedSuccess(t *testing.T) {
	run := func(arm string, wall, ok float64) schemas.SealedRun {
		return schemas.SealedRun{Slot: schemas.Slot{Arm: arm, Task: "t1", Condition: "clean", Counted: true},
			Metrics: map[string]schemas.Value{"wall_time_s": schemas.Observed(wall, schemas.EvidenceNative), MetricVerifiedSuccess: schemas.Observed(ok, schemas.EvidenceNative)}}
	}
	got := naiveScoreboard(newRunIndex([]schemas.SealedRun{run("A", 10, 0), run("B", 1, 1)}), schemas.Endpoint{Condition: "clean", Metric: "wall_time_s"})
	if got["A"].Num != 0 || got["B"].Num != 1 {
		t.Fatalf("naive = %+v, want A 0 passes and B 1", got)
	}
}
