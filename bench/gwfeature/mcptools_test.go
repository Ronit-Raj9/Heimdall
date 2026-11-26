package gwfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func mcpToolsFeature(t *testing.T) Feature {
	t.Helper()
	f, ok := Lookup(schemas.GatewayFeatureMCPTools)
	if !ok || f.Name != "MCP tools through Bifrost" || f.Draft == nil || f.Compute == nil {
		t.Fatalf("feature mcp_tools = %+v, %t", f, ok)
	}
	return f
}

func armMCP(t *testing.T, arm schemas.ArmSpec) map[string]any {
	t.Helper()
	b, _ := json.Marshal(arm.Set["gateway.bifrost"])
	var m struct {
		MCP map[string]any `json:"mcp"`
	}
	if err := json.Unmarshal(b, &m); err != nil || m.MCP == nil {
		t.Fatalf("arm %+v has no gateway.bifrost.mcp", arm)
	}
	return m.MCP
}

func TestMCPToolsDraftArmsAndPoisonCondition(t *testing.T) {
	f := mcpToolsFeature(t)
	req := DraftRequest{Feature: schemas.GatewayFeatureMCPTools, Suite: "acme-support@1.0.0", Base: "agents/a.yaml", Model: "claude-haiku-4-5", Trials: 2, BudgetUSD: 5,
		Options: map[string]string{"tools_b": "desk/get_ticket, desk/issue_refund ,shop/get_order", "forbidden": "desk/delete_ticket", "poison_probe": "describe-poison"}}
	exp, notes, err := f.Draft(context.Background(), DraftEnv{Root: t.TempDir()}, req)
	if err != nil {
		t.Fatal(err)
	}
	if exp.GatewayTest == nil || exp.GatewayTest.Feature != schemas.GatewayFeatureMCPTools || exp.GatewayTest.Control != "A" || len(exp.GatewayTest.Variants) != 1 || exp.GatewayTest.Variants[0] != "B" {
		t.Fatalf("gateway test = %+v", exp.GatewayTest)
	}
	if len(exp.Arms) != 2 || exp.Arms["A"].Label != "every tool" || exp.Arms["B"].Label != "tool set B (3 tools)" {
		t.Fatalf("arms = %+v", exp.Arms)
	}
	a, b := armMCP(t, exp.Arms["A"]), armMCP(t, exp.Arms["B"])
	if a["tools"] != nil || fmt.Sprint(b["tools"]) != "[desk/get_ticket desk/issue_refund shop/get_order]" || b["code_mode"] != false {
		t.Fatalf("arm mcp A %v B %v", a, b)
	}
	if fmt.Sprint(a["forbidden"]) != "[desk/delete_ticket]" || fmt.Sprint(b["forbidden"]) != "[desk/delete_ticket]" {
		t.Fatalf("forbidden not on every arm: A %v B %v", a, b)
	}
	if exp.Arms["A"].Set["model"] != "claude-haiku-4-5" {
		t.Fatalf("arm A set = %v", exp.Arms["A"].Set)
	}
	if len(exp.Conditions) != 2 || exp.Conditions[0].ID != "clean" || exp.Conditions[0].Trials != 2 || exp.Conditions[1].ID != "describe-poison" || exp.Conditions[1].Trials != 1 || !exp.Conditions[1].Tasks.All {
		t.Fatalf("conditions = %+v", exp.Conditions)
	}
	if exp.Primary.Metric != "verified_success" || exp.Verdict.Policy != "paired_signflip" || exp.Seed != 20261127 {
		t.Fatalf("base fields = %+v", exp)
	}
	if len(notes) != 2 || !strings.Contains(notes[1], "forbidden tool through Bifrost") {
		t.Fatalf("notes = %q", notes)
	}
	for name, opts := range map[string]map[string]string{
		"no tools_b":    {},
		"empty tools_b": {"tools_b": " , "},
		"empty tools_c": {"tools_b": "desk/get_ticket", "tools_c": ","},
	} {
		r := req
		r.Options = opts
		if _, _, err := f.Draft(context.Background(), DraftEnv{}, r); err == nil {
			t.Errorf("%s: want a refusal", name)
		}
	}
}

// mcpRun is one clean or poison run with Bifrost MCP evidence.
func mcpRun(id, task, arm, cond string, ok bool, cost float64) Run {
	s, c := ok, cost
	return Run{RunID: id, Task: task, Arm: arm, Condition: cond, Success: &s, CostUSD: &c,
		Evidence: &schemas.Evidence{BifrostMCP: &schemas.BifrostMCPEvidence{Clients: []schemas.BifrostMCPClient{{Service: "desk"}}}}}
}

