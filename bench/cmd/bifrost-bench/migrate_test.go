package main

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/metrics"
	"github.com/maximhq/bifrost/bench/migration"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/verdict"
)

// recordedApp is an app over the committed recorded bundle, read only,
// with the pinned price snapshot and the project's suites and policies.
func recordedApp(t *testing.T) *app {
	t.Helper()
	cfg, err := lib.LoadConfig(filepath.Join("..", "..", "deploy", "bench.yaml"))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	st, err := store.NewFSStore(recordedRoot, schemas.NoopLogger{})
	if err != nil {
		t.Fatalf("open recorded store: %v", err)
	}
	prices, err := evidence.LoadPrices(filepath.Join("..", "..", "deploy", "pricing-snapshot.json"))
	if err != nil {
		t.Fatalf("prices: %v", err)
	}
	return &app{cfg: cfg, logger: schemas.NoopLogger{}, store: st, prices: prices, reg: &schemas.Registry{Policies: verdict.Builtin(), Metrics: metrics.Builtin()}}
}

func TestCostExplainRecorded(t *testing.T) {
	a := recordedApp(t)
	e, err := a.costExplain(context.Background(), "e_pilot_mixed_nc93", schemas.GradeSourceOriginal, costexplain.Query{})
	if err != nil {
		t.Fatalf("cost explain: %v", err)
	}
	if e.A.Arm != "A" || e.B.Arm != "B" || e.Synthetic {
		t.Fatalf("arms %s %s synthetic %v, want A, B, live", e.A.Arm, e.B.Arm, e.Synthetic)
	}
	ca, _ := e.A.CostPerRun.Float()
	cb, _ := e.B.CostPerRun.Float()
	total, ok := e.PerRun.Total.Float()
	if !ok || math.Abs(total-(cb-ca)) > 1e-12 {
		t.Fatalf("total %v, want B minus A = %g", e.PerRun.Total, cb-ca)
	}
	sum := 0.0
	for _, p := range e.PerRun.Parts {
		x, ok := p.USD.Float()
		if !ok {
			t.Fatalf("part %s unknown on a fully priced bundle: %+v", p.ID, p.USD)
		}
		sum += x
	}
	if math.Abs(sum-total) > 1e-12 {
		t.Errorf("parts sum to %g, total %g", sum, total)
	}
	// Every row of the bundle was priced by the snapshot the repo pins, so
	// nothing is unattributed and the split is complete.
	if e.A.UnattributedRows+e.B.UnattributedRows+e.A.UnpricedRows+e.B.UnpricedRows != 0 {
		t.Errorf("unsplit or unpriced rows on the pinned snapshot: %+v %+v", e.A, e.B)
	}
	if !strings.Contains(costexplain.Markdown(e), "Cost per run, B minus A") {
		t.Error("markdown lacks the per-run waterfall")
	}
}

func TestMigrateReportRecorded(t *testing.T) {
	a := recordedApp(t)
	ctx := context.Background()
	rep, err := a.migrationReport(ctx, "e_pilot_mixed_nc93", schemas.GradeSourceOriginal, "", true, migration.Query{})
	if err != nil {
		t.Fatalf("migrate report: %v", err)
	}
	if len(rep.Pairs) != 1 || rep.Pairs[0].From.Arm != "A" || rep.Pairs[0].To.Arm != "B" {
		t.Fatalf("pairs %+v, want A to B", rep.Pairs)
	}
	p := rep.Pairs[0]
	// One run per task and condition: nothing can be told apart, and the
	// recommendation must say so.
	if p.Recommendation.Action != migration.ActionNotEnough || p.Overall.Result != migration.ResultCannotTell {
		t.Errorf("recommendation %s overall %s, want not enough evidence and cannot tell", p.Recommendation.Action, p.Overall.Result)
	}
	if rep.Policy != "enterprise-default" || p.Recommendation.Policy == nil {
		t.Errorf("policy %q %+v, want the experiment's enterprise-default policy evaluated", rep.Policy, p.Recommendation.Policy)
	}
	if !strings.Contains(strings.Join(rep.Notes, " "), "different agent configs") {
		t.Errorf("notes %v, want the harness note (codex against custom-python)", rep.Notes)
	}
	found := false
	for _, f := range p.Flipped {
		found = found || (f.Task == "t05" && f.Condition == "empty-test-run" && f.Kind == "fixed")
	}
	if !found {
		t.Errorf("flipped %+v, want t05 under empty-test-run fixed (Codex fails it, custom-python passes)", p.Flipped)
	}
	smoke, err := a.migrationReport(ctx, "e_smoke_sdoj", schemas.GradeSourceOriginal, "none", true, migration.Query{From: "A"})
	if err != nil {
		t.Fatalf("migrate report smoke: %v", err)
	}
	if !smoke.Synthetic || smoke.Pairs[0].Recommendation.Action != migration.ActionNotEnough {
		t.Errorf("smoke synthetic %v action %s, want synthetic and not enough evidence", smoke.Synthetic, smoke.Pairs[0].Recommendation.Action)
	}
}

func TestMigrateUsage(t *testing.T) {
	for _, args := range [][]string{{}, {"-agent", "a.yaml"}, {"report"}, {"report", "-exp"}} {
		if code := runMigrate(args); code != exitUsage {
			t.Errorf("runMigrate(%v) = %d, want %d", args, code, exitUsage)
		}
	}
	for _, args := range [][]string{{}, {"explain"}, {"pareto"}, {"other"}} {
		if code := runCost(args); code != exitUsage {
			t.Errorf("runCost(%v) = %d, want %d", args, code, exitUsage)
		}
	}
}

func TestMigrationExperimentPlans(t *testing.T) {
	text := migrationExperiment("agents/codex.yaml", "coding-core@1.0.0", "gpt-5.4-mini", []string{"gpt-5.4", "gpt-5.4-nano"}, "openai",
		[]string{"clean", "model-outage"}, []string{"t01", "t02"}, 3, 2.5, "policies/default.yaml", false)
	path := filepath.Join(t.TempDir(), "experiment.yaml")
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	exp, err := loadExperiment(path)
	if err != nil {
		t.Fatalf("the generated experiment does not load: %v\n%s", err, text)
	}
	if exp.Matrix == nil || len(exp.Matrix.Models) != 3 || exp.Matrix.Models[0].Model != "gpt-5.4-mini" || exp.Matrix.Models[1].Provider != "openai" {
		t.Errorf("matrix %+v, want the old model first, then the new ones via openai", exp.Matrix)
	}
	if len(exp.Conditions) != 2 || exp.Conditions[1].Trials != 3 || len(exp.Conditions[0].Tasks.IDs) != 2 || exp.Budget.TotalUSD != 2.5 || exp.Policy != "policies/default.yaml" {
		t.Errorf("experiment %+v", exp)
	}
}
