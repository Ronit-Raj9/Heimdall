package agentevents

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestAgentFields: reported events name their agent from the GenAI
// attributes, the native keys and the OpenAI Agents SDK handoff fields.
func TestAgentFields(t *testing.T) {
	tests := []struct {
		name                     string
		rec                      Record
		id, agentName, role, tgt string
	}{
		{"invoke_agent span with id", Record{Type: TypeAgent, ID: "span1", Name: "triage",
			Attributes: map[string]string{"gen_ai.agent.id": "agt_1", "gen_ai.agent.name": "Triage"}}, "agt_1", "Triage", "", ""},
		{"subagent_start named only", Record{Type: TypeSubagentStart, ID: "s1", Name: "worker-a",
			Attributes: map[string]string{"agent_role": "worker"}}, "worker-a", "worker-a", "worker", ""},
		{"subagent_start bare", Record{Type: TypeSubagentStart, ID: "s9"}, "s9", "", "", ""},
		{"subagent_end bare", Record{Type: TypeSubagentEnd, ID: "s9-end"}, "", "", "", ""},
		{"handoff span", Record{Type: TypeHandoff, Name: "handoff",
			Attributes: map[string]string{"from_agent": "triage", "to_agent": "billing"}}, "triage", "", "", "billing"},
		{"handoff by name", Record{Type: TypeHandoff, Name: "handoff to refunds"}, "", "", "", "refunds"},
		{"transfer tool", Record{Type: TypeToolCall, Name: "transfer_to_billing", Attributes: map[string]string{"agent_id": "triage"}}, "triage", "", "", "billing"},
		{"plain tool with agent", Record{Type: TypeToolCall, Name: "search", Attributes: map[string]string{"heimdall.agent": "w", "role": "x"}}, "w", "", "", ""},
		{"nothing", Record{Type: TypePlan}, "", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var e schemas.Event
			setAgentFields(&e, tt.rec, func(s string) string { return s })
			if e.AgentID != tt.id || e.AgentName != tt.agentName || e.AgentRole != tt.role || e.AgentTarget != tt.tgt {
				t.Errorf("got id %q name %q role %q target %q", e.AgentID, e.AgentName, e.AgentRole, e.AgentTarget)
			}
		})
	}
}

// TestAgentFieldsThroughNormalize: native lines keep their agent through
// the normalizer, and the run key never survives in an agent field.
func TestAgentFieldsThroughNormalize(t *testing.T) {
	in := `{"type":"subagent_start","id":"s1","name":"worker-a","attributes":{"agent_id":"worker-a","agent_role":"worker"}}
{"type":"tool_call","id":"t1","parent_id":"s1","name":"write","attributes":{"agent_id":"sk-bench-run-key-0123456789"}}`
	evs, bad := FromNativeJSONL([]byte(in), schemas.ReportChannelFile, opts())
	if bad != 0 || len(evs) != 2 {
		t.Fatalf("events %d bad %d", len(evs), bad)
	}
	if evs[0].AgentID != "worker-a" || evs[0].AgentRole != "worker" {
		t.Errorf("start = %+v", evs[0])
	}
	if evs[1].AgentID == "sk-bench-run-key-0123456789" {
		t.Errorf("run key not redacted from an agent id")
	}
}