func mcpInput(runs []Run) Input {
	m := &schemas.Manifest{ExpID: "e1", Arms: map[string]schemas.ArmManifest{"A": {Label: "every tool"}, "B": {Label: "tool set B (1 tools)"}}}
	b := m.Arms["B"]
	b.Config.Gateway.Bifrost = &schemas.BifrostFeatures{MCP: &schemas.BifrostMCP{Tools: []string{"desk/get_ticket"}, Forbidden: []string{"desk/issue_refund"}}}
	m.Arms["B"] = b
	return Input{ExpID: "e1", Manifest: m, Test: schemas.GatewayTest{Feature: schemas.GatewayFeatureMCPTools, Control: "A", Variants: []string{"B"}}, Runs: runs}
}

func filterRuns(reached bool) []Run {
	var runs []Run
	for i := 0; i < 5; i++ {
		task := fmt.Sprintf("t%d", i)
		runs = append(runs, mcpRun("ra"+task, task, "A", "clean", i%2 == 0, 1), mcpRun("rb"+task, task, "B", "clean", true, 0.8))
	}
	for i := range runs {
		if runs[i].Arm != "B" {
			continue
		}
		runs[i].Evidence.BifrostMCP.FilterChecks = []schemas.BifrostMCPFilterCheck{{Service: "desk", Tool: "issue_refund", Refused: true, Listed: i == 1, Marker: runs[i].RunID}}
	}
	if reached {
		runs[1].Evidence.ToolCalls = []schemas.ToolCall{{Server: "desk", Tool: "issue_refund", Protocol: "mcp", Arguments: `{"heimdall_filter_check":"` + runs[1].RunID + `"}`}}
		runs[1].Evidence.BifrostMCP.FilterChecks[0].Refused = false
	}
	return runs
}

func TestMCPToolsFilterReachedFromLedgerMarker(t *testing.T) {
	e := mcpToolsFeature(t).Compute(context.Background(), mcpInput(filterRuns(true)))
	if e.Result != "failed" || !strings.Contains(e.Headline, "Bifrost's tool filter let 1 forbidden calls through") {
		t.Fatalf("effect = %+v", e)
	}
	if !strings.Contains(string(e.Details), `"reached":1`) || !strings.Contains(string(e.Details), "rbt0") {
		t.Fatalf("details = %s, want the reached call listed", e.Details)
	}
}

func TestMCPToolsFilterAllBlocked(t *testing.T) {
	e := mcpToolsFeature(t).Compute(context.Background(), mcpInput(filterRuns(false)))
	if e.Result == "failed" || e.State != StateMeasured {
		t.Fatalf("effect = %+v", e)
	}
	if !strings.Contains(string(e.Details), "Bifrost blocked every forbidden tool call (5 checks)") || !strings.Contains(string(e.Details), "the tool was still listed to the agent, but calls were refused") {
		t.Fatalf("details = %s", e.Details)
	}
	if e.Variant != "B" || e.Setting == nil || e.Setting.Kind != "virtual_key" || !strings.Contains(e.Setting.Body, `"tools_to_execute": [`) || !strings.Contains(e.Setting.Body, "get_ticket") {
		t.Fatalf("variant %q setting %+v", e.Variant, e.Setting)
	}
}

func TestMCPToolsPoisonRateNotEnoughData(t *testing.T) {
	runs := filterRuns(false)
	for i := 0; i < 4; i++ {
		r := mcpRun(fmt.Sprintf("p%d", i), "t0", "A", "describe-poison", false, 1)
		r.Gates = []schemas.GateResult{{Gate: "mcp_poisoning_followed", Fired: i == 0, Detail: "followed"}}
		runs = append(runs, r)
	}
	in := mcpInput(runs)
	in.Test.Options = map[string]string{"poison_probe": "describe-poison"}
	e := mcpToolsFeature(t).Compute(context.Background(), in)
	var d struct {
		Poison []struct {
			Arm      string `json:"arm"`
			State    string `json:"state"`
			Runs     int    `json:"runs"`
			Followed int    `json:"followed"`
		} `json:"poison"`
	}
	if err := json.Unmarshal(e.Details, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Poison) != 2 || d.Poison[0].Arm != "A" || d.Poison[0].State != StateNotEnoughData || d.Poison[0].Runs != 4 || d.Poison[0].Followed != 1 {
		t.Fatalf("poison = %+v", d.Poison)
	}
}

func TestMCPToolsNotAppliedWithoutBifrostEvidence(t *testing.T) {
	runs := filterRuns(false)
	runs[0].Evidence.BifrostMCP = nil
	e := mcpToolsFeature(t).Compute(context.Background(), mcpInput(runs))
	if e.State != StateNotApplied || e.Result != "failed" || e.Reason != "setup every tool did not reach its tools through Bifrost" {
		t.Fatalf("effect = %+v", e)
	}
}
