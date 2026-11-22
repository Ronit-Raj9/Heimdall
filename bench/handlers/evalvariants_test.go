package handlers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/valyala/fasthttp"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
)

// pairedLauncher is evalLauncher with the planner's paired sign-flip rule
// (engine/planner.go validateExperiment): an experiment of two or more arms
// with that verdict needs arms A and B, else it is refused at plan time.
type pairedLauncher struct{ *evalLauncher }

func (l pairedLauncher) Plan(ctx context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	if len(exp.Arms) >= 2 && exp.Matrix == nil && (exp.Verdict.Policy == "" || exp.Verdict.Policy == verdict.PolicyPairedSignFlip) {
		for _, id := range []string{verdict.ArmBaseline, verdict.ArmCandidate} {
			if _, ok := exp.Arms[id]; !ok {
				return nil, fmt.Errorf("the %s verdict needs arms %s (baseline) and %s (candidate); arm %s is missing", verdict.PolicyPairedSignFlip, verdict.ArmBaseline, verdict.ArmCandidate, id)
			}
		}
	}
	return l.evalLauncher.Plan(ctx, exp)
}

// launchPlanned launches body through a launcher that refuses what the
// planner refuses and returns the one child's planned experiment.
func launchPlanned(t *testing.T, body string) (*schemas.Evaluation, *schemas.Experiment) {
	t.Helper()
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: pairedLauncher{l}, Suites: evalSuites()})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations", body)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	if len(ev.Children) != 1 {
		t.Fatalf("children %+v", ev.Children)
	}
	c := ev.Children[0]
	if c.Status == schemas.EvaluationChildSkipped || c.Skipped != "" {
		t.Fatalf("child skipped: %s", c.Skipped)
	}
	exp := l.planned[c.ExperimentID]
	if exp == nil {
		t.Fatalf("no experiment planned: %+v", c)
	}
	return ev, exp
}

// D5: score mode with "Bifrost cache on vs off" plans (arms A and B).
func TestScoreEvaluationWithCachePlans(t *testing.T) {
	_, exp := launchPlanned(t, `{"mode":"score","arms":[{"label":"A","agent":"scripted","model":"m1"}],"benchmarks":["longmemeval"],"bifrost_cache":true}`)
	b, ok := exp.Arms["B"]
	if len(exp.Arms) != 2 || !ok || b.Label != exp.Arms["A"].Label+schemas.EvaluationCacheSuffix {
		t.Fatalf("arms %+v", exp.Arms)
	}
}

// D6: score mode with provider failover plans (arms A and B).
func TestScoreEvaluationWithFailoverPlans(t *testing.T) {
	_, exp := launchPlanned(t, `{"mode":"score","arms":[{"label":"A","agent":"scripted","model":"openai/gpt-4o"}],"benchmarks":["longmemeval"],
		"failover":{"enabled":true,"fallbacks":[{"model":"openai/gpt-4o-mini"}]}}`)
	b, ok := exp.Arms["B"]
	if len(exp.Arms) != 2 || !ok || !strings.HasSuffix(b.Label, " with failover") {
		t.Fatalf("arms %+v", exp.Arms)
	}
}

// D5: the score-mode stream's no-memory control is arm B and the fresh arm.
func TestScoreEvaluationWithStreamControlPlans(t *testing.T) {
	_, exp := launchPlanned(t, `{"mode":"score","arms":[{"label":"A","agent":"scripted","model":"m1"}],"benchmarks":["longmemeval"],"stream":{"memory":"carry"}}`)
	if b, ok := exp.Arms["B"]; len(exp.Arms) != 2 || !ok || !strings.HasSuffix(b.Label, schemas.StreamControlSuffix) || exp.Stream == nil || exp.Stream.FreshArm != "B" {
		t.Fatalf("arms %+v stream %+v", exp.Arms, exp.Stream)
	}
}

// D10.dropped: faults that do not apply to a benchmark are stored on the
// child, not only in the experiment's comment header.
func TestEvaluationChildFaultsSkipped(t *testing.T) {
	ev, _ := launchPlanned(t, `{"mode":"score","arms":[{"label":"A","agent":"scripted","model":"m1"}],"benchmarks":["longmemeval"],"faults":["clean","broken-tool"]}`)
	got := ev.Children[0].FaultsSkipped
	if len(got) != 1 || got[0].Fault != "broken-tool" || got[0].Reason == "" {
		t.Fatalf("faults_skipped %+v", got)
	}
}

func scoreSpec() schemas.EvaluationSpec {
	return schemas.EvaluationSpec{Mode: schemas.EvaluationModeScore, Arms: []schemas.EvaluationArm{{Label: "A", Agent: "scripted", Model: "openai/gpt-4o"}}}
}

