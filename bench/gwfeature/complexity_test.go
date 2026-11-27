package gwfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	rtStrong = "claude-sonnet-4-5"
	rtCheap  = "claude-haiku-4-5"
)

func rtReq(opts map[string]string) DraftRequest {
	o := map[string]string{"strong_model": rtStrong, "cheap_model": rtCheap}
	for k, v := range opts {
		o[k] = v
	}
	return DraftRequest{Feature: schemas.GatewayFeatureComplexityRouter, Suite: "coding-core@1.0.0", Base: "agents/custom-python.yaml", Trials: 2, BudgetUSD: 5, Options: o}
}

// rtRun is one run with one message row of the given tier, served by model.
func rtRun(id, task, arm, tier, model string, success bool, cost float64) Run {
	s, c := success, cost
	return Run{RunID: id, Task: task, Arm: arm, Condition: "clean", Success: &s, CostUSD: &c,
		Rows: []Row{{ID: id + "-1", Kind: "message", Status: "success", Provider: "anthropic", ModelServed: model, LatencyMS: 100, RawKnown: tier != "", ComplexityTier: tier}}}
}

func rtInput(runs []Run) Input {
	return Input{ExpID: "e_router", Test: schemas.GatewayTest{Feature: schemas.GatewayFeatureComplexityRouter, Control: "A", Variants: []string{"B", "C"},
		Options: map[string]string{"strong_model": rtStrong, "cheap_model": rtCheap}}, Runs: runs}
}

func rtDetails(t *testing.T, e Effect) routerDetails {
	t.Helper()
	var d routerDetails
	if err := json.Unmarshal(e.Details, &d); err != nil {
		t.Fatalf("details: %v (%s)", err, e.Details)
	}
	return d
}

func TestRouterDraftArms(t *testing.T) {
	f, ok := Lookup(schemas.GatewayFeatureComplexityRouter)
	if !ok || f.Name != "Send easy tasks to a cheaper model" {
		t.Fatalf("feature not registered: %+v", f)
	}
	req := rtReq(nil)
	req.Provider = "anthropic"
	exp, notes, err := f.Draft(context.Background(), DraftEnv{Root: t.TempDir()}, req)
	if err != nil {
		t.Fatal(err)
	}
	if exp.GatewayTest == nil || exp.GatewayTest.Feature != schemas.GatewayFeatureComplexityRouter || exp.GatewayTest.Control != "A" || strings.Join(exp.GatewayTest.Variants, ",") != "B,C" {
		t.Fatalf("gateway test: %+v", exp.GatewayTest)
	}
	if len(exp.Arms) != 3 {
		t.Fatalf("arms: %+v", exp.Arms)
	}
	want := map[string][3]string{
		"A": {rtStrong, `complexity_tier != ""`, rtStrong},
		"B": {rtCheap, `complexity_tier == "COMPLEX"`, rtStrong},
		"C": {rtCheap, `complexity_tier != ""`, rtCheap},
	}
	for id, w := range want {
		arm := exp.Arms[id]
		if arm.Set["model"] != w[0] || arm.Set["gateway.provider"] != "anthropic" {
			t.Errorf("%s: set %+v", id, arm.Set)
		}
		r, _ := arm.Set["gateway.routing"].(map[string]any)
		targets, _ := r["targets"].([]any)
		if r["strategy"] != "rule" || r["when"] != w[1] || len(targets) != 1 || targets[0].(map[string]any)["model"] != w[2] {
			t.Errorf("%s: routing %+v", id, r)
		}
	}
	if exp.Arms["A"].Label != "strong model only" || exp.Arms["B"].Label != "router on" || exp.Arms["C"].Label != "cheap model only" {
		t.Errorf("labels: %+v", exp.Arms)
	}
	if exp.Conditions[0].Trials != 2 || !exp.Conditions[0].Tasks.All || exp.Primary.Metric != "verified_success" || exp.Seed != 20261127 {
		t.Errorf("base fields: %+v", exp)
	}
	if len(notes) < 2 {
		t.Errorf("notes: %v", notes)
	}
}

