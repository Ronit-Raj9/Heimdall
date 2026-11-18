package agentevents

import (
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Attribute keys a reported event can name its agent with, in the order they
// are read (docs/multi-agent.md). The GenAI conventions name the agent of an
// invoke_agent span; the plain keys are for native events; from_agent and
// to_agent are the OpenAI Agents SDK handoff span fields.
var (
	agentIDKeys     = []string{"gen_ai.agent.id", "agent.id", "agent_id", "heimdall.agent", "heimdall.agent_id"}
	agentNameKeys   = []string{"gen_ai.agent.name", "agent.name", "agent_name"}
	agentRoleKeys   = []string{"gen_ai.agent.role", "agent.role", "agent_role", "role"}
	handoffFromKeys = []string{"from_agent", "gen_ai.handoff.from", "handoff.from", "from"}
	handoffToKeys   = []string{"to_agent", "gen_ai.handoff.to", "handoff.to", "to"}
)

// agentKeys are the attribute keys spanRecord keeps for attribution.
var agentKeys = []string{"gen_ai.agent.id", "agent.id", "agent_id", "agent.name", "gen_ai.agent.role", "heimdall.agent", "from_agent", "to_agent", "gen_ai.handoff.from", "gen_ai.handoff.to"}

// HandoffToolPrefix is the name prefix of the OpenAI Agents SDK's handoff
// tools (transfer_to_<agent name>): a call to one is a handoff to that agent.
const HandoffToolPrefix = "transfer_to_"

// setAgentFields fills the event's agent fields from the record: an agent
// span or subagent_start names the agent it starts (its id attribute, else
// its name, else its own id); a handoff names its source and target; any
// other event names the agent its attributes give. Values are redacted and
// clipped like every other reported string.
func setAgentFields(e *schemas.Event, r Record, red func(string) string) {
	id := first(r.Attributes, agentIDKeys...)
	name := first(r.Attributes, agentNameKeys...)
	switch r.Type {
	case TypeAgent, TypeSubagentStart, TypeSubagentEnd:
		if name == "" {
			name = r.Name
		}
		if id == "" {
			id = name
		}
		if id == "" && r.Type != TypeSubagentEnd {
			id = r.ID
		}
	case TypeHandoff:
		if from := first(r.Attributes, handoffFromKeys...); from != "" && id == "" {
			id = from
		}
		e.AgentTarget = red(clip(first(r.Attributes, handoffToKeys...), 128))
		if e.AgentTarget == "" {
			e.AgentTarget = red(clip(handoffTarget(r.Name), 128))
		}
	case TypeMessage:
		e.AgentTarget = red(clip(first(r.Attributes, handoffToKeys...), 128))
	case TypeToolCall:
		if to, ok := strings.CutPrefix(r.Name, HandoffToolPrefix); ok && to != "" {
			e.AgentTarget = red(clip(to, 128))
		}
	}
	e.AgentID = red(clip(id, 128))
	e.AgentName = red(clip(name, 256))
	if r.Type == TypeAgent || r.Type == TypeSubagentStart {
		e.AgentRole = red(clip(first(r.Attributes, agentRoleKeys...), 64))
	}
}

// handoffTarget reads the target from a handoff span or event name such as
// "handoff to billing", "handoff -> billing" or "transfer_to_billing".
func handoffTarget(name string) string {
	n := strings.TrimSpace(name)
	if to, ok := strings.CutPrefix(n, HandoffToolPrefix); ok {
		return to
	}
	low := strings.ToLower(n)
	for _, sep := range []string{" to ", "->", ":"} {
		if i := strings.Index(low, sep); i >= 0 && strings.HasPrefix(low, "handoff") {
			return strings.TrimSpace(n[i+len(sep):])
		}
	}
	return ""
}
