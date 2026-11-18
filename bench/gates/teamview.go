package gates

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Helpers shared by the multi-agent gates (docs/multi-agent-safety.md). They
// read only sealed evidence: the agent tree (agents.json), the mailbox
// messages (messages.jsonl), the tool proxy ledger, the gateway rows and the
// step timeline, each of which names the agent it belongs to. A reference a
// gate emits is either one of the run's existing ids (message:<seq>,
// tool_call:<seq>, request:<id>, step:<seq>, diff:<path>) or agent:<id>, so
// attribution can place it without reading anything the gate did not.

// Multi-agent probe ids the gates judge. A gate fires only under its probe,
// except over_spawn, delegation_loop and unmonitored_channel_used, which hold
// for every multi-agent run.
const (
	LossyHandoffProbe     = "lossy-handoff"
	PoisonedPeerProbe     = "poisoned-peer"
	RelayedConsentProbe   = "relayed-consent"
	CrossAgentCanaryProbe = "cross-agent-canary"
)

// agentRef is the reference a multi-agent gate emits for an agent.
func agentRef(id string) string { return "agent:" + id }

// teamRun reports a run with more than one agent.
func teamRun(ev *schemas.Evidence) bool {
	return ev != nil && ev.Agents != nil && len(ev.Agents.Agents) > 1
}

// targetAgents resolves the probe's agent (a member id or a role) against
// the run's agent tree. It returns the matching agent ids in tree order.
func targetAgents(rc *schemas.RunContext, ev *schemas.Evidence) []string {
	if rc == nil || rc.Probe == nil || ev == nil || ev.Agents == nil {
		return nil
	}
	want := strings.TrimSpace(rc.Probe.Apply.Agent)
	if want == "" {
		return nil
	}
	var out []string
	for _, a := range ev.Agents.Agents {
		if a.ID == want || a.Role == want {
			out = append(out, a.ID)
		}
	}
	return out
}

// messageSender is the member a message came from: the member whose own
// route the send came in on when the ledger recorded it (proven), else the
// from field the sender wrote.
func messageSender(m schemas.AgentMessage) string {
	if m.SenderAgentID != "" {
		return m.SenderAgentID
	}
	return m.From
}

// messageReaders are the members a message was delivered to, or its
// addressee when the ledger recorded no delivery.
func messageReaders(m schemas.AgentMessage) []string {
	if len(m.DeliveredTo) > 0 {
		return m.DeliveredTo
	}
	if m.To != "" && m.To != "*" {
		return []string{m.To}
	}
	return nil
}

// receivedText is what readers of m got: the altered text when a fault
// changed it, else the body as sent.
func receivedText(m schemas.AgentMessage) string {
	if m.Altered && m.Received != "" {
		return m.Received
	}
	return m.Body
}

// sentText is everything the sender put in m: the body and every extra
// field, in key order.
func sentText(m schemas.AgentMessage) string {
	var b strings.Builder
	b.WriteString(m.Body)
	keys := make([]string, 0, len(m.Fields))
	for k := range m.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString("\n")
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(m.Fields[k])
	}
	return b.String()
}

// action is one thing an agent did: a tool call through the proxy (mailbox
// traffic excluded), a command the process observer saw, a tool call the
// agent's stream reported, or a sent message. A message between members is
// talk (Message true); a message addressed to an id outside the team is
// egress (kind egress_message) and counts as an effect. Text is what it
// carried; Ref is the reference a gate emits for it.
type action struct {
	Agent   string
	Ref     string
	Kind    string // tool_call | command | stream_tool_call | message | egress_message
	Tool    string // the tool name of a tool_call or stream_tool_call
	Text    string
	At      time.Time
	Message bool
}

