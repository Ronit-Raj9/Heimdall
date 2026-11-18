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
	if _, ok := bifrostMCPWitness(newRunIndex([]schemas.SealedRun{mcpRun("A", "t1", na, 1), mcpRun("B", "t1", na, 1)})); ok {
		t.Fatal("no run served MCP through Bifrost: the instrument must not appear")
	}
	c, ok := bifrostMCPWitness(newRunIndex([]schemas.SealedRun{mcpRun("A", "t1", na, 1), mcpRun("B", "t1", zero, 1), mcpRun("B", "t2", zero, 1)}))
	if !ok || c.Status != schemas.InstrumentPass || c.PerArm["B"].Den != 2 {
		t.Fatalf("agreeing witnesses: %+v", c)
	}
	c, _ = bifrostMCPWitness(newRunIndex([]schemas.SealedRun{mcpRun("B", "t1", zero, 1), mcpRun("B", "t2", two, 1)}))
	if c.Status != schemas.InstrumentFail || c.PerArm["B"].Num != 1 {
		t.Fatalf("a mismatch must fail the instrument: %+v", c)
	}
	c, _ = bifrostMCPWitness(newRunIndex([]schemas.SealedRun{mcpRun("B", "t1", zero, 1), mcpRun("B", "t2", schemas.Unknown("log incomplete", schemas.EvidenceGateway), 1)}))
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
