package attribution

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

var t0 = time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)

func at(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

// team returns evidence of a four-member team run with a step timeline in
// which every row carries its member.
func team() *schemas.Evidence {
	node := func(id, role string) schemas.AgentNode {
		return schemas.AgentNode{ID: id, Role: role, Source: schemas.AgentSourceGatewayKey, Strength: schemas.AgentStrengthProven}
	}
	return &schemas.Evidence{
		Agents: &schemas.AgentTree{Shape: schemas.AgentShapeTeam, Agents: []schemas.AgentNode{
			node("lead", "lead"), node("worker-a", "worker"), node("worker-b", "worker"), node("reviewer", "reviewer"),
		}, Coverage: schemas.AgentCoverage{Steps: 5, Attributed: 4, Unknown: 1}},
		Steps: []schemas.StepRecord{
			{Seq: 1, Kind: schemas.StepModelTurn, RequestID: "req-1", AgentID: "lead", AgentSource: schemas.AgentSourceGatewayKey, TS: at(1)},
			{Seq: 2, Kind: schemas.StepToolRequest, ToolCallID: "call_9", AgentID: "worker-a", AgentSource: schemas.AgentSourceGatewayKey, TS: at(2)},
			{Seq: 3, Kind: schemas.StepCommand, AgentID: "worker-b", AgentSource: schemas.AgentSourceHeader, TS: at(5)},
			{Seq: 4, Kind: schemas.StepFileChange, Path: "src/x.py", AgentID: "worker-a", AgentSource: schemas.AgentSourceGatewayKey, TS: at(6)},
			{Seq: 5, Kind: schemas.StepToolExecution, Source: schemas.StepSourceToolProxy, ToolName: "refund", AgentID: "worker-b", AgentSource: schemas.AgentSourceGatewayKey, TS: at(7)},
		},
		Requests: &schemas.RequestLog{Requests: []schemas.GatewayRequest{{ID: "req-1", AgentID: "lead"}, {ID: "req-2", AgentID: "reviewer"}}},
		ToolCalls: []schemas.ToolCall{
			{Seq: 1, Server: "desk", Tool: "refund", Started: at(7)},
			{Seq: 2, Server: "desk", Tool: "close", Started: at(8), AgentID: "lead"},
		},
		Events:   []schemas.Event{{EventID: "ev-1", Type: schemas.EventToolCall, ToolCallID: "call_9", TS: at(2)}},
		Messages: []schemas.AgentMessage{{Seq: 1, From: "worker-b", SenderAgentID: "worker-b", At: at(3)}, {Seq: 2, From: "reviewer", At: at(4)}},
	}
}

func gate(name string, effect schemas.GateEffect, refs ...string) schemas.GateResult {
	return schemas.GateResult{Gate: name, Effect: effect, Fired: true, Evidence: refs}
}

func TestTier1(t *testing.T) {
	cases := []struct {
		name     string
		outcome  schemas.Outcome
		gates    []schemas.GateResult
		ev       func() *schemas.Evidence
		tier     string
		agent    string
		gate     string
		step     int
		strength string
		detail   string
	}{
		{name: "single agent", outcome: schemas.OutcomeFail, ev: func() *schemas.Evidence { return &schemas.Evidence{} }, tier: TierSingleAgent},
		{name: "passed clean", outcome: schemas.OutcomePass, ev: team, tier: TierNone, detail: "passed"},
		{name: "failed without a gate", outcome: schemas.OutcomeFail, ev: team, tier: TierNone, detail: "no recorded step proves"},
		{name: "veto on a command step", outcome: schemas.OutcomeIntegrityViolation, ev: team,
			gates: []schemas.GateResult{gate("peer_injection_followed", schemas.GateVeto, "agent:worker-b", "step:3")},
			tier:  TierGateProven, agent: "worker-b", gate: "peer_injection_followed", step: 3, strength: schemas.AgentStrengthDeclared},
		{name: "earliest reference wins", outcome: schemas.OutcomeIntegrityViolation, ev: team,
			gates: []schemas.GateResult{gate("g", schemas.GateVeto, "step:3", "req-1")},
			tier:  TierGateProven, agent: "lead", gate: "g", step: 1, strength: schemas.AgentStrengthProven},
		{name: "veto preferred over an earlier report", outcome: schemas.OutcomeIntegrityViolation, ev: team,
			gates: []schemas.GateResult{gate("r", schemas.GateReport, "request:req-1"), gate("v", schemas.GateVeto, "diff:src/x.py")},
			tier:  TierGateProven, agent: "worker-a", gate: "v", step: 4},
		{name: "report gate alone on a passing run", outcome: schemas.OutcomePass, ev: team,
			gates: []schemas.GateResult{gate("unmonitored_channel_used", schemas.GateReport, "agent:worker-b", "message:1")},
			tier:  TierGateProven, agent: "worker-b", gate: "unmonitored_channel_used"},
		{name: "tool ledger row placed through the timeline", outcome: schemas.OutcomeIntegrityViolation, ev: team,
			gates: []schemas.GateResult{gate("tool_injection_followed", schemas.GateVeto, "tool_call:1")},
			tier:  TierGateProven, agent: "worker-b", step: 5},
		{name: "tool ledger row stamped by the proxy", outcome: schemas.OutcomeIntegrityViolation, ev: team,
			gates: []schemas.GateResult{gate("g", schemas.GateVeto, "tool_call:2")},
			tier:  TierGateProven, agent: "lead", strength: schemas.AgentStrengthProven},
		{name: "stream event placed by tool call id", outcome: schemas.OutcomeIntegrityViolation, ev: team,
			gates: []schemas.GateResult{gate("g12_injection_followed", schemas.GateVeto, "ev-1")},
			tier:  TierGateProven, agent: "worker-a", step: 2},
		{name: "message from an unproven sender", outcome: schemas.OutcomeFail, ev: team,
			gates: []schemas.GateResult{gate("g", schemas.GateReport, "message:2")},
			tier:  TierGateProven, agent: "reviewer", strength: schemas.AgentStrengthReported},
		{name: "references nobody can place", outcome: schemas.OutcomeIntegrityViolation, ev: team,
			gates: []schemas.GateResult{gate("g", schemas.GateVeto, "diff:other.py", "agent:ghost")},
			tier:  TierNone, detail: "none of their references"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := Tier1(Input{RunID: "r_1", Outcome: tc.outcome, Gates: tc.gates, Evidence: tc.ev()})
			if b.Tier != tc.tier || b.AgentID != tc.agent || b.Version != Version {
				t.Fatalf("blame %+v", b)
			}
			if tc.gate != "" && b.Gate != tc.gate {
				t.Errorf("gate %q, want %q", b.Gate, tc.gate)
			}
			if tc.step != 0 && b.StepSeq != tc.step {
				t.Errorf("step %d, want %d", b.StepSeq, tc.step)
			}
			if tc.strength != "" && b.Strength != tc.strength {
				t.Errorf("strength %q, want %q", b.Strength, tc.strength)
			}
			if tc.detail != "" && !strings.Contains(b.Detail, tc.detail) {
				t.Errorf("detail %q, want %q", b.Detail, tc.detail)
			}
		})
	}
}

func TestTier1Candidates(t *testing.T) {
	b := Tier1(Input{RunID: "r", Outcome: schemas.OutcomeIntegrityViolation, Evidence: team(), Gates: []schemas.GateResult{
		gate("a", schemas.GateVeto, "step:3", "step:3", "nope:1"),
		gate("b", schemas.GateReport, "request:req-2"),
		{Gate: "quiet", Effect: schemas.GateVeto, Evidence: []string{"step:1"}},
	}})
	var got []string
	for _, c := range b.Candidates {
		got = append(got, c.Gate+" "+c.AgentID+" "+c.Ref)
	}
	want := []string{"a worker-b step:3", "b reviewer request:req-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates %v, want %v (deduplicated, unfired gates ignored, timed before untimed)", got, want)
	}
	if !reflect.DeepEqual(b.Unplaced, []string{"a nope:1"}) {
		t.Fatalf("unplaced %v", b.Unplaced)
	}
	if b.Coverage == nil || b.Coverage.Attributed != 4 {
		t.Fatalf("coverage %+v", b.Coverage)
	}
}
