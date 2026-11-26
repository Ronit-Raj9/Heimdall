package gwfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

const p4Targets = "anthropic:claude-sonnet-4-5,bedrock:us.anthropic.claude-sonnet-4-5-20250929-v1:0"

func p4Draft(t *testing.T, id string, opts map[string]string) (*schemas.Experiment, []string, error) {
	t.Helper()
	f, ok := Lookup(id)
	if !ok {
		t.Fatalf("feature %s not registered", id)
	}
	return f.Draft(context.Background(), DraftEnv{Root: t.TempDir()}, DraftRequest{Feature: id, Suite: "s@1", Base: "agents/a.yaml", Trials: 1, BudgetUSD: 5, Options: opts})
}

// p4Runs builds n tasks per arm, each with one run of calls rows from provider and served model.
func p4Runs(arm, provider, asked, served string, tasks, calls int, success bool) []Run {
	var out []Run
	for i := 0; i < tasks; i++ {
		s, c := success, 0.1
		r := Run{RunID: fmt.Sprintf("%s-%d", arm, i), Task: fmt.Sprintf("t%d", i), Arm: arm, Success: &s, CostUSD: &c}
		for j := 0; j < calls; j++ {
			r.Rows = append(r.Rows, Row{ID: fmt.Sprintf("%s-%d-%d", arm, i, j), Kind: "message", Status: "success", Provider: provider,
				ModelAsked: asked, ModelServed: served, LatencyMS: 100})
		}
		out = append(out, r)
	}
	return out
}

func p4Manifest(arms ...string) *schemas.Manifest {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{}}
	for _, a := range arms {
		m.Arms[a] = schemas.ArmManifest{Label: a}
	}
	return m
}

func p4ProvidersInput(runs []Run, targets string) Input {
	return Input{Manifest: p4Manifest("A", "B"), Runs: runs,
		Test: schemas.GatewayTest{Feature: schemas.GatewayFeatureProviders, Control: "A", Variants: []string{"B"}, Options: map[string]string{"targets": targets}}}
}

func p4Compute(t *testing.T, id string, in Input) Effect {
	t.Helper()
	f, _ := Lookup(id)
	return f.Compute(context.Background(), in)
}

func TestProvidersDraftRefusesDifferentFamilies(t *testing.T) {
	_, _, err := p4Draft(t, schemas.GatewayFeatureProviders, map[string]string{"targets": "anthropic:claude-sonnet-4-5,bedrock:us.anthropic.claude-haiku-4-5-20251001-v1:0"})
	if err == nil || !strings.Contains(err.Error(), "these are different models: claude-sonnet-4-5 and us.anthropic.claude-haiku-4-5-20251001-v1:0") {
		t.Fatalf("err = %v", err)
	}
	exp, notes, err := p4Draft(t, schemas.GatewayFeatureProviders, map[string]string{"targets": p4Targets + ",vertex:claude-sonnet-4-5@20250929"})
	if err != nil {
		t.Fatal(err)
	}
	if len(exp.Arms) != 3 || exp.Arms["C"].Set["gateway.provider"] != "vertex" || exp.Arms["B"].Set["model"] != "us.anthropic.claude-sonnet-4-5-20250929-v1:0" || exp.Arms["A"].Label != "anthropic" {
		t.Fatalf("arms = %+v", exp.Arms)
	}
	if exp.GatewayTest == nil || exp.GatewayTest.Control != "A" || strings.Join(exp.GatewayTest.Variants, ",") != "B,C" || len(notes) != 2 {
		t.Fatalf("test = %+v notes = %v", exp.GatewayTest, notes)
	}
}

func TestProvidersDraftRefusesSameProviderTwice(t *testing.T) {
	_, _, err := p4Draft(t, schemas.GatewayFeatureProviders, map[string]string{"targets": "anthropic:claude-sonnet-4-5,anthropic:claude-sonnet-4-5-20250929"})
	if err == nil || !strings.Contains(err.Error(), "anthropic") {
		t.Fatalf("err = %v", err)
	}
	if _, _, err := p4Draft(t, schemas.GatewayFeatureProviders, map[string]string{"targets": "anthropic:claude-sonnet-4-5"}); err == nil {
		t.Fatal("one target accepted")
	}
}

func p4Details(t *testing.T, e Effect) providersDetails {
	t.Helper()
	var d providersDetails
	if err := json.Unmarshal(e.Details, &d); err != nil {
		t.Fatalf("details: %v", err)
	}
	return d
}