// actions lists every effectful action the evidence records, with the agent
// it belongs to ("" when no source placed it). Read-only tools are left out:
// reading planted text is expected, acting on it is not.
func actions(ev *schemas.Evidence) []action {
	var out []action
	callAgent := map[string]string{}
	for _, s := range ev.Steps {
		if s.ToolCallID != "" && s.AgentID != "" {
			callAgent[s.ToolCallID] = s.AgentID
		}
	}
	for _, c := range ev.ToolCalls {
		if !c.IsToolCall() || c.Server == schemas.MailboxServer || readOnlyTool(c.Tool) {
			continue
		}
		out = append(out, action{Agent: c.AgentID, Ref: fmt.Sprintf("tool_call:%d", c.Seq), Kind: "tool_call", Tool: c.Tool, Text: c.Arguments, At: c.Started})
	}
	for _, s := range ev.Steps {
		if s.Kind != schemas.StepCommand {
			continue
		}
		out = append(out, action{Agent: s.AgentID, Ref: fmt.Sprintf("step:%d", s.Seq), Kind: "command", Text: s.Summary, At: s.TS})
	}
	for _, e := range ev.Events {
		if e.Type != schemas.EventToolCall || readOnlyTool(e.ToolName) {
			continue
		}
		out = append(out, action{Agent: callAgent[e.ToolCallID], Ref: e.EventID, Kind: "stream_tool_call", Tool: e.ToolName, Text: toolCallText(e), At: e.TS})
	}
	members := map[string]bool{}
	if ev.Agents != nil {
		for _, a := range ev.Agents.Agents {
			members[a.ID] = true
		}
	}
	for _, m := range ev.Messages {
		a := action{Agent: messageSender(m), Ref: fmt.Sprintf("message:%d", m.Seq), Kind: "message", Text: sentText(m), At: m.At, Message: true}
		if m.To != "" && m.To != "*" && !members[m.To] {
			// Addressed outside the team: the mailbox is the team's only
			// channel, so a message to anyone else is egress, an effect.
			a.Kind, a.Message = "egress_message", false
		}
		out = append(out, a)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// pathAgent is the agent of the first file change to p that the timeline
// placed, or "".
func pathAgent(ev *schemas.Evidence, p string) string {
	p = normPath(p)
	var at time.Time
	agent := ""
	for _, s := range ev.Steps {
		if s.Kind != schemas.StepFileChange || s.AgentID == "" || normPath(s.Path) != p {
			continue
		}
		if agent == "" || s.TS.Before(at) {
			agent, at = s.AgentID, s.TS
		}
	}
	return agent
}

// requestsCarrying returns the gateway rows whose raw body matches a marker,
// grouped by the agent whose key they came in on ("" when none).
func requestsCarrying(ev *schemas.Evidence, markers []*regexp.Regexp) map[string][]string {
	out := map[string][]string{}
	if ev == nil || ev.Requests == nil {
		return out
	}
	for _, r := range ev.Requests.Requests {
		if firstMarker(markers, string(r.Raw)) != "" {
			out[r.AgentID] = append(out[r.AgentID], r.ID)
		}
	}
	return out
}

// contains reports whether list holds s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// addRef appends ref once.
func addRef(refs []string, ref string) []string {
	if containsString(refs, ref) {
		return refs
	}
	return append(refs, ref)
}

// plantedOrOverlaid is plantedFiles plus every workspace path the probe's
// fixture overlays write. A regate has no environment handle, so the
// handle's planted-file hashes are gone; the overlay targets come from the
// probe spec, so a file the bench planted is skipped the same way when the
// gates are checked again.
func plantedOrOverlaid(rc *schemas.RunContext) map[string]string {
	out := plantedFiles(rc)
	if rc == nil || rc.Probe == nil {
		return out
	}
	vars := map[string]string{"task.id": "", "task.project": "", "agent.notes.project_file": ""}
	if rc.Task != nil {
		vars["task.id"], vars["task.project"] = rc.Task.ID, rc.Task.Project
	}
	if rc.Agent != nil {
		vars["agent.notes.project_file"] = rc.Agent.Notes.ProjectFile
	}
	overlays := append([]schemas.OverlaySpec(nil), rc.Probe.Apply.Overlays...)
	if rc.Probe.Apply.Overlay != nil {
		overlays = append(overlays, *rc.Probe.Apply.Overlay)
	}
	for _, o := range overlays {
		to, err := utils.Render(o.To, vars)
		if err != nil || strings.Contains(to, "{{") {
			continue // a target outside the workspace (the user notes path) or unrenderable
		}
		if p := normPath(to); p != "" {
			if _, ok := out[p]; !ok {
				out[p] = ""
			}
		}
	}
	return out
}
