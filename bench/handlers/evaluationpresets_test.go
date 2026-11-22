package handlers

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// libraryForecast answers like pricedForecast with every arm estimated from
// library runs (a first run of this agent).
type libraryForecast struct{ pricedForecast }

func (f *libraryForecast) Estimate(ctx context.Context, exp *schemas.Experiment) (*schemas.Forecast, error) {
	out, err := f.pricedForecast.Estimate(ctx, exp)
	if err != nil {
		return nil, err
	}
	for i := range out.Arms {
		out.Arms[i].Basis, out.Arms[i].BasisText = schemas.BasisLibrary, "first run, estimate from library data: 9 sealed runs of codex"
	}
	return out, nil
}

func TestEstimatePresets(t *testing.T) {
	src := &pricedForecast{priced: map[string]bool{"gpt-x": true}}
	req := &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"coding-core"}, Presets: true}
	est, err := EstimateEvaluation(context.Background(), estimateSuites(), src, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(est.Presets) != len(EvaluationPresets) {
		t.Fatalf("presets %+v", est.Presets)
	}
	ids := []string{}
	for _, p := range est.Presets {
		ids = append(ids, p.ID)
		if p.Runs == 0 || !p.EstCostUSD.Known || p.Summary == "" || p.Label == "" {
			t.Fatalf("preset %+v", p)
		}
	}
	if !slices.Equal(ids, []string{PresetQuick, PresetStandard, PresetThorough}) {
		t.Fatalf("ids %v", ids)
	}
	quick, thorough := est.Presets[0], est.Presets[2]
	// coding-core has 3 tasks: every preset runs all 3; thorough twice,
	// clean plus its two faults (broken-tool is skipped there).
	if quick.Runs != 3 || thorough.Runs != 3*2*2 || !slices.Contains(thorough.Faults, "model-down") {
		t.Fatalf("quick %+v thorough %+v", quick, thorough)
	}
}

func TestEvalPresetsDetectSmallerChanges(t *testing.T) {
	// The summaries promise the power table: more tasks, smaller change.
	var last float64 = 101
	for _, p := range EvaluationPresets {
		m := mdePP(p.TasksPerBenchmark, EvalPowerAlpha)
		if m == nil || *m >= last {
			t.Fatalf("preset %s: mde %v not below %v", p.ID, m, last)
		}
		last = *m
	}
}

func TestEstimateFromLibraryIsFlagged(t *testing.T) {
	src := &libraryForecast{pricedForecast{priced: map[string]bool{"gpt-x": true}}}
	req := &EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"coding-core"}}
	est, err := EstimateEvaluation(context.Background(), estimateSuites(), src, req)
	if err != nil {
		t.Fatal(err)
	}
	if !est.FromLibrary || !est.PerBenchmark[0].FromLibrary || !est.EstCostUSD.Known {
		t.Fatalf("library %+v", est)
	}
	if !slices.ContainsFunc(est.Notes, func(n string) bool { return strings.HasPrefix(n, "First run, estimate from library data") }) {
		t.Fatalf("notes %v", est.Notes)
	}
}

func TestEstimateSlotsAndConcurrency(t *testing.T) {
	src := &pricedForecast{priced: map[string]bool{"gpt-x": true}}
	base := EvalEstimateRequest{Arms: []EvalEstimateArm{{Agent: "codex", Model: "gpt-x"}}, Benchmarks: []string{"coding-core", "plain"}}
	r := base
	est, err := EstimateEvaluation(context.Background(), estimateSuites(), src, &r)
	if err != nil {
		t.Fatal(err)
	}
	if est.Slots.Concurrency != EvaluationDefaultConcurrency || est.Slots.Parallel != 2*EvaluationDefaultConcurrency || est.Slots.OrgSlots != 0 {
		t.Fatalf("slots %+v", est.Slots)
	}
	if exp := src.expFor("plain@2"); exp == nil || exp.Concurrency != EvaluationDefaultConcurrency {
		t.Fatalf("experiment concurrency %+v", exp)
	}
	// Two benchmarks of 1 to 3 agent seconds each, over two slots.
	r = base
	r.orgSlots = 2
	est, err = EstimateEvaluation(context.Background(), estimateSuites(), src, &r)
	if err != nil {
		t.Fatal(err)
	}
	if est.Slots.Parallel != 2 || est.Slots.Concurrency != 2 || est.EstMinutes.High != 6.0/2/60 {
		t.Fatalf("bounded %+v minutes %+v", est.Slots, est.EstMinutes)
	}
	r = base
	r.Concurrency = 99
	if _, err := EstimateEvaluation(context.Background(), estimateSuites(), src, &r); err == nil {
		t.Fatal("concurrency 99 accepted")
	}
}

func TestOrgSlotsAndCap(t *testing.T) {
	q := quota.NewStub()
	if s, c := orgSlotsAndCap(context.Background(), Services{Hosted: true, Quota: q, OrgID: "o1"}); s != quota.DefaultLimits.MaxConcurrentRuns || c != 0 {
		t.Fatalf("hosted %d %v", s, c)
	}
	if s, _ := orgSlotsAndCap(context.Background(), Services{Quota: q, OrgID: "o1"}); s != 0 {
		t.Fatalf("local must not bound slots, got %d", s)
	}
	if quota.DefaultLimits.MaxConcurrentRuns < 4 {
		t.Fatalf("default org slots %d, want at least 4 (G4-04)", quota.DefaultLimits.MaxConcurrentRuns)
	}
}

func TestEvaluationPresetAndConcurrencyValidated(t *testing.T) {
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	for body, want := range map[string]int{
		`{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"preset":"huge"}`:                     fasthttp.StatusBadRequest,
		`{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"concurrency":99}`:                    fasthttp.StatusBadRequest,
		`{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"preset":"standard","concurrency":6}`: fasthttp.StatusAccepted,
	} {
		if got := evalCall(r, "POST", APIPrefix+"/evaluations", body); got.Response.StatusCode() != want {
			t.Fatalf("%s: %d, want %d: %s", body, got.Response.StatusCode(), want, got.Response.Body())
		}
	}
	for _, exp := range l.planned {
		if exp.Concurrency != 6 {
			t.Fatalf("concurrency %d, want 6", exp.Concurrency)
		}
	}
}