func TestRouterDraftRefusesSameModel(t *testing.T) {
	f, _ := Lookup(schemas.GatewayFeatureComplexityRouter)
	for name, opts := range map[string]map[string]string{
		"same":      {"cheap_model": rtStrong},
		"when":      {"router_when": `true`},
		"tolerance": {"tolerance": "0.7"},
		"missing":   {"strong_model": ""},
	} {
		if _, _, err := f.Draft(context.Background(), DraftEnv{}, rtReq(opts)); err == nil {
			t.Errorf("%s: no refusal", name)
		}
	}
}

func TestRouterTaskTierMajorityAndTie(t *testing.T) {
	runs := []Run{
		rtRun("r1", "t1", "A", "SIMPLE", rtStrong, true, 1),
		rtRun("r2", "t1", "B", "SIMPLE", rtCheap, true, 1),
		rtRun("r3", "t1", "C", "MEDIUM", rtCheap, true, 1),
		rtRun("r4", "t2", "A", "SIMPLE", rtStrong, true, 1),
		rtRun("r5", "t2", "C", "COMPLEX", rtCheap, true, 1),
		rtRun("r6", "t3", "A", "", rtStrong, true, 1),
	}
	tiers, dis, uncl := routerTaskTiers(runs, []string{"A", "B", "C"})
	if tiers["t1"] != "SIMPLE" || tiers["t2"] != "COMPLEX" {
		t.Fatalf("tiers: %v", tiers)
	}
	if _, ok := tiers["t3"]; ok || uncl != 1 || dis != 2 {
		t.Fatalf("unclassified %d disagreements %d tiers %v", uncl, dis, tiers)
	}
}

func TestRouterNotAppliedWithoutTiers(t *testing.T) {
	var runs []Run
	for i := 0; i < 6; i++ {
		task := fmt.Sprintf("t%d", i)
		for _, arm := range []string{"A", "B", "C"} {
			runs = append(runs, rtRun(task+arm, task, arm, "", rtStrong, true, 1))
		}
	}
	e := computeRouter(context.Background(), rtInput(runs))
	if e.State != StateNotApplied || !strings.Contains(e.Reason, "no request a complexity tier") || e.Result != "failed" {
		t.Fatalf("effect: %+v", e)
	}
}

func TestRouterNotAppliedWhenRuleNeverRouted(t *testing.T) {
	var runs []Run
	for i := 0; i < 6; i++ {
		task := fmt.Sprintf("t%d", i)
		runs = append(runs, rtRun(task+"A", task, "A", "COMPLEX", rtStrong, true, 1),
			rtRun(task+"B", task, "B", "COMPLEX", rtCheap, true, 0.2),
			rtRun(task+"C", task, "C", "COMPLEX", rtCheap, true, 0.2))
	}
	e := computeRouter(context.Background(), rtInput(runs))
	if e.State != StateNotApplied || e.Reason != "The router rule matched no request: no call of the router setup went to "+rtStrong+"." {
		t.Fatalf("effect: %+v", e)
	}
}

func TestRouterPerTierNotEnoughData(t *testing.T) {
	var runs []Run
	for i := 0; i < 6; i++ {
		task := fmt.Sprintf("s%d", i)
		runs = append(runs, rtRun(task+"A", task, "A", "SIMPLE", rtStrong, true, 1),
			rtRun(task+"B", task, "B", "SIMPLE", rtCheap, true, 0.2),
			rtRun(task+"C", task, "C", "SIMPLE", rtCheap, true, 0.2))
	}
	for i := 0; i < 2; i++ {
		task := fmt.Sprintf("c%d", i)
		runs = append(runs, rtRun(task+"A", task, "A", "COMPLEX", rtStrong, true, 1),
			rtRun(task+"B", task, "B", "COMPLEX", rtStrong, true, 1),
			rtRun(task+"C", task, "C", "COMPLEX", rtCheap, false, 0.2))
	}
	e := computeRouter(context.Background(), rtInput(runs))
	d := rtDetails(t, e)
	if len(d.Tiers) != 3 || d.Tiers[0].Tier != "SIMPLE" || d.Tiers[2].Tier != "COMPLEX" {
		t.Fatalf("tiers: %+v", d.Tiers)
	}
	if d.Tiers[0].State != StateMeasured || d.Tiers[0].Tasks != 6 {
		t.Errorf("simple: %+v", d.Tiers[0])
	}
	if d.Tiers[2].State != StateNotEnoughData || d.Tiers[2].Reason != "2 of the 5 tasks needed have results on both setups" {
		t.Errorf("complex: %+v", d.Tiers[2])
	}
	if d.Tiers[1].State != StateNotEnoughData || d.Tiers[1].Tasks != 0 {
		t.Errorf("medium: %+v", d.Tiers[1])
	}
}