func TestProvidersServedAsAskedIgnoresUnknown(t *testing.T) {
	runs := append(p4Runs("A", "anthropic", "claude-sonnet-4-5", "", 6, 5, true),
		p4Runs("B", "bedrock", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", "us.anthropic.claude-sonnet-4-5-20250929-v1:0", 6, 5, true)...)
	e := p4Compute(t, schemas.GatewayFeatureProviders, p4ProvidersInput(runs, p4Targets))
	d := p4Details(t, e)
	a, b := d.Arms[0], d.Arms[1]
	if a.ServedAsAsked != nil || a.ServedUnknown != 30 {
		t.Fatalf("A served = %v unknown %d", a.ServedAsAsked, a.ServedUnknown)
	}
	if b.ServedAsAsked == nil || *b.ServedAsAsked != 1 || b.ServedKnown != 30 || b.ServedUnknown != 0 {
		t.Fatalf("B = %+v", b)
	}
	if e.Variant != "B" || e.State != StateMeasured {
		t.Fatalf("variant %q state %s", e.Variant, e.State)
	}
	if !strings.Contains(e.Headline, "Every Bedrock call answered with the model asked for (30 of 30).") {
		t.Fatalf("headline = %s", e.Headline)
	}
}

func TestProvidersHeadlineNamesOtherServedModel(t *testing.T) {
	runs := append(p4Runs("A", "anthropic", "claude-sonnet-4-5", "claude-sonnet-4-5-20250929", 6, 5, true),
		p4Runs("B", "vertex", "claude-sonnet-4-5@20250929", "claude-sonnet-4-5@20250929", 6, 5, true)...)
	for i := 0; i < 3; i++ {
		runs[6+i].Rows[0].ModelServed = "claude-3-7-sonnet"
	}
	e := p4Compute(t, schemas.GatewayFeatureProviders, p4ProvidersInput(runs, "anthropic:claude-sonnet-4-5,vertex:claude-sonnet-4-5@20250929"))
	if !strings.Contains(e.Headline, "Vertex answered 3 of 30 calls with claude-3-7-sonnet, not the model asked for.") {
		t.Fatalf("headline = %s", e.Headline)
	}
	if e.Result != "failed" || e.Variant != "A" {
		t.Fatalf("result %q variant %q", e.Result, e.Variant)
	}
}

func TestProvidersNotAppliedWrongProvider(t *testing.T) {
	runs := append(p4Runs("A", "anthropic", "claude-sonnet-4-5", "", 6, 2, true),
		p4Runs("B", "anthropic", "claude-sonnet-4-5", "", 6, 2, true)...)
	e := p4Compute(t, schemas.GatewayFeatureProviders, p4ProvidersInput(runs, p4Targets))
	if e.State != StateNotApplied || e.Reason != "calls of setup B were answered by anthropic" || e.Result != "failed" {
		t.Fatalf("state %s reason %q result %q", e.State, e.Reason, e.Result)
	}
}

func TestProvidersSettingFallbackOrder(t *testing.T) {
	runs := append(p4Runs("A", "anthropic", "claude-sonnet-4-5", "claude-sonnet-4-5", 6, 5, true),
		p4Runs("B", "bedrock", "b-model", "", 6, 5, true)...)
	runs = append(runs, p4Runs("C", "vertex", "claude-sonnet-4-5@20250929", "", 6, 5, true)...)
	for i := 0; i < 6; i++ {
		runs[6+i].Rows[0].Status = "error" // B: 6 errors in 30
	}
	runs[12].Rows[0].Status = "error" // C: 1 error in 30
	in := p4ProvidersInput(runs, "anthropic:claude-sonnet-4-5,bedrock:b-model,vertex:claude-sonnet-4-5@20250929")
	in.Manifest = p4Manifest("A", "B", "C")
	in.Test.Variants = []string{"B", "C"}
	e := p4Compute(t, schemas.GatewayFeatureProviders, in)
	if e.Setting == nil || e.Setting.Kind != "routing_rule" {
		t.Fatalf("setting = %+v", e.Setting)
	}
	var r RoutingRule
	if err := json.Unmarshal([]byte(e.Setting.Body), &r); err != nil {
		t.Fatal(err)
	}
	if r.Enabled || r.Scope != "global" || r.CelExpression != `model == "claude-sonnet-4-5"` || len(r.Targets) != 1 || r.Targets[0].Provider != "anthropic" {
		t.Fatalf("rule = %+v", r)
	}
	if strings.Join(r.Fallbacks, ",") != "vertex/claude-sonnet-4-5@20250929,bedrock/b-model" {
		t.Fatalf("fallbacks = %v", r.Fallbacks)
	}
	if e.Setting.Note != "Bifrost tries fallbacks in order when the primary fails." {
		t.Fatalf("note = %q", e.Setting.Note)
	}
}
