package metrics

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var maT0 = time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)

// maAt is maT0 plus s seconds; a negative s is the zero time (not recorded).
func maAt(s int) time.Time {
	if s < 0 {
		return time.Time{}
	}
	return maT0.Add(time.Duration(s) * time.Second)
}

// maNode builds an agent node; start or end -1 leaves the time unrecorded.
func maNode(id, role, name, parent string, depth, start, end int, source string) schemas.AgentNode {
	return schemas.AgentNode{ID: id, Role: role, Name: name, ParentID: parent, Depth: depth,
		Started: maAt(start), Ended: maAt(end), Source: source, Strength: schemas.AgentSourceStrength(source)}
}

// maTeam is a three-member team: lead 0-10 s, w1 2-6 s, w2 3-9 s, all on
// their own gateway keys, with two messages and one handoff.
func maTeam() *schemas.AgentTree {
	return &schemas.AgentTree{
		Version: schemas.AgentTreeVersion, Shape: schemas.AgentShapeTeam,
		Agents: []schemas.AgentNode{
			maNode("lead", schemas.AgentRoleLead, "", "", 0, 0, 10, schemas.AgentSourceGatewayKey),
			maNode("w1", schemas.AgentRoleWorker, "", "", 0, 2, 6, schemas.AgentSourceGatewayKey),
			maNode("w2", schemas.AgentRoleWorker, "", "", 0, 3, 9, schemas.AgentSourceGatewayKey),
		},
		Edges: []schemas.AgentEdge{
			{Kind: schemas.AgentEdgeMessage, From: "lead", To: "w1", Size: 10},
			{Kind: schemas.AgentEdgeMessage, From: "w1", To: "lead", Size: 5},
			{Kind: schemas.AgentEdgeHandoff, From: "lead", To: "w2"},
		},
		Coverage: schemas.AgentCoverage{Steps: 10, Attributed: 8,
			ByStrength: map[string]int{schemas.AgentStrengthProven: 5, schemas.AgentStrengthDeclared: 2, schemas.AgentStrengthReported: 1}},
	}
}

// maInProcess is an in-process tree known from transcripts: main spawns a
// and b with the same payload (a repeat), and a spawns c, which has a's name
// and role (a loop). No agent has times.
func maInProcess() *schemas.AgentTree {
	return &schemas.AgentTree{
		Version: schemas.AgentTreeVersion, Shape: schemas.AgentShapeInProcess,
		Agents: []schemas.AgentNode{
			maNode("main", schemas.AgentRoleLead, "main", "", 0, -1, -1, schemas.AgentSourceTranscript),
			maNode("a", schemas.AgentRoleSubagent, "explorer", "main", 1, -1, -1, schemas.AgentSourceTranscript),
			maNode("b", schemas.AgentRoleSubagent, "planner", "main", 1, -1, -1, schemas.AgentSourceTranscript),
			maNode("c", schemas.AgentRoleSubagent, "explorer", "a", 2, -1, -1, schemas.AgentSourceTranscript),
		},
		Edges: []schemas.AgentEdge{
			{Kind: schemas.AgentEdgeSpawn, From: "main", To: "a", PayloadSHA256: "p1"},
			{Kind: schemas.AgentEdgeSpawn, From: "main", To: "b", PayloadSHA256: "p1"},
			{Kind: schemas.AgentEdgeSpawn, From: "a", To: "c", PayloadSHA256: "p2"},
		},
	}
}

// withAgents returns a copy of tr with its agents replaced.
func withAgents(tr *schemas.AgentTree, agents ...schemas.AgentNode) *schemas.AgentTree {
	out := *tr
	out.Agents = agents
	return &out
}

// usd is a priced cost row.
func usd(req string, v float64) schemas.CostRow {
	return schemas.CostRow{RequestID: req, USD: schemas.Observed(v, schemas.EvidenceGateway)}
}

