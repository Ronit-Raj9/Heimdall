package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Agent attribution (docs/multi-agent.md). BuildAgents reads every record
// that names an agent, builds the run's agent tree, and gives each timeline
// row the agent it belongs to with the strongest source that shows it:
// a team member's gateway key, a declared header on the gateway row, the
// agent's transcript, its OpenTelemetry spans, or its native self-report.
// A row nothing places keeps no agent id. A run with fewer than two agents
// gets no tree, and its timeline is left exactly as it was.

// Reported event types BuildAgents reads (agentevents.Type* values).
const (
	reportedAgent         = "agent"
	reportedSubagentStart = "subagent_start"
	reportedSubagentEnd   = "subagent_end"
	reportedHandoff       = "handoff"
	reportedToolCall      = "tool_call"
	reportedMessage       = "message"
)

// agentBuilder collects agents and edges while the tree is built.
type agentBuilder struct {
	nodes map[string]*schemas.AgentNode
	order []string
	edges []schemas.AgentEdge
	notes []string
	avail map[string]bool
	shape string
}

// BuildAgents builds the agent tree of a run from its evidence and sets
// AgentID and AgentSource on the rows of steps in place. team is the run's
// team.json (nil for a run that is not a team). It returns nil, and leaves
// steps untouched, when the run has fewer than two agents. It is pure.
func BuildAgents(ev *schemas.Evidence, steps []schemas.StepRecord, team *schemas.TeamRecord) *schemas.AgentTree {
	if ev == nil {
		return nil
	}
	b := &agentBuilder{nodes: map[string]*schemas.AgentNode{}, avail: map[string]bool{}}
	b.addTeam(team)
	b.addGateway(ev.Requests)
	b.addTranscript(ev.Events)
	b.addReported(ev.Events)
	b.addMessages(ev.Messages)
	if len(b.nodes) < 2 {
		return nil
	}
	if b.shape == "" {
		b.shape = schemas.AgentShapeInProcess
	}
	b.depths()
	cov := b.attribute(ev, steps)
	b.rootTimes(steps)
	tree := &schemas.AgentTree{Version: schemas.AgentTreeVersion, Shape: b.shape, Edges: b.edges, Coverage: cov, Notes: b.notes}
	for _, id := range b.order {
		tree.Agents = append(tree.Agents, *b.nodes[id])
	}
	sort.SliceStable(tree.Edges, func(i, j int) bool { return tree.Edges[i].At.Before(tree.Edges[j].At) })
	return tree
}

// ReadTeamRecord reads team.json from a run folder; a run without one has
// none and no error.
func ReadTeamRecord(dir string) (*schemas.TeamRecord, error) {
	if dir == "" {
		return nil, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, schemas.TeamFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", schemas.TeamFile, err)
	}
	var t schemas.TeamRecord
	if err := json.Unmarshal(b, &t); err != nil {
		return nil, fmt.Errorf("read %s: %w", schemas.TeamFile, err)
	}
	return &t, nil
}

// node returns the agent with id, creating it, and records that source saw
// it; the strongest source becomes the node's own.
func (b *agentBuilder) node(id, source string) *schemas.AgentNode {
	n, ok := b.nodes[id]
	if !ok {
		n = &schemas.AgentNode{ID: id, Source: source, Strength: schemas.AgentSourceStrength(source)}
		b.nodes[id] = n
		b.order = append(b.order, id)
	}
	if schemas.AgentSourceRank(source) < schemas.AgentSourceRank(n.Source) {
		n.Source, n.Strength = source, schemas.AgentSourceStrength(source)
	}
	if !slices.Contains(n.Sources, source) {
		n.Sources = append(n.Sources, source)
		slices.SortFunc(n.Sources, func(a, c string) int { return schemas.AgentSourceRank(a) - schemas.AgentSourceRank(c) })
	}
	b.avail[source] = true
	return n
}

// span widens the node's known lifetime to include t.
func span(n *schemas.AgentNode, start, end time.Time) {
	if !start.IsZero() && (n.Started.IsZero() || start.Before(n.Started)) {
		n.Started = start
	}
	if end.IsZero() {
		end = start
	}
	if !end.IsZero() && end.After(n.Ended) {
		n.Ended = end
	}
}

