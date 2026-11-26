package gwfeature

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func p4BudgetDraft(t *testing.T, base string, opts map[string]string) (*schemas.Experiment, []string, error) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "agents", "a.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := Lookup(schemas.GatewayFeatureBudgetCurve)
	return f.Draft(context.Background(), DraftEnv{Root: root}, DraftRequest{Feature: f.ID, Suite: "s@1", Base: "agents/a.yaml", Model: "claude-sonnet-4-5", Trials: 1, BudgetUSD: 5, Options: opts})
}

func TestBudgetDraftArmsAscendingWithLimits(t *testing.T) {
	exp, notes, err := p4BudgetDraft(t, "id: a\nmodel: x\n", map[string]string{"budgets": "0.10, 0.25, 1.00"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{"A": 1, "B": 0.10, "C": 0.25}
	for arm, b := range want {
		set := exp.Arms[arm].Set
		raw, _ := json.Marshal(set["gateway.routing"])
		var r schemas.RoutingPolicy
		if err := json.Unmarshal(raw, &r); err != nil || r.Strategy != schemas.RoutingFixed || r.Limits == nil || r.Limits.BudgetUSD != b || r.Limits.BudgetReset != "1d" || len(r.Targets) != 1 || r.Targets[0].Model != "claude-sonnet-4-5" {
			t.Fatalf("arm %s routing %s", arm, raw)
		}
		if set["budgets.usd"] != b || set["model"] != "claude-sonnet-4-5" {
			t.Fatalf("arm %s set %v", arm, set)
		}
	}
	if !exp.Budget.Disclose || len(notes) != 1 || exp.GatewayTest.Control != "A" || strings.Join(exp.GatewayTest.Variants, ",") != "B,C" {
		t.Fatalf("disclose %v notes %v test %+v", exp.Budget.Disclose, notes, exp.GatewayTest)
	}
	if exp.Arms["B"].Label != "0.10 USD per task" {
		t.Fatalf("label %q", exp.Arms["B"].Label)
	}
	if _, _, err := p4BudgetDraft(t, "id: a\n", map[string]string{"budgets": "0.10,1.00"}); err == nil {
		t.Fatal("two budgets accepted")
	}
	if _, _, err := p4BudgetDraft(t, "id: a\n", map[string]string{"budgets": "0.50,0.25,1.00"}); err == nil {
		t.Fatal("budgets out of order accepted")
	}
	exp, _, err = p4BudgetDraft(t, "id: a\n", map[string]string{"budgets": "0.10,0.25,1.00", "current": "0.25", "tell_agent": "no"})
	if err != nil || exp.Budget.Disclose || exp.Arms["A"].Set["budgets.usd"] != 0.25 || exp.Arms["C"].Set["budgets.usd"] != 1.0 {
		t.Fatalf("current: err %v exp %+v", err, exp)
	}
}

func TestBudgetDraftRefusesFairnessBase(t *testing.T) {
	_, _, err := p4BudgetDraft(t, "id: a\nfairness: equal_budget\n", map[string]string{"budgets": "0.10,0.25,1.00"})
	if err == nil {
		t.Fatal("fairness base accepted")
	}
	_, _, err = p4BudgetDraft(t, "id: a\ngateway:\n  routing:\n    strategy: fixed\n", map[string]string{"budgets": "0.10,0.25,1.00"})
	if err == nil {
		t.Fatal("routing base accepted")
	}
}

// p4BudgetRuns adds tasks runs to arm with budget b; solved says which task passes.
func p4BudgetRuns(arm string, tasks int, cost float64, solved func(i int) bool) []Run {
	var out []Run
	for i := 0; i < tasks; i++ {
		s, c := solved(i), cost
		out = append(out, Run{RunID: fmt.Sprintf("%s-%d", arm, i), Task: fmt.Sprintf("t%02d", i), Arm: arm, Success: &s, CostUSD: &c})
	}
	return out
}

func p4BudgetInput(runs []Run, budgets map[string]float64) Input {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{}}
	var variants []string
	for _, arm := range []string{"A", "B", "C"} {
		b, ok := budgets[arm]
		if !ok {
			continue
		}
		cfg := schemas.AgentConfig{}
		cfg.Budgets.USD = b
		m.Arms[arm] = schemas.ArmManifest{Label: arm, Config: cfg}
		if arm != "A" {
			variants = append(variants, arm)
		}
	}
	return Input{Manifest: m, Runs: runs, Test: schemas.GatewayTest{Feature: schemas.GatewayFeatureBudgetCurve, Control: "A", Variants: variants}}
}

func p4BudgetDetails(t *testing.T, e Effect) budgetDetails {
	t.Helper()
	var d budgetDetails
	if err := json.Unmarshal(e.Details, &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestBudgetBestValueSmallestIndistinguishable(t *testing.T) {
	// A 1.00 USD solves 40 of 40, C 0.25 USD solves 39 of 40, B 0.10 USD solves 12 of 40.
	runs := p4BudgetRuns("A", 40, 0.5, func(int) bool { return true })
	runs = append(runs, p4BudgetRuns("C", 40, 0.2, func(i int) bool { return i > 0 })...)
	runs = append(runs, p4BudgetRuns("B", 40, 0.1, func(i int) bool { return i < 12 })...)
	e := p4Compute(t, schemas.GatewayFeatureBudgetCurve, p4BudgetInput(runs, map[string]float64{"A": 1, "B": 0.1, "C": 0.25}))
	if e.State != StateMeasured || e.Variant != "C" {
		t.Fatalf("state %s variant %q reason %q", e.State, e.Variant, e.Reason)
	}
	d := p4BudgetDetails(t, e)
	if len(d.Points) != 3 || d.Points[0].Arm != "B" || d.Points[2].Arm != "A" || d.BestValue != "C" {
		t.Fatalf("points %+v best %s", d.Points, d.BestValue)
	}
	if c := d.Points[1].CostPerSolved; c == nil || fmt.Sprintf("%.4f", *c) != "0.2051" {
		t.Fatalf("cost per solved %v", c)
	}
	if !strings.HasPrefix(e.Headline, "0.25 USD per task solves 98% (range ") || !strings.Contains(e.Headline, "as many as 1.00 USD (100%); 0.10 USD solves 30%. Best value: 0.25 USD.") {
		t.Fatalf("headline %s", e.Headline)
	}
	if e.Setting == nil || e.Setting.Kind != "virtual_key" || !strings.Contains(e.Setting.Body, `"max_limit": 0.25`) {
		t.Fatalf("setting %+v", e.Setting)
	}
}

func TestBudgetStoppedShareFromRefusedRows(t *testing.T) {
	runs := p4BudgetRuns("A", 6, 0.5, func(int) bool { return true })
	runs = append(runs, p4BudgetRuns("B", 6, 0.1, func(int) bool { return false })...)
	runs = append(runs, p4BudgetRuns("C", 6, 0.2, func(int) bool { return true })...)
	for i := 6; i < 9; i++ {
		runs[i].Rows = []Row{{Kind: "message", Status: "success"}, {Kind: "message", Status: "error", BudgetRefused: true}}
	}
	e := p4Compute(t, schemas.GatewayFeatureBudgetCurve, p4BudgetInput(runs, map[string]float64{"A": 1, "B": 0.1, "C": 0.25}))
	d := p4BudgetDetails(t, e)
	if s := d.Points[0].StoppedShare; d.Points[0].Arm != "B" || s == nil || *s != 0.5 {
		t.Fatalf("B stopped %v", s)
	}
	if s := d.Points[1].StoppedShare; s == nil || *s != 0 {
		t.Fatalf("C stopped %v", s)
	}
}

func TestBudgetNotEnoughData(t *testing.T) {
	runs := p4BudgetRuns("A", 3, 0.5, func(int) bool { return true })
	runs = append(runs, p4BudgetRuns("B", 3, 0.1, func(int) bool { return true })...)
	runs = append(runs, p4BudgetRuns("C", 3, 0.2, func(int) bool { return true })...)
	e := p4Compute(t, schemas.GatewayFeatureBudgetCurve, p4BudgetInput(runs, map[string]float64{"A": 1, "B": 0.1, "C": 0.25}))
	if e.State != StateNotEnoughData || e.Reason != "3 of the 5 tasks needed have results on both setups" || e.Result != "" {
		t.Fatalf("state %s reason %q result %q", e.State, e.Reason, e.Result)
	}
}