// rtMixed: 6 SIMPLE tasks equal on A and C, 6 COMPLEX tasks C fails.
func rtMixed() []Run {
	var runs []Run
	for i := 0; i < 6; i++ {
		task := fmt.Sprintf("s%d", i)
		runs = append(runs, rtRun(task+"A", task, "A", "SIMPLE", rtStrong, true, 1),
			rtRun(task+"B", task, "B", "SIMPLE", rtCheap, true, 0.2),
			rtRun(task+"C", task, "C", "SIMPLE", rtCheap, true, 0.2))
	}
	for i := 0; i < 6; i++ {
		task := fmt.Sprintf("c%d", i)
		runs = append(runs, rtRun(task+"A", task, "A", "COMPLEX", rtStrong, true, 1),
			rtRun(task+"B", task, "B", "COMPLEX", rtStrong, true, 1),
			rtRun(task+"C", task, "C", "COMPLEX", rtCheap, false, 0.2))
	}
	return runs
}

func TestRouterRecommendsLargestSafeCut(t *testing.T) {
	e := computeRouter(context.Background(), rtInput(rtMixed()))
	d := rtDetails(t, e)
	r := d.Recommendation
	if r.State != StateMeasured || !r.Estimated {
		t.Fatalf("recommendation: %+v", r)
	}
	// [SIMPLE] and [SIMPLE, MEDIUM] cost the same here (no MEDIUM task); the
	// first one found with the largest saving wins.
	if got := strings.Join(r.CheapTiers, ","); got != "SIMPLE" {
		t.Fatalf("cut %q", got)
	}
	if r.SavedShare == nil || *r.SavedShare < 0.39 || *r.SavedShare > 0.41 {
		t.Errorf("saved share %v", r.SavedShare)
	}
	if e.State != StateMeasured || e.Variant != "B" || e.Tasks != 12 {
		t.Errorf("effect: %+v", e)
	}
	if !strings.HasPrefix(e.Headline, "With the router on, cost per task fell 40%") {
		t.Errorf("headline %q", e.Headline)
	}
	if e.Result != "saved" {
		t.Errorf("result %q", e.Result)
	}
}

func TestRouterSettingIsDisabledGlobalRule(t *testing.T) {
	e := computeRouter(context.Background(), rtInput(rtMixed()))
	if e.Setting == nil || e.Setting.Kind != "routing_rule" {
		t.Fatalf("setting: %+v", e.Setting)
	}
	var r RoutingRule
	if err := json.Unmarshal([]byte(e.Setting.Body), &r); err != nil {
		t.Fatal(err)
	}
	if r.Enabled || r.Scope != "global" || r.Priority != 10 || r.Name != "heimdall-router-e_router" {
		t.Errorf("rule: %+v", r)
	}
	if r.CelExpression != `complexity_tier in ["MEDIUM", "COMPLEX"]` {
		t.Errorf("cel %q", r.CelExpression)
	}
	if len(r.Targets) != 1 || r.Targets[0].Model != rtStrong || r.Targets[0].Provider != "anthropic" || r.Targets[0].Weight != 1 {
		t.Errorf("targets: %+v", r.Targets)
	}
	if !strings.Contains(e.Setting.Note, "set your app's default model to "+rtCheap) {
		t.Errorf("note %q", e.Setting.Note)
	}
}