// addTeam adds one agent per team member, proven by the virtual key the
// bench minted for its container.
func (b *agentBuilder) addTeam(team *schemas.TeamRecord) {
	if team == nil || len(team.Members) == 0 {
		return
	}
	b.shape = schemas.AgentShapeTeam
	keys := map[string]schemas.MemberKey{}
	for _, k := range team.Keys {
		keys[k.AgentID] = k
	}
	results := map[string]schemas.MemberResult{}
	for _, r := range team.Results {
		results[r.AgentID] = r
	}
	for _, m := range team.Members {
		n := b.node(m.ID, schemas.AgentSourceGatewayKey)
		n.Role, n.Name, n.Model = m.Role, m.ID, m.Model
		if n.Model == "" && m.Config != nil {
			n.Model = m.Config.Model
		}
		k := keys[m.ID]
		n.KeyID, n.BudgetUSD = k.ID, m.BudgetUSD
		if n.BudgetUSD == 0 {
			n.BudgetUSD = k.BudgetUSD
		}
		r, ok := results[m.ID]
		if !ok {
			continue
		}
		n.Container = r.Container
		switch {
		case r.Exec != nil:
			n.Started, n.Ended = r.Exec.Started, r.Exec.Ended
			code := r.Exec.ExitCode
			n.ExitCode = &code
			n.Exit = exitReason(r.Exec)
		case r.Error != "":
			n.Exit = "error: " + clip(r.Error, 200)
		}
	}
}

func exitReason(x *schemas.ExecResult) string {
	switch {
	case x.TimedOut:
		return "timeout"
	case x.ExitCode == 0:
		return "completed"
	default:
		return fmt.Sprintf("exit code %d", x.ExitCode)
	}
}

// addGateway adds the agents the gateway rows name: a member key (proven)
// or a declared header label. A requested call of an OpenAI Agents SDK
// handoff tool (transfer_to_<agent>) is a handoff edge.
func (b *agentBuilder) addGateway(log *schemas.RequestLog) {
	if log == nil {
		return
	}
	for _, r := range log.Requests {
		id, src := gatewayAgent(r)
		if id == "" {
			continue
		}
		if src == schemas.AgentSourceGatewayKey {
			b.shape = schemas.AgentShapeTeam // member keys exist only in team runs
		}
		n := b.node(id, src)
		end := r.Timestamp.Add(time.Duration(r.LatencyMS * float64(time.Millisecond)))
		if n.Source != schemas.AgentSourceGatewayKey || n.Started.IsZero() {
			span(n, r.Timestamp, end)
		}
		if n.Model == "" {
			n.Model = r.ModelServed
		}
		if r.AgentID != "" && r.AgentLabel != "" && r.AgentLabel != r.AgentID {
			b.note(fmt.Sprintf("gateway row %s came in on %s's key but declared agent %q", r.ID, r.AgentID, r.AgentLabel))
		}
		for _, tc := range r.ToolCalls {
			if to, ok := strings.CutPrefix(tc.Name, handoffToolPrefix); ok && to != "" {
				b.node(to, src)
				b.edge(schemas.AgentEdge{Kind: schemas.AgentEdgeHandoff, From: id, To: to, At: end, Source: src, Ref: tc.ID})
			}
		}
	}
}

// handoffToolPrefix names the OpenAI Agents SDK's handoff tools.
const handoffToolPrefix = "transfer_to_"

// gatewayAgent is the agent a gateway row names and how: its member key,
// else its declared label.
func gatewayAgent(r schemas.GatewayRequest) (string, string) {
	switch {
	case r.AgentID != "":
		return r.AgentID, schemas.AgentSourceGatewayKey
	case r.AgentLabel != "":
		return r.AgentLabel, schemas.AgentSourceHeader
	}
	return "", ""
}