// maRequests are gateway rows of the team: r1 on lead's key, r2 on w1's key,
// r3 labelled w2, r4 a fallback of r3 with nothing of its own, r5 with no
// agent anywhere.
func maRequests() *schemas.RequestLog {
	tok := func(n int64) *schemas.Tokens { return &schemas.Tokens{Fresh: n, Out: n} }
	return &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
		{ID: "r1", Kind: kindMessage, AgentID: "lead", Tokens: tok(10)},
		{ID: "r2", Kind: kindMessage, AgentID: "w1", Tokens: tok(5)},
		{ID: "r3", Kind: kindMessage, AgentLabel: "w2", Tokens: tok(2)},
		{ID: "r4", ParentID: "r3", Kind: kindMessage, Tokens: tok(1)},
		{ID: "r5", Kind: kindMessage, Tokens: tok(4)},
	}}
}

// maCase is one metric on one evidence: either a number (with bounds when
// unknown) or an unknown whose reason contains unknown.
type maCase struct {
	name    string
	metric  schemas.Metric
	rc      *schemas.RunContext
	ev      *schemas.Evidence
	want    float64
	unknown string
	bounds  *[2]float64
	level   schemas.EvidenceLevel
}

func runMACases(t *testing.T, tests []maCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := tt.metric.Compute(context.Background(), tt.rc, tt.ev)
			if err != nil {
				t.Fatalf("Compute: %v", err)
			}
			checkMAValue(t, v, tt.want, tt.unknown, tt.bounds)
			if tt.level != "" && v.Evidence != tt.level {
				t.Errorf("evidence %q, want %q", v.Evidence, tt.level)
			}
		})
	}
}

