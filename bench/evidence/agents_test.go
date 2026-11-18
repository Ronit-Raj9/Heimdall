package evidence

import (
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var agentT0 = time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)

func agentAt(s float64) time.Time { return agentT0.Add(time.Duration(s * float64(time.Second))) }

func agentMS(n int64) *int64 { return &n }

func agentsByID(tree *schemas.AgentTree) map[string]schemas.AgentNode {
	out := map[string]schemas.AgentNode{}
	for _, a := range tree.Agents {
		out[a.ID] = a
	}
	return out
}

func edgesOf(tree *schemas.AgentTree, kind string) []schemas.AgentEdge {
	var out []schemas.AgentEdge
	for _, e := range tree.Edges {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// TestBuildAgentsSingleAgent: a run with one agent, or none named, gets no
// tree and its timeline is left untouched.
func TestBuildAgentsSingleAgent(t *testing.T) {
	for _, tc := range []struct {
		name string
		ev   *schemas.Evidence
	}{
		{"plain run", &schemas.Evidence{Requests: &schemas.RequestLog{Requests: []schemas.GatewayRequest{{ID: "g1", Kind: "message", Timestamp: agentAt(1)}}}}},
		{"one header label", &schemas.Evidence{Requests: &schemas.RequestLog{Requests: []schemas.GatewayRequest{{ID: "g1", Kind: "message", AgentLabel: "main", Timestamp: agentAt(1)}}}}},
		{"nil", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []schemas.StepRecord{{Seq: 1, Source: schemas.StepSourceGateway, RequestID: "g1"}}
			if tree := BuildAgents(tc.ev, steps, nil); tree != nil {
				t.Fatalf("tree = %+v, want nil", tree)
			}
			if steps[0].AgentID != "" || steps[0].AgentSource != "" {
				t.Errorf("step touched: %+v", steps[0])
			}
		})
	}
}

// TestBuildAgentsTeam: members are proven by their keys, the lead's run key
// rows are the lead's, tool proxy rows and messages carry their routes.
func TestBuildAgentsTeam(t *testing.T) {
	zero, one := 0, 1
	team := &schemas.TeamRecord{
		Members: []schemas.TeamMember{{ID: "lead", Role: "lead", BudgetUSD: 1}, {ID: "worker-a", Role: "worker"}},
		Keys:    []schemas.MemberKey{{AgentID: "lead", ID: "vk-run"}, {AgentID: "worker-a", ID: "vk-a", BudgetUSD: 0.5}},
		Results: []schemas.MemberResult{
			{AgentID: "lead", Container: "bench-r-m-lead", Exec: &schemas.ExecResult{Started: agentAt(0), Ended: agentAt(10), ExitCode: zero}},
			{AgentID: "worker-a", Exec: &schemas.ExecResult{Started: agentAt(0), Ended: agentAt(8), ExitCode: one}},
		},
	}
	ev := &schemas.Evidence{
		Requests: &schemas.RequestLog{Requests: []schemas.GatewayRequest{
			{ID: "g1", Kind: "message", Timestamp: agentAt(1), AgentID: "lead"},
			{ID: "g2", Kind: "message", Timestamp: agentAt(2), AgentID: "worker-a", AgentLabel: "lead"},
			{ID: "g3", ParentID: "g2", Kind: "message", Timestamp: agentAt(2.5), AgentID: "worker-a"},
		}},
		ToolCalls: []schemas.ToolCall{{Seq: 1, Server: "mailbox", Tool: "POST /send", Started: agentAt(3), AgentID: "worker-a"}},
		Messages:  []schemas.AgentMessage{{Seq: 1, From: "worker-a", SenderAgentID: "worker-a", To: "lead", At: agentAt(3), BodySHA256: "abc", Size: 4}},
	}
	steps := []schemas.StepRecord{
		{Seq: 1, TS: agentAt(1), Source: schemas.StepSourceGateway, Kind: schemas.StepModelTurn, RequestID: "g1"},
		{Seq: 2, TS: agentAt(2), Source: schemas.StepSourceGateway, Kind: schemas.StepModelTurn, RequestID: "g2"},
		{Seq: 3, TS: agentAt(2.5), Source: schemas.StepSourceGateway, Kind: schemas.StepModelAttempt, RequestID: "g3"},
		{Seq: 4, TS: agentAt(3), Source: schemas.StepSourceToolProxy, ToolName: "POST /send"},
		{Seq: 5, TS: agentAt(4), Source: schemas.StepSourceFS, Kind: schemas.StepFileChange},
		{Seq: 6, TS: agentAt(4), Source: schemas.StepSourceFS, Kind: schemas.StepFileChange, WitnessOf: 4},
	}
	tree := BuildAgents(ev, steps, team)
	if tree == nil || tree.Shape != schemas.AgentShapeTeam {
		t.Fatalf("tree = %+v", tree)
	}
	a := agentsByID(tree)
	if len(a) != 2 || a["lead"].Source != schemas.AgentSourceGatewayKey || a["lead"].Strength != schemas.AgentStrengthProven ||
		a["lead"].KeyID != "vk-run" || a["lead"].Exit != "completed" || a["worker-a"].Exit != "exit code 1" || a["worker-a"].BudgetUSD != 0.5 {
		t.Errorf("agents = %+v", a)
	}
	want := []string{"lead", "worker-a", "worker-a", "worker-a", "", "worker-a"}
	for i, s := range steps {
		if s.AgentID != want[i] {
			t.Errorf("step %d agent = %q, want %q", s.Seq, s.AgentID, want[i])
		}
	}
	if steps[1].AgentSource != schemas.AgentSourceGatewayKey {
		t.Errorf("the key wins over a declared label: %+v", steps[1])
	}
	if c := tree.Coverage; c.Steps != 5 || c.Attributed != 4 || c.Unknown != 1 || c.ByStrength[schemas.AgentStrengthProven] != 4 {
		t.Errorf("coverage = %+v", c)
	}
	if m := edgesOf(tree, schemas.AgentEdgeMessage); len(m) != 1 || m[0].Strength != schemas.AgentStrengthProven || m[0].To != "lead" {
		t.Errorf("message edges = %+v", m)
	}
	if len(tree.Notes) != 1 {
		t.Errorf("notes = %v, want the key/label mismatch", tree.Notes)
	}
}

// TestBuildAgentsHeader: declared labels on the gateway rows of one key make
// an in-process tree; a transfer_to_ tool call is a handoff.
func TestBuildAgentsHeader(t *testing.T) {
	ev := &schemas.Evidence{Requests: &schemas.RequestLog{Requests: []schemas.GatewayRequest{
		{ID: "g1", Kind: "message", Timestamp: agentAt(1), AgentLabel: "triage", ToolCalls: []schemas.ToolCallRef{{ID: "c1", Name: "transfer_to_billing"}}},
		{ID: "g2", Kind: "message", Timestamp: agentAt(2), AgentLabel: "billing"},
	}}}
	steps := []schemas.StepRecord{
		{Seq: 1, TS: agentAt(1), Source: schemas.StepSourceGateway, RequestID: "g1"},
		{Seq: 2, TS: agentAt(2), Source: schemas.StepSourceGateway, RequestID: "g2"},
	}
	tree := BuildAgents(ev, steps, nil)
	if tree == nil || tree.Shape != schemas.AgentShapeInProcess || len(tree.Agents) != 2 {
		t.Fatalf("tree = %+v", tree)
	}
	if steps[0].AgentID != "triage" || steps[1].AgentSource != schemas.AgentSourceHeader {
		t.Errorf("steps = %+v", steps)
	}
	if h := edgesOf(tree, schemas.AgentEdgeHandoff); len(h) != 1 || h[0].From != "triage" || h[0].To != "billing" || h[0].Strength != schemas.AgentStrengthDeclared {
		t.Errorf("handoffs = %+v", h)
	}
}

// TestBuildAgentsTranscript: a Claude Code Task call spawns a subagent whose
// stream lines carry its id; the call's result returns it.
func TestBuildAgentsTranscript(t *testing.T) {
	sub := "task:toolu_1"
	ev := &schemas.Evidence{Events: []schemas.Event{
		{Source: schemas.SourceAgentStream, Type: schemas.EventToolCall, TS: agentAt(1), ToolCallID: "toolu_1", ToolName: "Agent", AgentTarget: sub, AgentName: "general-purpose"},
		{Source: schemas.SourceAgentStream, Type: schemas.EventModelResponse, TS: agentAt(2), AgentID: sub, ModelServed: "claude-x"},
		{Source: schemas.SourceAgentStream, Type: schemas.EventToolCall, TS: agentAt(3), ToolCallID: "toolu_2", ToolName: "Bash", AgentID: sub},
		{Source: schemas.SourceAgentStream, Type: schemas.EventToolResult, TS: agentAt(4), ToolCallID: "toolu_2", AgentID: sub},
		{Source: schemas.SourceAgentStream, Type: schemas.EventToolResult, TS: agentAt(5), ToolCallID: "toolu_1"},
	}}
	steps := []schemas.StepRecord{
		{Seq: 1, TS: agentAt(0.5), Source: schemas.StepSourceGateway, RequestID: "g0"},
		{Seq: 2, TS: agentAt(3), Source: schemas.StepSourceGateway, Kind: schemas.StepToolRequest, ToolCallID: "toolu_2"},
		{Seq: 3, TS: agentAt(4), Source: schemas.StepSourceAgent, ToolCallID: "toolu_2", WitnessOf: 2},
		{Seq: 4, TS: agentAt(3.5), Source: schemas.StepSourceProcess, Kind: schemas.StepCommand},
		{Seq: 5, TS: agentAt(6), Source: schemas.StepSourceGateway, RequestID: "g9"},
	}
	tree := BuildAgents(ev, steps, nil)
	if tree == nil {
		t.Fatal("no tree")
	}
	a := agentsByID(tree)
	s := a[sub]
	if s.ParentID != schemas.AgentRoot || s.Depth != 1 || s.SpawnToolCallID != "toolu_1" || s.Name != "general-purpose" || s.Model != "claude-x" || s.Exit != "completed" {
		t.Errorf("subagent = %+v", s)
	}
	want := []string{schemas.AgentRoot, sub, sub, sub, schemas.AgentRoot}
	for i, st := range steps {
		if st.AgentID != want[i] || st.AgentSource != schemas.AgentSourceTranscript {
			t.Errorf("step %d = %q %q, want %q transcript", st.Seq, st.AgentID, st.AgentSource, want[i])
		}
	}
	if len(edgesOf(tree, schemas.AgentEdgeSpawn)) != 1 || len(edgesOf(tree, schemas.AgentEdgeReturn)) != 1 {
		t.Errorf("edges = %+v", tree.Edges)
	}
}

// TestBuildAgentsReported: OTLP agent spans nest; native subagent events and
// handoffs build the tree; overlapping siblings leave a row unknown.
func TestBuildAgentsReported(t *testing.T) {
	t.Run("otlp", func(t *testing.T) {
		ev := &schemas.Evidence{Events: []schemas.Event{
			{EventID: "e1", Source: schemas.SourceAgentReported, Channel: schemas.ReportChannelOTLP, ReportedType: "agent", ReportedID: "s1", AgentID: "triage", TS: agentAt(0), DurationMS: agentMS(10000)},
			{EventID: "e2", ParentEventID: "e1", Source: schemas.SourceAgentReported, Channel: schemas.ReportChannelOTLP, ReportedType: "subagent_start", ReportedID: "s2", AgentID: "search", AgentRole: "worker", TS: agentAt(1), DurationMS: agentMS(3000)},
			{EventID: "e3", ParentEventID: "e2", Source: schemas.SourceAgentReported, Channel: schemas.ReportChannelOTLP, ReportedType: "tool_call", ReportedID: "s3", TS: agentAt(2)},
		}}
		steps := []schemas.StepRecord{
			{Seq: 1, TS: agentAt(2), Source: schemas.StepSourceReported, Channel: schemas.ReportChannelOTLP, ReportID: "s2"},
			{Seq: 2, TS: agentAt(2), Source: schemas.StepSourceReported, Channel: schemas.ReportChannelOTLP, ReportID: "s3", ParentSeq: 1},
			{Seq: 3, TS: agentAt(2.5), Source: schemas.StepSourceGateway, RequestID: "g1"},
		}
		tree := BuildAgents(ev, steps, nil)
		a := agentsByID(tree)
		if a["search"].ParentID != "triage" || a["search"].Depth != 1 || a["triage"].Depth != 0 || a["search"].Source != schemas.AgentSourceOTLP || a["search"].Role != "worker" {
			t.Errorf("agents = %+v", a)
		}
		for _, s := range steps {
			if s.AgentID != "search" || s.AgentSource != schemas.AgentSourceOTLP {
				t.Errorf("step %d = %q %q", s.Seq, s.AgentID, s.AgentSource)
			}
		}
	})
	t.Run("native, overlapping workers", func(t *testing.T) {
		ev := &schemas.Evidence{Events: []schemas.Event{
			{EventID: "e1", Source: schemas.SourceAgentReported, Channel: "stdout", ReportedType: "subagent_start", ReportedID: "a", AgentID: "worker-a", TS: agentAt(1)},
			{EventID: "e2", Source: schemas.SourceAgentReported, Channel: "stdout", ReportedType: "subagent_start", ReportedID: "b", AgentID: "worker-b", TS: agentAt(1.5)},
			{EventID: "e3", Source: schemas.SourceAgentReported, Channel: "stdout", ReportedType: "subagent_end", AgentID: "worker-a", TS: agentAt(4)},
			{EventID: "e4", Source: schemas.SourceAgentReported, Channel: "stdout", ReportedType: "subagent_end", ReportedParent: "b", TS: agentAt(5), IsError: true},
			{EventID: "e5", Source: schemas.SourceAgentReported, Channel: "stdout", ReportedType: "handoff", AgentID: "worker-a", AgentTarget: "reviewer", TS: agentAt(6)},
		}}
		steps := []schemas.StepRecord{
			{Seq: 1, TS: agentAt(2), Source: schemas.StepSourceGateway, RequestID: "g1"},
			{Seq: 2, TS: agentAt(4.5), Source: schemas.StepSourceGateway, RequestID: "g2"},
			{Seq: 3, TS: agentAt(9), Source: schemas.StepSourceGateway, RequestID: "g3"},
		}
		tree := BuildAgents(ev, steps, nil)
		a := agentsByID(tree)
		if a["worker-a"].ParentID != schemas.AgentRoot || a["worker-b"].Exit != "error" || a["worker-a"].Exit != "completed" {
			t.Errorf("agents = %+v", a)
		}
		want := []string{"", "worker-b", schemas.AgentRoot}
		for i, s := range steps {
			if s.AgentID != want[i] {
				t.Errorf("step %d = %q, want %q", s.Seq, s.AgentID, want[i])
			}
		}
		if h := edgesOf(tree, schemas.AgentEdgeHandoff); len(h) != 1 || h[0].From != "worker-a" || h[0].To != "reviewer" {
			t.Errorf("handoffs = %+v", h)
		}
		if len(edgesOf(tree, schemas.AgentEdgeReturn)) != 2 {
			t.Errorf("returns = %+v", tree.Edges)
		}
	})
}

// TestBuildAgentsParentCycle: a reported parent cycle is cut, never looped.
func TestBuildAgentsParentCycle(t *testing.T) {
	b := &agentBuilder{nodes: map[string]*schemas.AgentNode{}, avail: map[string]bool{}}
	b.node("a", schemas.AgentSourceSelfReport).ParentID = "b"
	b.node("b", schemas.AgentSourceSelfReport).ParentID = "a"
	b.node("c", schemas.AgentSourceSelfReport).ParentID = "ghost"
	b.depths()
	if len(b.notes) < 2 || b.nodes["c"].ParentID != "" {
		t.Errorf("notes %v, c %+v", b.notes, b.nodes["c"])
	}
	if b.nodes["a"].Depth > 1 || b.nodes["b"].Depth > 1 {
		t.Errorf("depths a=%d b=%d", b.nodes["a"].Depth, b.nodes["b"].Depth)
	}
}

// TestBuildAgentsMessageEdges: a reported message names its receiver; a
// mailbox message whose fields say kind claim is a claim edge.
func TestBuildAgentsMessageEdges(t *testing.T) {
	ev := &schemas.Evidence{
		Events: []schemas.Event{
			{EventID: "e1", Source: schemas.SourceAgentReported, Channel: "file", ReportedType: "subagent_start", ReportedID: "s1", AgentID: "a", TS: agentAt(1)},
			{EventID: "e2", ParentEventID: "e1", Source: schemas.SourceAgentReported, Channel: "file", ReportedType: "message", AgentTarget: "b", TS: agentAt(2)},
		},
		Messages: []schemas.AgentMessage{{Seq: 1, From: "a", To: "b", Fields: map[string]string{"kind": "claim"}, At: agentAt(3)}},
	}
	tree := BuildAgents(ev, nil, nil)
	if m := edgesOf(tree, schemas.AgentEdgeMessage); len(m) != 1 || m[0].From != "a" || m[0].To != "b" || m[0].Strength != schemas.AgentStrengthReported {
		t.Errorf("message edges = %+v", m)
	}
	if c := edgesOf(tree, schemas.AgentEdgeClaim); len(c) != 1 || c[0].Strength != schemas.AgentStrengthDeclared {
		t.Errorf("claim edges = %+v", c)
	}
}