// addTranscript adds the subagents the agent's own stream shows: a spawn
// call (Claude Code Task or Agent) names the subagent, and every line the
// subagent wrote carries its id. The main agent is the root.
func (b *agentBuilder) addTranscript(events []schemas.Event) {
	results := map[string]time.Time{}
	for _, e := range events {
		if e.Source == schemas.SourceAgentStream && e.Type == schemas.EventToolResult && e.ToolCallID != "" {
			results[e.ToolCallID] = e.TS
		}
	}
	for _, e := range events {
		if e.Source != schemas.SourceAgentStream {
			continue
		}
		if e.AgentTarget != "" {
			root := b.node(schemas.AgentRoot, schemas.AgentSourceTranscript)
			parent := e.AgentID
			if parent == "" {
				parent = root.ID
			}
			n := b.node(e.AgentTarget, schemas.AgentSourceTranscript)
			if n.ParentID == "" {
				n.ParentID, n.SpawnToolCallID, n.Role = parent, e.ToolCallID, schemas.AgentRoleSubagent
			}
			if n.Name == "" {
				n.Name = e.AgentName
			}
			span(n, e.TS, time.Time{})
			b.edge(schemas.AgentEdge{Kind: schemas.AgentEdgeSpawn, From: parent, To: n.ID, At: e.TS, Source: schemas.AgentSourceTranscript, Ref: e.ToolCallID})
			// A Task call's result is the subagent's answer; a Codex spawn_agent
			// call answers as soon as the thread starts, so it returns nothing.
			if at, ok := results[e.ToolCallID]; ok && e.ToolName != "spawn_agent" {
				span(n, at, at)
				n.Exit = "completed"
				b.edge(schemas.AgentEdge{Kind: schemas.AgentEdgeReturn, From: n.ID, To: parent, At: at, Source: schemas.AgentSourceTranscript, Ref: e.ToolCallID})
			}
		}
		if e.AgentID != "" {
			n := b.node(e.AgentID, schemas.AgentSourceTranscript)
			if n.ParentID == "" && n.ID != schemas.AgentRoot {
				n.ParentID = b.node(schemas.AgentRoot, schemas.AgentSourceTranscript).ID
			}
			if n.Model == "" {
				n.Model = e.ModelServed
			}
			span(n, e.TS, time.Time{})
		}
	}
}

// reportedSource is the attribution source of an agent-reported event: its
// OpenTelemetry spans, or its native self-report.
func reportedSource(e schemas.Event) string {
	if e.Channel == schemas.ReportChannelOTLP {
		return schemas.AgentSourceOTLP
	}
	return schemas.AgentSourceSelfReport
}

// addReported adds the agents the agent reported: agent spans and
// subagent_start events start one, subagent_end ends it, a handoff links two.
// Nesting follows the reported parent links: the nearest enclosing agent is
// the parent; a subagent_start with no enclosing agent is a child of the
// main agent.
func (b *agentBuilder) addReported(events []schemas.Event) {
	byID := map[string]schemas.Event{}
	for _, e := range events {
		if e.Source == schemas.SourceAgentReported {
			byID[e.EventID] = e
		}
	}
	started := map[string]string{} // reported id of a start to the agent it started
	for _, e := range events {
		if e.Source != schemas.SourceAgentReported {
			continue
		}
		src := reportedSource(e)
		switch e.ReportedType {
		case reportedAgent, reportedSubagentStart:
			if e.AgentID == "" {
				continue
			}
			n := b.node(e.AgentID, src)
			if n.Name == "" {
				n.Name = e.AgentName
			}
			if n.Role == "" {
				n.Role = e.AgentRole
			}
			if n.Model == "" {
				n.Model = e.ModelServed
			}
			end := time.Time{}
			if e.DurationMS != nil {
				end = e.TS.Add(time.Duration(*e.DurationMS) * time.Millisecond)
			}
			span(n, e.TS, end)
			started[e.ReportedID] = n.ID
			parent := enclosingAgent(e, byID)
			if parent == "" && e.ReportedType == reportedSubagentStart {
				parent = b.node(schemas.AgentRoot, src).ID
			}
			if parent != "" && parent != n.ID && n.ParentID == "" {
				n.ParentID, n.SpawnEventID = parent, e.EventID
				if n.Role == "" {
					n.Role = schemas.AgentRoleSubagent
				}
				b.edge(schemas.AgentEdge{Kind: schemas.AgentEdgeSpawn, From: parent, To: n.ID, At: e.TS, Source: src, Ref: e.EventID})
			}
		case reportedSubagentEnd:
			id := e.AgentID
			if id == "" {
				id = started[e.ReportedParent]
			}
			if id == "" {
				id = started[e.ReportedID]
			}
			n, ok := b.nodes[id]
			if !ok {
				continue
			}
			span(n, e.TS, e.TS)
			n.Exit = "completed"
			if e.IsError {
				n.Exit = "error"
			}
			if n.ParentID != "" {
				b.edge(schemas.AgentEdge{Kind: schemas.AgentEdgeReturn, From: n.ID, To: n.ParentID, At: e.TS, Source: src, Ref: e.EventID})
			}
		case reportedMessage:
			if e.AgentTarget == "" {
				continue
			}
			from := e.AgentID
			if from == "" {
				from = enclosingAgent(e, byID)
			}
			if from != "" {
				b.node(from, src)
				b.edge(schemas.AgentEdge{Kind: schemas.AgentEdgeMessage, From: from, To: e.AgentTarget, At: e.TS, Source: src, Ref: e.EventID})
			}
		case reportedHandoff, reportedToolCall:
			if e.AgentTarget == "" {
				continue
			}
			from := e.AgentID
			if from == "" {
				from = enclosingAgent(e, byID)
			}
			if from == "" {
				from = b.node(schemas.AgentRoot, src).ID
			}
			b.node(from, src)
			to := b.node(e.AgentTarget, src)
			span(to, e.TS, time.Time{})
			b.edge(schemas.AgentEdge{Kind: schemas.AgentEdgeHandoff, From: from, To: to.ID, At: e.TS, Source: src, Ref: e.EventID})
		default:
			if e.AgentID != "" {
				span(b.node(e.AgentID, src), e.TS, time.Time{})
			}
		}
	}
}

