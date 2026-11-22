package handlers

// Evaluation size presets and worker slots (G4-04, G4-16). The wizard offers
// three sizes of the same setup, each with its runs, time, cost range and
// the smallest change it can detect, from one estimate request (presets:
// true). Quick look is the default: a first answer in minutes. The estimate
// also says how many runs go side by side: the per-benchmark concurrency of
// the spec over all its benchmarks, bounded by the org's worker slots.

import (
	"context"
	"slices"
	"sync"

	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/schemas"
)

// EvalPreset is one size of an evaluation.
type EvalPreset struct {
	ID                string   `json:"id"`
	Label             string   `json:"label"`
	Summary           string   `json:"summary"`
	TasksPerBenchmark int      `json:"tasks_per_benchmark"`
	Trials            int      `json:"trials"`
	Faults            []string `json:"faults"` // added to the faults the setup already has
}

// Preset ids.
const (
	PresetQuick    = "quick"
	PresetStandard = "standard"
	PresetThorough = "thorough"
)

// EvaluationPresets are the sizes the wizard offers, smallest first. The
// task counts follow the power table of the estimate: 10 tasks see about a
// 30 point change, 30 about 16, 50 about 12 (EvalPowerSD, alpha 0.05).
var EvaluationPresets = []EvalPreset{
	{ID: PresetQuick, Label: "Quick look", Summary: "A first answer in minutes: 10 tasks per benchmark, once each, clean only.",
		TasksPerBenchmark: 10, Trials: 1, Faults: []string{faults.Clean}},
	{ID: PresetStandard, Label: "Standard", Summary: "Enough tasks for a decision: 30 per benchmark, once each.",
		TasksPerBenchmark: 30, Trials: 1, Faults: []string{faults.Clean}},
	{ID: PresetThorough, Label: "Thorough", Summary: "Before a release: 50 tasks per benchmark, twice each, also with the model down and a broken tool.",
		TasksPerBenchmark: 50, Trials: 2, Faults: []string{faults.Clean, "model-down", "broken-tool"}},
}

// EvalPresetEstimate is one preset of the setup, estimated.
type EvalPresetEstimate struct {
	EvalPreset
	Runs       int              `json:"runs"`
	EstCostUSD EvalCostRange    `json:"est_cost_usd"`
	EstMinutes EvalMinutesRange `json:"est_minutes"`
	// MDEpp is the smallest change it can detect overall, in points, and
	// MDEPerBenchmarkPP the largest per-benchmark one (null when a
	// benchmark has too few tasks to decide).
	MDEpp             *float64 `json:"mde_pp"`
	MDEPerBenchmarkPP *float64 `json:"mde_per_benchmark_pp"`
	FromLibrary       bool     `json:"from_library,omitempty"`
}

// normalizePreset checks spec.Preset; the numbers of the spec are what
// runs, the preset only names where they came from.
func normalizePreset(spec *schemas.EvaluationSpec) error {
	if spec.Preset == "" || spec.Preset == schemas.EvaluationPresetFirstLook { // first_look: normalizeFirstLook (lane Z01)
		return nil
	}
	for _, p := range EvaluationPresets {
		if p.ID == spec.Preset {
			return nil
		}
	}
	return badf("preset %q is not one of quick, standard, thorough, first_look", spec.Preset)
}

// estimatePresets estimates every preset of req's setup: req with the
// preset's tasks, trials and faults (added to req's own). The presets are
// estimated side by side (at most presetParallel at once) through the same
// forecast cache and come back in the order of EvaluationPresets (D4.slow).
// A preset that cannot be estimated is left out.
func estimatePresets(ctx context.Context, suites SuiteSource, src ForecastSource, req *EvalEstimateRequest, agents map[string]*schemas.AgentConfig, root string, cache *forecastCache) []EvalPresetEstimate {
	results := make([]*EvalPresetEstimate, len(EvaluationPresets))
	sem := make(chan struct{}, presetParallel)
	var wg sync.WaitGroup
	for i, p := range EvaluationPresets {
		r := presetRequest(req, p)
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			est, err := estimateEvaluation(ctx, suites, src, r, agents, root, cache)
			if err != nil {
				return
			}
			pe := EvalPresetEstimate{EvalPreset: p, Runs: est.Runs, EstCostUSD: est.EstCostUSD, EstMinutes: est.EstMinutes,
				MDEpp: est.Power.Overall.MDEpp, FromLibrary: est.FromLibrary}
			pe.Faults = r.Faults
			pe.MDEPerBenchmarkPP = worstMDE(est.Power.PerBenchmark)
			results[i] = &pe
		}()
	}
	wg.Wait()
	out := make([]EvalPresetEstimate, 0, len(EvaluationPresets))
	for _, pe := range results {
		if pe != nil {
			out = append(out, *pe)
		}
	}
	return out
}

// presetParallel is how many presets are estimated at once.
const presetParallel = 3

// presetRequest is req sized as preset p, with its own copies of everything
// the estimate may write (normalizeEvalRequest trims the arms in place), so
// presets estimated side by side never share them.
func presetRequest(req *EvalEstimateRequest, p EvalPreset) *EvalEstimateRequest {
	r := *req
	r.Presets, r.TasksPerBenchmark, r.Trials = false, p.TasksPerBenchmark, p.Trials
	r.Arms = slices.Clone(req.Arms)
	r.Benchmarks = slices.Clone(req.Benchmarks)
	if req.Stream != nil {
		st := *req.Stream
		r.Stream = &st
	}
	if req.Failover != nil { // normalizeFailover trims the fallbacks in place
		f := *req.Failover
		f.Fallbacks = slices.Clone(req.Failover.Fallbacks)
		r.Failover = &f
	}
	r.Faults = slices.Clone(req.Faults)
	for _, f := range p.Faults {
		if !slices.Contains(r.Faults, f) {
			r.Faults = append(r.Faults, f)
		}
	}
	return &r
}

// worstMDE is the largest per-benchmark smallest detectable change, nil
// when any benchmark cannot decide at all or none runs.
func worstMDE(rows []EvalPowerBenchmark) *float64 {
	var worst *float64
	for _, b := range rows {
		if b.MDEpp == nil {
			return nil
		}
		if worst == nil || *b.MDEpp > *worst {
			v := *b.MDEpp
			worst = &v
		}
	}
	return worst
}

// evalSlots is how many runs go at once: concurrency per benchmark times
// the benchmarks that run, at most the org's worker slots when known.
func evalSlots(req *EvalEstimateRequest, benchmarks int) EvalSlots {
	c := evalConcurrency(schemas.EvaluationSpec{Concurrency: req.Concurrency})
	s := EvalSlots{Concurrency: c, OrgSlots: req.orgSlots, Parallel: c * max(1, benchmarks)}
	if s.OrgSlots > 0 {
		s.Concurrency = min(s.Concurrency, s.OrgSlots)
		s.Parallel = min(s.Parallel, s.OrgSlots)
	}
	return s
}

// orgSlotsAndCap reads the org's worker slots (max concurrent runs) and
// daily spend cap from its quota on a hosted server; zeros elsewhere or
// when the quota cannot be read (the estimate then does not bound them).
func orgSlotsAndCap(ctx context.Context, s Services) (int, float64) {
	if !s.Hosted || s.Quota == nil || s.OrgID == "" {
		return 0, 0
	}
	l, err := s.Quota.Get(ctx, s.OrgID)
	if err != nil {
		return 0, 0
	}
	return l.MaxConcurrentRuns, l.MaxSpendUSDPerDay
}