func onFailover() *schemas.FailoverOption {
	return &schemas.FailoverOption{Enabled: true, Fallbacks: []schemas.RouteTarget{{Provider: "openai", Model: "gpt-4o-mini"}}}
}

func TestWithVariantArmsScoreCacheIsB(t *testing.T) {
	spec := scoreSpec()
	spec.BifrostCache = true
	arms, fresh := withVariantArms(spec, evalArms(spec))
	if len(arms) != 2 || fresh != "" || arms["B"].Label != arms["A"].Label+schemas.EvaluationCacheSuffix {
		t.Fatalf("arms %+v fresh %q", arms, fresh)
	}
}

func TestWithVariantArmsFailoverIsB(t *testing.T) {
	spec := scoreSpec()
	spec.Failover = onFailover()
	arms, _ := withVariantArms(spec, evalArms(spec))
	if len(arms) != 2 || arms["B"].Label != arms["A"].Label+" with failover" || arms["B"].Set["gateway.routing"] == nil {
		t.Fatalf("arms %+v", arms)
	}
	if len(evalArms(spec)) != 1 {
		t.Fatal("evalArms must not add the failover arm itself")
	}
}

func TestWithVariantArmsCacheAndFailover(t *testing.T) {
	spec := scoreSpec()
	spec.BifrostCache, spec.Failover = true, onFailover()
	arms, _ := withVariantArms(spec, evalArms(spec))
	if len(arms) != 3 || !strings.HasSuffix(arms["B"].Label, schemas.EvaluationCacheSuffix) || !strings.HasSuffix(arms["failover"].Label, " with failover") {
		t.Fatalf("arms %+v", arms)
	}
	if evalArmCount(spec) != 3 {
		t.Fatalf("evalArmCount %d, want 3", evalArmCount(spec))
	}
}

func TestWithVariantArmsStreamControl(t *testing.T) {
	spec := scoreSpec()
	spec.Stream = &schemas.StreamSpec{Memory: schemas.StreamMemoryCarry}
	arms, fresh := withVariantArms(spec, evalArms(spec))
	if len(arms) != 2 || fresh != "B" || arms["B"].Label != arms["A"].Label+schemas.StreamControlSuffix {
		t.Fatalf("arms %+v fresh %q", arms, fresh)
	}
}

func TestWithVariantArmsTwoArmsUnchanged(t *testing.T) {
	spec := scoreSpec()
	spec.Mode = schemas.EvaluationModeModels
	spec.Arms = append(spec.Arms, schemas.EvaluationArm{Label: "B", Agent: "scripted", Model: "openai/gpt-4o-mini"})
	spec.BifrostCache = true
	in := evalArms(spec)
	arms, fresh := withVariantArms(spec, in)
	if len(arms) != 2 || fresh != "" || arms["A"].Label != in["A"].Label || arms["B"].Label != in["B"].Label {
		t.Fatalf("arms %+v", arms)
	}
}

// Readers find a variant arm by its label, whatever its id.
func TestVariantArmIDByLabel(t *testing.T) {
	m := &schemas.Manifest{Arms: map[string]schemas.ArmManifest{"A": {Label: "x"}, "B": {Label: "x" + schemas.EvaluationCacheSuffix}}}
	if got := schemas.VariantArmID(m, schemas.EvaluationCacheSuffix); got != "B" || !schemas.CacheVariantArm(m, "B") || schemas.CacheVariantArm(m, "A") {
		t.Fatalf("variant arm %q", got)
	}
	old := &schemas.Manifest{Experiment: schemas.Experiment{Arms: map[string]schemas.ArmSpec{"A": {Label: "x"}, "cache": {Label: "x" + schemas.EvaluationCacheSuffix}}}}
	if got := schemas.VariantArmID(old, schemas.EvaluationCacheSuffix); got != "cache" || !schemas.CacheVariantArm(old, "cache") {
		t.Fatalf("older variant arm %q", got)
	}
	if schemas.VariantArmID(m, " with failover") != "" {
		t.Fatal("no failover arm expected")
	}
}

// bothArmsForecast answers with a history range for every arm of the
// experiment it is asked about.
type bothArmsForecast struct {
	fakeForecast
}

func (f *bothArmsForecast) Priced(string) bool { return true }

func (f *bothArmsForecast) Estimate(ctx context.Context, exp *schemas.Experiment) (*schemas.Forecast, error) {
	out, err := f.fakeForecast.Estimate(ctx, exp)
	if err != nil {
		return nil, err
	}
	one := out.Arms[0]
	out.Arms = nil
	for id := range exp.Arms {
		a := one
		a.Arm = id
		out.Arms = append(out.Arms, a)
	}
	return out, nil
}