// enclosingAgent is the agent of the nearest reported ancestor that is an
// agent span, a subagent_start or names an agent.
func enclosingAgent(e schemas.Event, byID map[string]schemas.Event) string {
	seen := map[string]bool{}
	for p := e.ParentEventID; p != "" && !seen[p]; {
		seen[p] = true
		pe, ok := byID[p]
		if !ok {
			return ""
		}
		if pe.AgentID != "" && pe.ReportedType != reportedHandoff {
			return pe.AgentID
		}
		p = pe.ParentEventID
	}
	return ""
}

// addMessages adds a message edge per mailbox message: proven when the
// tool proxy knew the sender's route, declared otherwise.
func (b *agentBuilder) addMessages(msgs []schemas.AgentMessage) {
	for _, m := range msgs {
		from, src := m.SenderAgentID, schemas.AgentSourceGatewayKey
		if from == "" {
			from, src = m.From, schemas.AgentSourceHeader
		}
		if from == "" {
			continue
		}
		if _, ok := b.nodes[from]; !ok && m.SenderAgentID == "" && b.shape == schemas.AgentShapeTeam {
			b.note(fmt.Sprintf("message %d claims sender %q, which is not a team member", m.Seq, m.From))
		}
		kind := schemas.AgentEdgeMessage
		if m.Fields["kind"] == schemas.AgentEdgeClaim { // a message that claims a task (fields.kind claim)
			kind = schemas.AgentEdgeClaim
		}
		b.edge(schemas.AgentEdge{Kind: kind, From: from, To: m.To, At: m.At, PayloadSHA256: m.BodySHA256,
			Size: m.Size, Source: src, Ref: fmt.Sprintf("mailbox:%d", m.Seq)})
	}
}

func (b *agentBuilder) edge(e schemas.AgentEdge) {
	if e.Strength == "" {
		e.Strength = schemas.AgentSourceStrength(e.Source)
	}
	b.edges = append(b.edges, e)
}

func (b *agentBuilder) note(s string) {
	if len(b.notes) < 50 && !slices.Contains(b.notes, s) {
		b.notes = append(b.notes, s)
	}
}

// depths sets each agent's depth from its parent chain. A parent that is not
// an agent of the tree is dropped with a note; a cycle is cut at the agent
// that closes it.
func (b *agentBuilder) depths() {
	for _, id := range b.order {
		n := b.nodes[id]
		if n.ParentID != "" {
			if _, ok := b.nodes[n.ParentID]; !ok {
				b.note(fmt.Sprintf("agent %s names parent %s, which is not in the tree", id, n.ParentID))
				n.ParentID = ""
			}
		}
	}
	for _, id := range b.order {
		n := b.nodes[id]
		seen := map[string]bool{id: true}
		d := 0
		for p := n.ParentID; p != ""; p = b.nodes[p].ParentID {
			if seen[p] {
				b.note(fmt.Sprintf("agent %s is in a parent cycle; its parent link was cut", id))
				n.ParentID, d = "", 0
				break
			}
			seen[p] = true
			d++
		}
		n.Depth = d
	}
}

// rootTimes gives the main agent of an in-process run the lifetime of the
// whole timeline when no source recorded its own.
func (b *agentBuilder) rootTimes(steps []schemas.StepRecord) {
	root, ok := b.nodes[schemas.AgentRoot]
	if !ok || len(steps) == 0 {
		return
	}
	for _, s := range steps {
		span(root, s.TS, time.Time{})
	}
}