func checkMAValue(t *testing.T, v schemas.Value, want float64, unknown string, bounds *[2]float64) {
	t.Helper()
	if unknown != "" || bounds != nil {
		if v.State != schemas.ValueUnknown || v.V != nil || !strings.Contains(v.Reason, unknown) {
			t.Fatalf("value %+v, want unknown with reason containing %q", v, unknown)
		}
		if bounds == nil {
			if v.Bounds != nil {
				t.Errorf("bounds %v, want none", *v.Bounds)
			}
			return
		}
		if v.Bounds == nil || !near(v.Bounds[0], bounds[0]) || !near(v.Bounds[1], bounds[1]) {
			t.Fatalf("bounds %v, want %v", v.Bounds, *bounds)
		}
		return
	}
	got, ok := v.Float()
	if !ok || !near(got, want) {
		t.Fatalf("value %+v, want observed %v", v, want)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestMultiAgentSingleAgentRunIsUnknown(t *testing.T) {
	for _, m := range Builtin() {
		ma, ok := m.(*multiAgentMetric)
		if !ok || ma.GetName() == BudgetExceeded {
			continue
		}
		t.Run(ma.GetName(), func(t *testing.T) {
			v, err := ma.Compute(context.Background(), nil, &schemas.Evidence{Steps: []schemas.StepRecord{{Kind: schemas.StepModelTurn}}})
			if err != nil || v.State != schemas.ValueUnknown || v.Reason != reasonSingleAgent {
				t.Fatalf("single-agent run: %+v %v", v, err)
			}
			if v, _ := ma.Compute(context.Background(), nil, nil); v.Reason != reasonSingleAgent {
				t.Fatalf("nil evidence: %+v", v)
			}
			if v, _ := ma.Compute(context.Background(), nil, &schemas.Evidence{Agents: &schemas.AgentTree{}}); v.Reason != reasonNoAgents {
				t.Fatalf("empty tree: %+v", v)
			}
			if d, _ := ma.ComputeDerived(context.Background(), nil, &schemas.Evidence{}); len(d) != 0 {
				t.Fatalf("derived on a single-agent run: %v", d)
			}
		})
	}
}

func TestMultiAgentTreeMetrics(t *testing.T) {
	team, inproc := &schemas.Evidence{Agents: maTeam()}, &schemas.Evidence{Agents: maInProcess()}
	serial := &schemas.Evidence{Agents: withAgents(maTeam(),
		maNode("lead", schemas.AgentRoleLead, "", "", 0, 0, 2, schemas.AgentSourceGatewayKey),
		maNode("w1", schemas.AgentRoleWorker, "", "", 0, 2, 4, schemas.AgentSourceGatewayKey),
		maNode("w2", schemas.AgentRoleWorker, "", "", 0, 4, 6, schemas.AgentSourceGatewayKey))}
	oneMissing := &schemas.Evidence{Agents: withAgents(maTeam(),
		maNode("lead", schemas.AgentRoleLead, "", "", 0, 0, 10, schemas.AgentSourceGatewayKey),
		maNode("w1", schemas.AgentRoleWorker, "", "", 0, 2, -1, schemas.AgentSourceGatewayKey))}
	backwards := &schemas.Evidence{Agents: withAgents(maTeam(),
		maNode("lead", schemas.AgentRoleLead, "", "", 0, 5, 1, schemas.AgentSourceGatewayKey),
		maNode("w1", schemas.AgentRoleWorker, "", "", 0, 2, 4, schemas.AgentSourceGatewayKey))}
	instant := &schemas.Evidence{Agents: withAgents(maTeam(),
		maNode("lead", schemas.AgentRoleLead, "", "", 0, 3, 3, schemas.AgentSourceGatewayKey),
		maNode("w1", schemas.AgentRoleWorker, "", "", 0, 3, 3, schemas.AgentSourceGatewayKey))}
	oneSpawned := &schemas.Evidence{Agents: withAgents(maInProcess(),
		maNode("main", schemas.AgentRoleLead, "main", "", 0, 0, 9, schemas.AgentSourceTranscript),
		maNode("a", schemas.AgentRoleSubagent, "explorer", "main", 1, 1, 2, schemas.AgentSourceTranscript))}
	inprocTimed := &schemas.Evidence{Agents: withAgents(maInProcess(),
		maNode("main", schemas.AgentRoleLead, "main", "", 0, 0, 9, schemas.AgentSourceTranscript),
		maNode("a", schemas.AgentRoleSubagent, "explorer", "main", 1, 1, 4, schemas.AgentSourceTranscript),
		maNode("b", schemas.AgentRoleSubagent, "planner", "main", 1, 3, 5, schemas.AgentSourceTranscript))}
	parentsOnly := &schemas.Evidence{Agents: withAgents(maInProcess(), maInProcess().Agents...)}
	parentsOnly.Agents.Edges = nil
	mailbox := &schemas.Evidence{Agents: maTeam(), Messages: []schemas.AgentMessage{
		{Seq: 1, From: "lead", To: "w1", Body: "abcd", Size: 4}, {Seq: 2, From: "w1", To: "*", Body: "abc"}, {Seq: 3, From: "w2", To: "lead", Size: 6},
	}}
	tests := []maCase{
		{name: "agents spawned: team", metric: newAgentsSpawnedMetric(), ev: team, want: 2, level: schemas.EvidenceGateway},
		{name: "agents spawned: in-process from transcripts", metric: newAgentsSpawnedMetric(), ev: inproc, want: 3, level: schemas.EvidenceNative},
		{name: "max depth: team", metric: newMaxDepthMetric(), ev: team, want: 0},
		{name: "max depth: in-process", metric: newMaxDepthMetric(), ev: inproc, want: 2},
		{name: "max fan-out: team members are children of the run", metric: newMaxFanOutMetric(), ev: team, want: 3},
		{name: "max fan-out: in-process", metric: newMaxFanOutMetric(), ev: inproc, want: 2},
		{name: "handoffs", metric: newHandoffsMetric(), ev: team, want: 1},
		{name: "handoffs: none in the tree", metric: newHandoffsMetric(), ev: inproc, want: 0},
		{name: "messages from edges", metric: newMessagesMetric(), ev: team, want: 2, level: schemas.EvidenceGateway},
		{name: "message bytes from edges", metric: newMessageBytesMetric(), ev: team, want: 15},
		{name: "messages from the mailbox win over edges", metric: newMessagesMetric(), ev: mailbox, want: 3, level: schemas.EvidenceBoundary},
		{name: "message bytes from the mailbox, body length when size is missing", metric: newMessageBytesMetric(), ev: mailbox, want: 13},
		{name: "serial collapse: members overlap", metric: newSerialCollapseMetric(), ev: team, want: 0},
		{name: "serial collapse: members one after another (touching ends)", metric: newSerialCollapseMetric(), ev: serial, want: 1},
		{name: "serial collapse: in-process subagents overlap, root ignored", metric: newSerialCollapseMetric(), ev: inprocTimed, want: 0},
		{name: "serial collapse: times missing", metric: newSerialCollapseMetric(), ev: inproc, unknown: "has no start or end time"},
		{name: "serial collapse: one spawned agent", metric: newSerialCollapseMetric(), ev: oneSpawned, unknown: "1 spawned agents"},
		{name: "critical path", metric: newCriticalPathMetric(), ev: team, want: 10},
		{name: "critical path: serial team", metric: newCriticalPathMetric(), ev: serial, want: 6},
		{name: "critical path: one end missing", metric: newCriticalPathMetric(), ev: oneMissing, unknown: "agent w1 has no start or end time"},
		{name: "critical path: ended before started", metric: newCriticalPathMetric(), ev: backwards, unknown: "agent lead ended before it started"},
		{name: "parallelism", metric: newParallelismMetric(), ev: team, want: 2},
		{name: "parallelism: serial team", metric: newParallelismMetric(), ev: serial, want: 1},
		{name: "parallelism: times missing", metric: newParallelismMetric(), ev: inproc, unknown: "has no start or end time"},
		{name: "parallelism: zero span", metric: newParallelismMetric(), ev: instant, unknown: reasonZeroSpan},
		{name: "delegation loops: repeat payload and same name and role as the spawner", metric: newDelegationLoopsMetric(), ev: inproc, want: 2},
		{name: "delegation loops: parent links only find the name loop", metric: newDelegationLoopsMetric(), ev: parentsOnly, want: 1},
		{name: "delegation loops: team without spawns", metric: newDelegationLoopsMetric(), ev: team, want: 0},
		{name: "attribution coverage", metric: newAttributionCoverageMetric(), ev: team, want: 0.8},
		{name: "attribution coverage: no timeline rows", metric: newAttributionCoverageMetric(), ev: inproc, unknown: reasonNoTimelineRows},
	}
	runMACases(t, tests)
}

func TestDelegationLoopsCycles(t *testing.T) {
	tests := []struct {
		name string
		tree *schemas.AgentTree
		want int
	}{
		{name: "spawn back to an ancestor by id", tree: &schemas.AgentTree{
			Agents: []schemas.AgentNode{{ID: "x", Name: "x"}, {ID: "y", Name: "y", ParentID: "x"}},
			Edges:  []schemas.AgentEdge{{Kind: schemas.AgentEdgeSpawn, From: "x", To: "y"}, {Kind: schemas.AgentEdgeSpawn, From: "y", To: "x"}},
		}, want: 1},
		{name: "same name, other role is no loop", tree: &schemas.AgentTree{
			Agents: []schemas.AgentNode{{ID: "x", Name: "n", Role: "lead"}, {ID: "y", Name: "n", Role: "worker", ParentID: "x"}},
		}, want: 0},
		{name: "same payload from other parents is no repeat", tree: &schemas.AgentTree{
			Agents: []schemas.AgentNode{{ID: "x"}, {ID: "y"}, {ID: "p", ParentID: "x"}, {ID: "q", ParentID: "y"}},
			Edges: []schemas.AgentEdge{{Kind: schemas.AgentEdgeSpawn, From: "x", To: "p", PayloadSHA256: "h"},
				{Kind: schemas.AgentEdgeSpawn, From: "y", To: "q", PayloadSHA256: "h"}},
		}, want: 0},
		{name: "a corrupt parent cycle ends", tree: &schemas.AgentTree{
			Agents: []schemas.AgentNode{{ID: "x", ParentID: "y"}, {ID: "y", ParentID: "x"}, {ID: "z", Name: "z"}},
			Edges:  []schemas.AgentEdge{{Kind: schemas.AgentEdgeSpawn, From: "x", To: "z"}},
		}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := delegationLoops(tt.tree); got != tt.want {
				t.Fatalf("delegationLoops = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestOrchestrationShare(t *testing.T) {
	steps := []schemas.StepRecord{{Kind: schemas.StepModelTurn, RequestID: "r3", AgentID: "w2"}}
	ev := func(cost ...schemas.CostRow) *schemas.Evidence {
		return &schemas.Evidence{Agents: maTeam(), Requests: maRequests(), Steps: steps, Cost: cost}
	}
	priced := []schemas.CostRow{usd("r1", 2), usd("r2", 1), usd("r3", 0.5), usd("r4", 0.5)}
	noLead := &schemas.Evidence{Agents: withAgents(maTeam(), maNode("w1", schemas.AgentRoleWorker, "", "", 0, 0, 1, schemas.AgentSourceGatewayKey)), Cost: priced}
	byName := &schemas.Evidence{
		Agents: withAgents(maTeam(), maNode("rt", schemas.AgentRoleRouter, "router-bot", "", 0, -1, -1, schemas.AgentSourceHeader),
			maNode("w1", schemas.AgentRoleWorker, "", "", 0, -1, -1, schemas.AgentSourceHeader)),
		Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{{ID: "q1", AgentLabel: "router-bot"}, {ID: "q2", AgentLabel: "w1"}}},
		Cost:     []schemas.CostRow{usd("q1", 1), usd("q2", 3)},
	}
	unmappedOnly := &schemas.Evidence{Agents: maTeam(), Requests: maRequests(), Cost: []schemas.CostRow{usd("r5", 1)}}
	tests := []maCase{
		{name: "lead pays half: key, label and the model turn of a fallback's root", metric: newOrchestrationShareMetric(), ev: ev(priced...), want: 0.5, level: schemas.EvidenceGateway},
		{name: "a row with no agent bounds the share", metric: newOrchestrationShareMetric(), ev: ev(append(priced, usd("r5", 1))...), unknown: "1 of 5 cost rows map to no agent", bounds: &[2]float64{0.4, 0.6}},
		{name: "a not-applicable row adds nothing", metric: newOrchestrationShareMetric(), ev: ev(append(priced, schemas.CostRow{RequestID: "r5", USD: schemas.NotApplicable()})...), want: 0.5},
		{name: "unpriced row", metric: newOrchestrationShareMetric(), ev: ev(usd("r1", 1), schemas.CostRow{RequestID: "r2", USD: schemas.Unknown("no price", schemas.EvidenceGateway)}), unknown: "1 of 2 cost rows unpriced"},
		{name: "label names an agent by its unique name", metric: newOrchestrationShareMetric(), ev: byName, want: 0.25},
		{name: "no orchestration role", metric: newOrchestrationShareMetric(), ev: noLead, unknown: reasonNoOrchestrator},
		{name: "no cost rows", metric: newOrchestrationShareMetric(), ev: ev(), unknown: "no cost rows for 5 gateway requests"},
		{name: "no row maps to an agent", metric: newOrchestrationShareMetric(), ev: unmappedOnly, unknown: reasonNoCostMapped},
		{name: "zero cost", metric: newOrchestrationShareMetric(), ev: ev(usd("r1", 0)), unknown: reasonZeroCost},
	}
	runMACases(t, tests)
}

func TestPerAgentDerived(t *testing.T) {
	steps := []schemas.StepRecord{
		{Kind: schemas.StepModelTurn, RequestID: "r1", AgentID: "lead"},
		{Kind: schemas.StepModelTurn, RequestID: "r2", AgentID: "w1"},
		{Kind: schemas.StepModelTurn, RequestID: "r3", AgentID: "w2"},
		{Kind: schemas.StepToolRequest, ToolCallID: "c1", AgentID: "w1"},
		{Kind: schemas.StepToolRequest, ToolCallID: "c2", AgentID: "w1"},
	}
	full := &schemas.Evidence{Agents: maTeam(), Requests: maRequests(), Steps: steps,
		Cost: []schemas.CostRow{usd("r1", 2), usd("r2", 1), usd("r3", 0.5), usd("r4", 0.25)}}
	full.Requests.Requests = full.Requests.Requests[:4] // r5 has no agent: drop it here
	gap := &schemas.Evidence{Agents: maTeam(), Requests: maRequests(),
		Steps: append(append([]schemas.StepRecord(nil), steps...), schemas.StepRecord{Kind: schemas.StepToolRequest, ToolCallID: "c9"}),
		Cost:  []schemas.CostRow{usd("r1", 2), usd("r5", 1)}}
	unpriced := &schemas.Evidence{Agents: maTeam(), Requests: &schemas.RequestLog{Complete: false, Requests: maRequests().Requests},
		Cost: []schemas.CostRow{usd("r1", 2), {RequestID: "r2", USD: schemas.Unknown("no price", schemas.EvidenceGateway)}}}
	type want struct {
		v       float64
		unknown string
		bounds  *[2]float64
	}
	tests := []struct {
		name   string
		ev     *schemas.Evidence
		want   map[string]want
		absent []string
	}{
		{name: "every row placed", ev: full, want: map[string]want{
			"agent_cost_usd.lead": {v: 2}, "agent_cost_usd.w1": {v: 1}, "agent_cost_usd.w2": {v: 0.75},
			"agent_tokens.lead": {v: 20}, "agent_tokens.w1": {v: 10}, "agent_tokens.w2": {v: 6},
			"agent_tool_calls.lead": {v: 0}, "agent_tool_calls.w1": {v: 2}, "agent_tool_calls.w2": {v: 0},
			"agent_model_turns.lead": {v: 1}, "agent_model_turns.w1": {v: 1}, "agent_model_turns.w2": {v: 1},
		}},
		{name: "unplaced work makes bounds", ev: gap, want: map[string]want{
			"agent_cost_usd.lead":    {unknown: "1 cost rows map to no agent", bounds: &[2]float64{2, 3}},
			"agent_cost_usd.w2":      {unknown: "map to no agent", bounds: &[2]float64{0, 1}},
			"agent_tokens.w1":        {unknown: "1 message requests map to no agent", bounds: &[2]float64{10, 18}},
			"agent_tool_calls.w1":    {unknown: "1 tool_request rows carry no agent id", bounds: &[2]float64{2, 3}},
			"agent_model_turns.lead": {v: 1},
		}},
		{name: "unpriced row, incomplete log, no timeline", ev: unpriced, want: map[string]want{
			"agent_cost_usd.lead": {v: 2}, "agent_cost_usd.w1": {unknown: "1 cost rows of agent w1 unpriced"},
		}, absent: []string{"agent_tokens.lead", "agent_tool_calls.lead", "agent_model_turns.lead"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newAgentsSpawnedMetric().ComputeDerived(context.Background(), nil, tt.ev)
			if err != nil {
				t.Fatal(err)
			}
			for name, w := range tt.want {
				v, ok := got[name]
				if !ok {
					t.Fatalf("no %s in %v", name, got)
				}
				t.Run(name, func(t *testing.T) { checkMAValue(t, v, w.v, w.unknown, w.bounds) })
			}
			for _, name := range tt.absent {
				if _, ok := got[name]; ok {
					t.Errorf("%s present: %+v", name, got[name])
				}
			}
		})
	}
}

func TestAgentKeys(t *testing.T) {
	tests := []struct {
		name string
		ids  []string
		want map[string]string
	}{
		{name: "charset", ids: []string{"Worker.1", "lead", "a b/c"}, want: map[string]string{"Worker.1": "worker_1", "lead": "lead", "a b/c": "a_b_c"}},
		{name: "collisions get a suffix in id order", ids: []string{"a_b", "a.b", "A.B"}, want: map[string]string{"A.B": "a_b", "a.b": "a_b_2", "a_b": "a_b_3"}},
		{name: "empty and long ids", ids: []string{"", strings.Repeat("x", 80)}, want: map[string]string{"": "_", strings.Repeat("x", 80): strings.Repeat("x", maxAgentKey)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr := &schemas.AgentTree{}
			for _, id := range tt.ids {
				tr.Agents = append(tr.Agents, schemas.AgentNode{ID: id})
			}
			got := agentKeys(tr)
			for id, key := range tt.want {
				if got[id] != key {
					t.Errorf("key of %q = %q, want %q", id, got[id], key)
				}
			}
		})
	}
}

func TestDuplicateWorkRatio(t *testing.T) {
	refs := &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{{ID: "r1", Kind: kindMessage, ToolCalls: []schemas.ToolCallRef{
		{ID: "c1", Name: "Edit", ArgsHash: "h1", Target: "/workspace/src/b.go"},
		{ID: "c2", Name: "Edit", ArgsHash: "h2", Paths: "src/b.go"},
		{ID: "c3", Name: "Bash", ArgsHash: "h3", Target: "go test ./..."},
		{ID: "c4", Name: "Bash", ArgsHash: "h3", Target: "go test ./..."},
	}}}}
	steps := []schemas.StepRecord{
		{Kind: schemas.StepFileChange, Path: "src/a.go", AgentID: "lead"},
		{Kind: schemas.StepFileChange, Path: "./src/a.go", AgentID: "w1"},
		{Kind: schemas.StepToolRequest, ToolCallID: "c1", AgentID: "w1"},
		{Kind: schemas.StepToolRequest, ToolCallID: "c2", ToolName: "Edit", AgentID: "w2"},
		{Kind: schemas.StepToolRequest, ToolCallID: "c3", AgentID: "lead"},
		{Kind: schemas.StepToolRequest, ToolCallID: "c4", AgentID: "w1"},
		{Kind: schemas.StepFileChange, Path: "src/c.go"}, // no agent: not counted
	}
	solo := []schemas.StepRecord{
		{Kind: schemas.StepFileChange, Path: "src/a.go", AgentID: "lead"},
		{Kind: schemas.StepFileChange, Path: "src/a.go", AgentID: "lead"},
		{Kind: schemas.StepToolRequest, ToolCallID: "c3", AgentID: "lead"},
	}
	tests := []maCase{
		// Paths src/a.go (lead, w1) and src/b.go (w1, w2) are shared; calls
		// Edit/h1, Edit/h2 are not, Bash/h3 (lead, w1) is: 3 of 5.
		{name: "shared paths and calls", metric: newDuplicateWorkRatioMetric(), ev: &schemas.Evidence{Agents: maTeam(), Requests: refs, Steps: steps}, want: 0.6},
		{name: "one agent repeating itself is no duplicate", metric: newDuplicateWorkRatioMetric(), ev: &schemas.Evidence{Agents: maTeam(), Requests: refs, Steps: solo}, want: 0},
		{name: "no attributed step", metric: newDuplicateWorkRatioMetric(), ev: &schemas.Evidence{Agents: maTeam(), Steps: []schemas.StepRecord{{Kind: schemas.StepFileChange, Path: "x"}}}, unknown: reasonNoAttributed},
		{name: "only model turns attributed", metric: newDuplicateWorkRatioMetric(), ev: &schemas.Evidence{Agents: maTeam(), Steps: []schemas.StepRecord{{Kind: schemas.StepModelTurn, AgentID: "lead"}}}, unknown: reasonNoWork},
	}
	runMACases(t, tests)
}

func TestAttributionCoverageDerived(t *testing.T) {
	noSplit := maTeam()
	noSplit.Coverage.ByStrength = nil
	none := maTeam()
	none.Coverage = schemas.AgentCoverage{Steps: 4}
	tests := []struct {
		name string
		tree *schemas.AgentTree
		want map[string]float64
	}{
		{name: "split by strength", tree: maTeam(), want: map[string]float64{
			"attribution_coverage_proven": 0.5, "attribution_coverage_declared": 0.2, "attribution_coverage_reported": 0.1}},
		{name: "attributed rows without a split", tree: noSplit},
		{name: "nothing attributed is an observed zero", tree: none, want: map[string]float64{
			"attribution_coverage_proven": 0, "attribution_coverage_declared": 0, "attribution_coverage_reported": 0}},
		{name: "no rows", tree: maInProcess()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := newAttributionCoverageMetric().ComputeDerived(context.Background(), nil, &schemas.Evidence{Agents: tt.tree})
			if err != nil || len(got) != len(tt.want) {
				t.Fatalf("derived = %v %v, want %v", got, err, tt.want)
			}
			for name, w := range tt.want {
				checkMAValue(t, got[name], w, "", nil)
			}
		})
	}
}

func TestBudgetExceeded(t *testing.T) {
	capped := func(usd float64) *schemas.RunContext {
		return &schemas.RunContext{Agent: &schemas.AgentConfig{Budgets: schemas.Budgets{USD: usd}}}
	}
	bounded := schemas.CostRow{RequestID: "x", USD: schemas.UnknownWithin("no price", schemas.EvidenceGateway, 0, 1)}
	open := schemas.CostRow{RequestID: "y", USD: schemas.Unknown("no price", schemas.EvidenceGateway)}
	cost := func(rows ...schemas.CostRow) *schemas.Evidence { return &schemas.Evidence{Cost: rows} }
	tests := []maCase{
		{name: "single agent over the cap", metric: newBudgetExceededMetric(), rc: capped(2), ev: cost(usd("a", 1.5), usd("b", 1)), want: 1, level: schemas.EvidenceGateway},
		{name: "team under the cap", metric: newBudgetExceededMetric(), rc: capped(2), ev: &schemas.Evidence{Agents: maTeam(), Cost: []schemas.CostRow{usd("a", 1.5)}}, want: 0},
		{name: "exactly the cap is not over it", metric: newBudgetExceededMetric(), rc: capped(0.3), ev: cost(usd("a", 0.1), usd("b", 0.2)), want: 0},
		{name: "bounds wholly above the cap", metric: newBudgetExceededMetric(), rc: capped(2), ev: cost(usd("a", 2.5), bounded), want: 1},
		{name: "bounds wholly at or below the cap", metric: newBudgetExceededMetric(), rc: capped(2), ev: cost(usd("a", 1), bounded), want: 0},
		{name: "bounds across the cap", metric: newBudgetExceededMetric(), rc: capped(2), ev: cost(usd("a", 1.5), bounded), unknown: "run cost unknown against the 2.00 USD cap"},
		{name: "unbounded unpriced row", metric: newBudgetExceededMetric(), rc: capped(2), ev: cost(usd("a", 1), open), unknown: "1 of 2 cost rows unpriced"},
		{name: "no cost evidence", metric: newBudgetExceededMetric(), rc: capped(2), ev: &schemas.Evidence{}, unknown: "no cost evidence"},
		{name: "no cap", metric: newBudgetExceededMetric(), rc: capped(0), ev: cost(usd("a", 5)), unknown: reasonNoCap},
		{name: "no run context", metric: newBudgetExceededMetric(), ev: cost(usd("a", 5)), unknown: reasonNoCap},
	}
	runMACases(t, tests)
}
