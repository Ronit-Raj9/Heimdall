package probes

import (
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestTargetSlotsA2AWorkerRole: a gateway probe naming the worker role of
// an a2a delegation member arms that member's key slot, as naming its id
// does (audit 8-B-12).
func TestTargetSlotsA2AWorkerRole(t *testing.T) {
	agent := &schemas.AgentConfig{Adapter: "a2a", A2A: &schemas.A2AAgent{Team: []schemas.A2AMember{{ID: "reviewer", Role: "worker"}}}}
	for _, want := range []string{"worker", "reviewer"} {
		rc := &schemas.RunContext{
			Agent: agent,
			Run:   &schemas.Run{RunID: "r1"},
			Probe: &schemas.ProbeSpec{ID: "p1", Apply: schemas.ProbeApply{Agent: want}},
			Key: &schemas.RunKey{KeySlot: "s0", Members: []schemas.MemberKey{
				{AgentID: "lead", KeySlot: "s0"}, {AgentID: "reviewer", KeySlot: "s1"}}},
		}
		got, err := targetSlots(rc, "s0")
		if err != nil || !slices.Equal(got, []string{"s1"}) {
			t.Errorf("apply.agent %s: slots %v, err %v; want [s1]", want, got, err)
		}
	}
}