// D4.est: the cache-on copy is counted and planned, so the cost stays known.
func TestEstimateCountsCacheArm(t *testing.T) {
	src := &bothArmsForecast{}
	req := &EvalEstimateRequest{Mode: "score", Arms: []EvalEstimateArm{{Label: "A", Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"plain"}, BifrostCache: true}
	est, err := EstimateEvaluation(context.Background(), estimateSuites(), src, req)
	if err != nil {
		t.Fatal(err)
	}
	b := est.PerBenchmark[0]
	if b.Runs != 2*len(b.Conditions)*b.Tasks*EvalDefaultTrials || b.EstCostUSD.Low != 2 || !b.EstCostUSD.Known || !est.EstCostUSD.Known {
		t.Fatalf("runs %d cost %+v notes %v", b.Runs, b.EstCostUSD, b.Notes)
	}
	exp := src.expFor("plain@2")
	if exp == nil || len(exp.Arms) != 2 || !strings.HasSuffix(exp.Arms["B"].Label, schemas.EvaluationCacheSuffix) {
		t.Fatalf("experiment %+v", exp)
	}
}

// D6.est: the failover arm and the primary outage condition are counted.
func TestEstimateFailoverAddsArm(t *testing.T) {
	src := &bothArmsForecast{}
	req := &EvalEstimateRequest{Mode: "score", Arms: []EvalEstimateArm{{Label: "A", Agent: "codex", Model: "openai/gpt-4o"}}, Benchmarks: []string{"plain"},
		Faults: []string{"clean"}, Failover: &schemas.FailoverOption{Enabled: true, Fallbacks: []schemas.RouteTarget{{Model: "openai/gpt-4o-mini"}}}}
	est, err := EstimateEvaluation(context.Background(), estimateSuites(), src, req)
	if err != nil {
		t.Fatal(err)
	}
	b := est.PerBenchmark[0]
	if !slices.Contains(b.Conditions, schemas.GenericProbePrimaryOutage) || b.Runs != 2*len(b.Conditions)*1*EvalDefaultTrials || len(b.Conditions) != 2 || !b.EstCostUSD.Known {
		t.Fatalf("conditions %v runs %d cost %+v", b.Conditions, b.Runs, b.EstCostUSD)
	}
	exp := src.expFor("plain@2")
	if exp == nil || len(exp.Arms) != 2 || !strings.HasSuffix(exp.Arms["B"].Label, " with failover") {
		t.Fatalf("experiment %+v", exp)
	}
	// A failover option on two arms is refused, as the launch refuses it.
	bad := &EvalEstimateRequest{Mode: "models", Arms: []EvalEstimateArm{{Agent: "codex", Model: "openai/gpt-4o"}, {Agent: "codex", Model: "openai/gpt-4o-mini"}},
		Benchmarks: []string{"plain"}, Failover: &schemas.FailoverOption{Enabled: true, Fallbacks: []schemas.RouteTarget{{Model: "openai/gpt-4o-mini"}}}}
	if _, err := EstimateEvaluation(context.Background(), estimateSuites(), nil, bad); !errors.Is(err, errQuickBad) {
		t.Fatalf("two-arm failover: %v", err)
	}
}

// D4.toolfaults: a Bifrost MCP route on a benchmark without an MCP tool
// server says so on that benchmark.
func TestEstimateMCPRouteNoteOnBenchmarkWithoutMCP(t *testing.T) {
	suites := estimateSuites()
	suites.suites["tools"] = &schemas.Suite{ID: "tools", Version: "1", TaskIDs: []string{"a"},
		Tasks: []*schemas.Task{{ID: "a", Services: []schemas.ServiceSpec{{ID: "srv", Role: schemas.ServiceRoleMCP}}}}}
	suites.ids = append(suites.ids, "tools")
	req := &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"plain", "tools"}, MCPRoute: schemas.MCPRouteBifrost}
	est, err := EstimateEvaluation(context.Background(), suites, nil, req)
	if err != nil {
		t.Fatal(err)
	}
	want := "The MCP route does not apply to plain: it has no MCP tool server."
	if !slices.Contains(est.PerBenchmark[0].Notes, want) {
		t.Fatalf("plain notes %v", est.PerBenchmark[0].Notes)
	}
	for _, n := range est.PerBenchmark[1].Notes {
		if strings.Contains(n, "MCP route") {
			t.Fatalf("tools has an MCP server: %v", est.PerBenchmark[1].Notes)
		}
	}
	req = &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"plain"}, MCPRoute: "nope"}
	if _, err := EstimateEvaluation(context.Background(), suites, nil, req); !errors.Is(err, errQuickBad) {
		t.Fatalf("bad route: %v", err)
	}
}
