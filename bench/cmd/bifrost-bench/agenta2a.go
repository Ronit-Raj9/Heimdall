package main

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/utils"
)

// B1: agent check's row for an a2a agent (docs/a2a-agents.md): whether
// Bifrost's agent log witnessed the check run.
const rowA2A = "a2a_witness"

// a2aRows returns the agent gateway witness row for an a2a agent, nil for
// any other agent. It is pure.
func a2aRows(in *checkInput) []checkRow {
	if in == nil || !utils.IsA2A(in.agent) {
		return nil
	}
	row := checkRow{ID: rowA2A, Title: "a2a agent: agent gateway witness"}
	a := in.ev.A2A
	if a == nil {
		return []checkRow{row.set(checkFail, "evidence.json has no agent gateway witness", "check that Bifrost's agent gateway is on (client.a2a_external_client_url) and reaches the agent's card (client.a2a_private_allowlist for a private address)")}
	}
	detail := fmt.Sprintf("registration %s; %d operation(s), %d stream event(s), %d task(s) in the agent log; %d LLM or MCP row(s) linked by agent_correlation_id; strength %s",
		a.Entry, len(a.Operations), a.StreamEvents, len(a.TaskStates), len(a.Linked), a.Strength)
	if a.Agreement != nil && a.Agreement.Note != "" {
		detail += "; " + a.Agreement.Note
	}
	switch {
	case !a.Complete:
		return []checkRow{row.set(checkFail, detail+"; "+a.Reason, "the agent log must hold every operation the launcher sent; check the gateway's logs store")}
	case len(a.Linked) == 0:
		return []checkRow{row.set(checkWarn, detail, "send x-bf-agent-correlation-id (the A2A task id) and x-bf-session-id (the context id) on the agent's model calls through Bifrost, with the x-bf-vk it receives, so they are linked to the task")}
	}
	return []checkRow{row.set(checkPass, detail, "")}
}
