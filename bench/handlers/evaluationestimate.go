package handlers

// Agent-first evaluations, lane A07: POST /api/bench/evaluations/estimate
// (AGENTFIRST_CONTRACT.md section 3). It builds the same one experiment per
// benchmark an evaluation would launch (arms from the spec, conditions from
// the faults through package faults, the same N admitted tasks the launch
// picks (evalpick.go), trials) and
// asks the org's forecast (forecast/ over sealed history, the planner and the
// price snapshot) for each. Nothing is written and nothing runs. A cost is
// known only when every benchmark has a history range and the price snapshot
// lists every arm model; otherwise known is false and nothing is guessed.

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/maximhq/bifrost/bench/forecast"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// Evaluation estimate defaults and limits.
const (
	EvalDefaultTasks  = EvaluationDefaultTasks
	EvalDefaultTrials = EvaluationDefaultTrials
	EvalMaxTasks      = 100
	EvalMaxTrials     = 20
	EvalMaxArms       = 2
)

// EvalEstimateArm is one arm of the evaluation spec, as the contract's
// EvaluationArm (decoded locally so this lane does not depend on the
// schemas.EvaluationSpec of another lane).
type EvalEstimateArm struct {
	Label    string `json:"label"`
	Agent    string `json:"agent"`
	Version  int    `json:"version,omitempty"`
	Model    string `json:"model"`
	Provider string `json:"provider,omitempty"`
}

// EvalEstimateRequest is the part of the contract's EvaluationSpec the
// estimate reads. Other fields of the spec are accepted and ignored.
type EvalEstimateRequest struct {
	Name              string            `json:"name"`
	Mode              string            `json:"mode"`
	Arms              []EvalEstimateArm `json:"arms"`
	Benchmarks        []string          `json:"benchmarks"`
	Pack              string            `json:"pack,omitempty"`
	TasksPerBenchmark int               `json:"tasks_per_benchmark"`
	Trials            int               `json:"trials"`
	Faults            []string          `json:"faults"`
	BudgetUSD         float64           `json:"budget_usd,omitempty"`
	// Stream is the evaluation's stream block (memory from task to task).
	// In score mode with memory carried the launch adds a no-memory control
	// arm, and replay runs go at the end of every chain: both are counted.
	Stream *schemas.StreamSpec `json:"stream,omitempty"`
	// Concurrency is the runs of one benchmark that go at once (the spec's
	// concurrency; 0 is EvaluationDefaultConcurrency).
	Concurrency int `json:"concurrency,omitempty"`
	// Presets asks for the Quick look, Standard and Thorough sizes of the
	// same setup too (evaluationpresets.go).
	Presets bool `json:"presets,omitempty"`

	orgSlots int     // the org's worker slots (0: not bounded here)
	dailyCap float64 // the org's daily spend cap (0: none)

	// workerSlots is the runs at once of the worker the evaluation goes to
	// (Services.WorkerSlots, N90; 0: not bounded here).
	workerSlots int
	// BifrostCache adds the cache-on copy of the one arm (cacheOnArm).
	BifrostCache bool `json:"bifrost_cache,omitempty"`
	// Failover is the provider failover option: the launch adds the arm
	// with fallbacks and the primary outage condition (normalizeFailover).
	Failover *schemas.FailoverOption `json:"failover,omitempty"`
	// MCPRoute is how the arms reach the tasks' MCP tool servers
	// (EvaluationSpec.MCPRoute); a Bifrost route on a benchmark without an
	// MCP tool server gets a note.
	MCPRoute string `json:"mcp_route,omitempty"`
	// Worker is where the evaluation runs (EvaluationSpec.Worker: a target
	// or a worker's pool; "" where the organization's runs go). Only the
	// worker slot bound of the estimate reads it (N90).
	Worker string `json:"worker,omitempty"`
}

// evalArmID is the experiment arm id of spec arm i: A, B, C and so on, the
// labels the launch gives arms without one.
func evalArmID(i int) string {
	return string(rune('A' + i))
}

// specView is the evaluation spec the request stands for, as far as the
// experiment's arms go; spec arm i is labelled evalArmID(i), the id the
// estimate's experiment gives it.
func (r *EvalEstimateRequest) specView() schemas.EvaluationSpec {
	spec := schemas.EvaluationSpec{Mode: r.Mode, BifrostCache: r.BifrostCache, Stream: r.Stream, Failover: r.Failover, MCPRoute: r.MCPRoute,
		Faults: r.Faults, Concurrency: r.Concurrency}
	if spec.Mode == "" && len(r.Arms) == 1 {
		spec.Mode = schemas.EvaluationModeScore
	}
	for i, a := range r.Arms {
		spec.Arms = append(spec.Arms, schemas.EvaluationArm{Label: evalArmID(i), Agent: a.Agent, Version: a.Version, Model: a.Model, Provider: a.Provider})
	}
	return spec
}

// armCount is the experiment arms one benchmark runs: the spec arms and
// the variants the launch adds (cache-on copy, failover arm, no-memory
// control), as evalArmCount counts them.
func (r *EvalEstimateRequest) armCount() int {
	return evalArmCount(r.specView())
}

// EvalCostRange is a cost range in USD. Known false means at least one part
// has no history or no official price; Low and High then cover only what is
// known (High may be an upper bound), never a guess.
type EvalCostRange struct {
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
	Known bool    `json:"known"`
	// Reason says why the cost is not known; empty when it is.
	Reason string `json:"reason,omitempty"`
}

// EvalMinutesRange is a wall clock range in minutes, with the same meaning
// of Known.
type EvalMinutesRange struct {
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
	Known bool    `json:"known"`
}

// EvalBenchmarkEstimate is one benchmark's part of the estimate.
type EvalBenchmarkEstimate struct {
	Benchmark  string           `json:"benchmark"`
	Runs       int              `json:"runs"`
	Tasks      int              `json:"tasks"`
	Conditions []string         `json:"conditions"`
	FaultsSkip []faults.Skip    `json:"faults_skipped"`
	EstCostUSD EvalCostRange    `json:"est_cost_usd"`
	EstMinutes EvalMinutesRange `json:"est_minutes"`
	Basis      string           `json:"basis,omitempty"`
	// EstimateBasis is "list_price" when some arm has no run history at all
	// and its cost is a rough guess: its agent's typical tokens per run times
	// the model's list price (forecast basis agent_tokens). Empty otherwise.
	EstimateBasis string   `json:"estimate_basis,omitempty"`
	Skipped       string   `json:"skipped,omitempty"` // the benchmark cannot run at all
	Notes         []string `json:"notes"`
	Concurrency   int      `json:"concurrency,omitempty"`
	// FromLibrary is true when some arm has no history of its own and its
	// cost and time come from other agents' runs on this benchmark.
	FromLibrary bool `json:"from_library,omitempty"`

	agentLow, agentHigh float64 // summed agent seconds, for the evaluation's wall time
}

// EvalEstimate is the answer of POST /evaluations/estimate.
type EvalEstimate struct {
	Runs         int                     `json:"runs"`
	EstCostUSD   EvalCostRange           `json:"est_cost_usd"`
	EstMinutes   EvalMinutesRange        `json:"est_minutes"`
	PerBenchmark []EvalBenchmarkEstimate `json:"per_benchmark"`
	Power        EvalPower               `json:"power"`
	Notes        []string                `json:"notes"`
	// UnpricedModels are the arm models the price snapshot does not list:
	// the estimate has no cost for them (never $0).
	UnpricedModels []string `json:"unpriced_models"`
	// Budget is the cap that applies, the suggested one and the runs a cap
	// below the expected cost would leave unstarted (evaluationbudget.go).
	Budget EvalBudget `json:"budget"`
	// Slots says how many runs go side by side and why.
	Slots EvalSlots `json:"slots"`
	// FromLibrary is true when some benchmark's estimate comes from library
	// runs (a first run of this agent); it is refined from the org's own
	// runs once they exist.
	FromLibrary bool `json:"from_library,omitempty"`
	// Presets are the preset sizes of the same setup, when asked for.
	Presets []EvalPresetEstimate `json:"presets,omitempty"`
}

// EvalSlots is how many runs of the evaluation go at once: Concurrency per
// benchmark, Parallel over all its benchmarks, bounded by OrgSlots (the
// org's worker slots on the shared worker, 0 when this server does not
// bound them).
type EvalSlots struct {
	Concurrency int `json:"concurrency"`
	OrgSlots    int `json:"org_slots"`
	Parallel    int `json:"parallel"`
	// WorkerSlots is how many runs the worker the evaluation goes to runs
	// at once (N90: the org's live runners of the target's pool); 0 when
	// unknown or not bounded (a sandbox platform, local mode).
	WorkerSlots int `json:"worker_slots,omitempty"`
}

// Power check settings: the assumed per-task SD of the paired difference
// (verdict/pairedsignflip.go default), the test level, the power, and the
// change the advice aims at.
const (
	EvalPowerSD     = 0.30
	EvalPowerAlpha  = 0.05
	EvalPowerPower  = 0.8
	EvalPowerTarget = 0.10
	evalPowerMaxN   = 10000
)

// EvalPowerBenchmark is one benchmark's smallest detectable change, in
// percentage points, at alpha 0.05 split over the benchmarks (Holm worst
// case). Decidable is false when the task count cannot reach that level at
// all with the paired sign-flip test; MDEpp is then null.
type EvalPowerBenchmark struct {
	Benchmark string   `json:"benchmark"`
	Tasks     int      `json:"tasks"`
	Decidable bool     `json:"decidable"`
	MDEpp     *float64 `json:"mde_pp"`
}

// EvalPowerOverall is the pooled test over every task at alpha 0.05.
type EvalPowerOverall struct {
	Tasks int      `json:"tasks"`
	MDEpp *float64 `json:"mde_pp"`
}

// EvalPower is the power check of the estimate (RESEARCH_R4 R3). The unit
// is the task: trials steady each task's score but do not add tasks.
type EvalPower struct {
	PerBenchmark []EvalPowerBenchmark `json:"per_benchmark"`
	Overall      EvalPowerOverall     `json:"overall"`
	SDSource     string               `json:"sd_source"`
	Advice       string               `json:"advice"`
}

func mdePP(n int, alpha float64) *float64 {
	m := stats.MDE(n, EvalPowerSD, alpha, EvalPowerPower)
	if math.IsNaN(m) || math.IsInf(m, 0) {
		return nil
	}
	v := math.Round(m*1000) / 10
	return &v
}

// evalPower computes the power check over the benchmarks that run.
func evalPower(per []EvalBenchmarkEstimate, trials int) EvalPower {
	p := EvalPower{PerBenchmark: []EvalPowerBenchmark{}, SDSource: "assumed"}
	var run []EvalBenchmarkEstimate
	for _, b := range per {
		if b.Skipped == "" && b.Tasks > 0 {
			run = append(run, b)
		}
	}
	if len(run) == 0 {
		p.Advice = "No benchmark can run, so nothing can be detected."
		return p
	}
	alphaB := EvalPowerAlpha / float64(len(run))
	for _, b := range run {
		row := EvalPowerBenchmark{Benchmark: b.Benchmark, Tasks: b.Tasks, Decidable: stats.MinPossibleP(b.Tasks) <= alphaB}
		if row.Decidable {
			row.MDEpp = mdePP(b.Tasks, alphaB)
		}
		p.PerBenchmark = append(p.PerBenchmark, row)
		p.Overall.Tasks += b.Tasks
	}
	p.Overall.MDEpp = mdePP(p.Overall.Tasks, EvalPowerAlpha)
	target := int(EvalPowerTarget * 100)
	var advice []string
	if p.Overall.MDEpp != nil && *p.Overall.MDEpp <= EvalPowerTarget*100 {
		advice = append(advice, fmt.Sprintf("Enough tasks to detect a %d point change overall.", target))
	} else if need := stats.TasksNeeded(EvalPowerTarget, EvalPowerSD, EvalPowerAlpha, EvalPowerPower, 2, evalPowerMaxN); need > p.Overall.Tasks {
		advice = append(advice, fmt.Sprintf("Add %d tasks to detect a %d point change overall.", need-p.Overall.Tasks, target))
	}
	if trials > 2 {
		advice = append(advice, "More tasks narrow the margin faster than more trials: trials steady each task's score but do not add tasks.")
	}
	p.Advice = strings.Join(advice, " ")
	return p
}

// modelPricer is the optional ForecastSource method that says whether the
// price snapshot lists a model (cmd/bifrost-bench serveForecast).
type modelPricer interface {
	Priced(model string) bool
}

func init() {
	RegisterOrgArea("evaluationestimate", func(env OrgEnv, r *router.Router) {
		h := &evalEstimateHandler{env: env, forecast: func() ForecastSource { return quickRunForecast(env.Cfg.Root) }, cache: newForecastCache()}
		r.POST(APIPrefix+"/evaluations/estimate", env.W(h.post))
	})
}

type evalEstimateHandler struct {
	env      OrgEnv
	forecast func() ForecastSource
	cache    *forecastCache // forecasts by experiment, agent configs and suite stamp (evalestimatecache.go)
}

func (h *evalEstimateHandler) post(ctx *fasthttp.RequestCtx) {
	if h.env.Services.Suites == nil {
		sendUnavailable(ctx, h.env.Mode, "benchmarks")
		return
	}
	var req EvalEstimateRequest
	if err := json.Unmarshal(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse evaluation: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	var src ForecastSource
	if h.forecast != nil {
		src = h.forecast()
	}
	agents := (&evaluationsHandler{env: h.env}).agentConfigs(work)
	root := ""
	if p := h.env.Services.Projects; p != nil {
		root = p.Root()
	} else if h.env.Cfg != nil {
		root = h.env.Cfg.Root
	}
	req.orgSlots, req.dailyCap = orgSlotsAndCap(work, h.env.Services)
	req.workerSlots = workerSlotsOf(work, h.env.Services, req.Worker)
	est, err := estimateEvaluation(work, h.env.Services.Suites, src, &req, agents, root, h.cache)
	if err != nil {
		if errors.Is(err, errQuickBad) {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
			return
		}
		sendDepError(ctx, h.env.Mode, "estimate evaluation", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, plainServed(est))
}

// EstimateEvaluation validates req, resolves its benchmarks and estimates
// each through src. A nil src gives runs and unknown costs. A bad request
// wraps errQuickBad.
func EstimateEvaluation(ctx context.Context, suites SuiteSource, src ForecastSource, req *EvalEstimateRequest) (*EvalEstimate, error) {
	return EstimateEvaluationWith(ctx, suites, src, req, nil, "")
}

// EstimateEvaluationWith is EstimateEvaluation with the org's agent configs
// (by path, agents/<id>.yaml) and the bench root, so a benchmark whose tasks
// bring their own image is estimated with the task-image variant of each
// agent the evaluation would launch, or skipped as it would be.
func EstimateEvaluationWith(ctx context.Context, suites SuiteSource, src ForecastSource, req *EvalEstimateRequest, agents map[string]*schemas.AgentConfig, root string) (*EvalEstimate, error) {
	return estimateEvaluation(ctx, suites, src, req, agents, root, nil)
}

// estimateParallel is how many benchmarks one estimate forecasts at once.
const estimateParallel = 4

// estimateEvaluation is EstimateEvaluationWith with an optional forecast
// cache; the benchmarks are estimated side by side, in request order.
func estimateEvaluation(ctx context.Context, suites SuiteSource, src ForecastSource, req *EvalEstimateRequest, agents map[string]*schemas.AgentConfig, root string, cache *forecastCache) (*EvalEstimate, error) {
	if err := normalizeEvalRequest(req); err != nil {
		return nil, err
	}
	ids, notes, err := evalBenchmarks(ctx, suites, req)
	if err != nil {
		return nil, err
	}
	out := &EvalEstimate{PerBenchmark: []EvalBenchmarkEstimate{}, Notes: notes, UnpricedModels: []string{}}
	if src == nil {
		out.Notes = append(out.Notes, "no forecast on this server: run counts only, cost and time unknown")
	}
	pricer, _ := src.(modelPricer)
	if src != nil && pricer == nil {
		out.Notes = append(out.Notes, "the price snapshot is not available on this server, so no cost is known")
	}
	var unpriced []string
	for i, a := range req.Arms {
		if a.Model == "" { // J2: the agent's own model, which it reports itself
			if agents != nil {
				path, _ := normalizeAgent(a.Agent)
				if cfg := agents[path]; cfg != nil && !utils.OwnsModel(cfg) {
					return nil, badf("arms[%d]: pick a model: agent %s runs on the model you give it", i, a.Agent)
				}
			}
			if !slices.Contains(unpriced, "") {
				unpriced = append(unpriced, "")
				out.Notes = append(out.Notes, "the agent picks its own model, so its cost is reported by the agent and not estimated here")
			}
			continue
		}
		if pricer == nil || !pricer.Priced(a.Model) {
			if !slices.Contains(unpriced, a.Model) {
				unpriced = append(unpriced, a.Model)
			}
		}
	}
	if pricer != nil {
		out.UnpricedModels = append(out.UnpricedModels, unpriced...)
		for _, m := range unpriced {
			if m == "" {
				continue
			}
			out.Notes = append(out.Notes, fmt.Sprintf("model %s has no official price in the price snapshot, so its cost is not known; set a price on its key in Settings > Model keys to cost its finished runs", m))
		}
	}
	per := make([]EvalBenchmarkEstimate, len(ids))
	var wg sync.WaitGroup
	sem := make(chan struct{}, estimateParallel)
	for i, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			per[i] = estimateBenchmark(ctx, suites, src, req, id, agents, root, cache)
		}()
	}
	wg.Wait()
	allKnown, timeKnown, ran := true, true, false
	var agentLow, agentHigh float64
	running := 0
	for _, be := range per {
		if be.Skipped == "" {
			ran = true
			if len(unpriced) > 0 {
				be.EstCostUSD.Known = false
				be.EstCostUSD.Reason = priceReason(unpriced)
			} else if !be.EstCostUSD.Known {
				be.EstCostUSD.Reason = "no run history to estimate from"
			}
			allKnown = allKnown && be.EstCostUSD.Known
			timeKnown = timeKnown && be.EstMinutes.Known
			out.Runs += be.Runs
			out.EstCostUSD.Low += be.EstCostUSD.Low
			out.EstCostUSD.High += be.EstCostUSD.High
			agentLow, agentHigh = agentLow+be.agentLow, agentHigh+be.agentHigh
			out.FromLibrary = out.FromLibrary || be.FromLibrary
			running++
		}
		out.PerBenchmark = append(out.PerBenchmark, be)
	}
	out.EstCostUSD.Known = ran && allKnown
	switch {
	case out.EstCostUSD.Known:
	case src == nil:
		out.EstCostUSD.Reason = "no forecast on this server"
	case pricer == nil:
		out.EstCostUSD.Reason = "no price snapshot on this server"
	case len(unpriced) > 0:
		out.EstCostUSD.Reason = priceReason(unpriced)
	case !ran:
		out.EstCostUSD.Reason = "no benchmark can run"
	default:
		out.EstCostUSD.Reason = "some benchmarks have no run history to estimate from"
	}
	out.Power = evalPower(out.PerBenchmark, req.Trials)
	out.Slots = evalSlots(req, running)
	if p := float64(max(1, out.Slots.Parallel)); ran {
		out.EstMinutes.Low, out.EstMinutes.High = agentLow/p/60, agentHigh/p/60
	}
	out.EstMinutes.Known = ran && timeKnown
	if out.FromLibrary {
		out.Notes = append(out.Notes, "First run, estimate from library data: other agents' finished runs on the same tasks, repriced for your model. Your agent may differ, so the range is wide; it narrows from your own runs after this evaluation.")
	}
	out.Budget = estimateBudget(out, req.BudgetUSD, req.dailyCap)
	if req.Presets {
		out.Presets = estimatePresets(ctx, suites, src, req, agents, root, cache)
		samePresetAsRequest(out, req)
	}
	if !ran {
		out.Notes = append(out.Notes, "no benchmark of this evaluation can run")
	}
	return out, nil
}

func normalizeEvalRequest(req *EvalEstimateRequest) error {
	if req.Mode == schemas.EvaluationModeModelSwitch { // Z21: up to EvaluationModelSwitchMaxArms models
		if len(req.Arms) < 2 || len(req.Arms) > schemas.EvaluationModelSwitchMaxArms {
			return badf("arms: mode model_switch takes 2 to %d models, got %d", schemas.EvaluationModelSwitchMaxArms, len(req.Arms))
		}
	} else if len(req.Arms) == 0 || len(req.Arms) > EvalMaxArms {
		return badf("arms: need 1 or %d arms, got %d", EvalMaxArms, len(req.Arms))
	}
	for i := range req.Arms {
		a := &req.Arms[i]
		a.Model, a.Provider = strings.TrimSpace(a.Model), strings.TrimSpace(a.Provider)
		if a.Model != "" && !quickModelRe.MatchString(a.Model) { // J2: blank is the agent's own model
			return badf("arms[%d].model %q is not a valid model name", i, a.Model)
		}
		if a.Version < 0 {
			return badf("arms[%d].version must not be negative", i)
		}
		if _, err := normalizeAgent(a.Agent); err != nil {
			return fmt.Errorf("arms[%d]: %w", i, err)
		}
	}
	if req.TasksPerBenchmark == 0 {
		req.TasksPerBenchmark = EvalDefaultTasks
	}
	if req.Trials == 0 {
		req.Trials = EvalDefaultTrials
	}
	if req.TasksPerBenchmark < 1 || req.TasksPerBenchmark > EvalMaxTasks {
		return badf("tasks_per_benchmark must be between 1 and %d", EvalMaxTasks)
	}
	if req.Trials < 1 || req.Trials > EvalMaxTrials {
		return badf("trials must be between 1 and %d", EvalMaxTrials)
	}
	if req.Stream != nil {
		if req.Stream.FreshArm != "" {
			return badf("stream.fresh_arm is chosen by the evaluation: in score mode a no-memory control of the agent is added")
		}
		if err := req.Stream.Validate(nil); err != nil {
			return badf("%v", err)
		}
	}
	spec := req.specView()
	if err := normalizeFailover(&spec); err != nil { // D6: as the launch: the failover arm and the primary outage
		return err
	}
	req.Failover, req.Faults = spec.Failover, spec.Faults
	if err := normalizeMCPRoute(&spec); err != nil {
		return err
	}
	req.MCPRoute = spec.MCPRoute
	if req.BudgetUSD < 0 {
		return badf("budget_usd must not be negative")
	}
	if req.Concurrency < 0 || req.Concurrency > EvaluationMaxConcurrency {
		return badf("concurrency must be between 1 and %d (0 is the default, %d)", EvaluationMaxConcurrency, EvaluationDefaultConcurrency)
	}
	if len(req.Benchmarks) == 0 && strings.TrimSpace(req.Pack) == "" {
		return badf("pick at least one benchmark or a pack")
	}
	return nil
}

// evalPacks is a temporary copy of the contract's pack table (A03 owns the
// real one); ids ending in "*" match by prefix. all-compatible is every
// benchmark here: capability filtering is the evaluation planner's job.
var evalPacks = map[string][]string{
	"coding":   {"hd-harbor-tb2", "hd-swe-verified", "hd-swe-rebench", "hd-swe-ib-lcb"},
	"terminal": {"hd-harbor-tb2", "hd-harbor-skills"},
	"support":  {"tau2*", "mcpmark*", "agentdojo-*"},
	"memory":   {"longmemeval*", "locomo*"},
	"research": {"gaia*", "browsecomp-plus-*"},
	"voice":    {"voicebench*"},
	"safety":   {"agentdojo-*", "hd-swe-ib-swe"},
}

// evalBenchmarks is the explicit benchmark list, else the pack's benchmarks
// present on this server, deduplicated in order.
func evalBenchmarks(ctx context.Context, suites SuiteSource, req *EvalEstimateRequest) ([]string, []string, error) {
	var out, notes []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id != "" && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	for _, b := range req.Benchmarks {
		add(b)
	}
	if len(out) > 0 {
		return out, notes, nil
	}
	pack := strings.TrimSpace(req.Pack)
	present, err := suites.SuiteIDs(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list benchmarks: %w", err)
	}
	sort.Strings(present)
	if pack == "all-compatible" {
		for _, id := range present {
			add(id)
		}
	} else {
		pats, ok := evalPacks[pack]
		if !ok {
			return nil, nil, badf("pack %q is not a pack", pack)
		}
		for _, p := range pats {
			for _, id := range present {
				if id == p || (strings.HasSuffix(p, "*") && strings.HasPrefix(id, strings.TrimSuffix(p, "*"))) {
					add(id)
				}
			}
		}
	}
	if len(out) == 0 {
		notes = append(notes, fmt.Sprintf("pack %s has no benchmark on this server", pack))
	}
	return out, notes, nil
}

// estimateBenchmark estimates one benchmark. Problems become Skipped or
// notes, never an error.
func estimateBenchmark(ctx context.Context, suites SuiteSource, src ForecastSource, req *EvalEstimateRequest, id string, agents map[string]*schemas.AgentConfig, root string, cache *forecastCache) EvalBenchmarkEstimate {
	be := EvalBenchmarkEstimate{Benchmark: id, Conditions: []string{}, FaultsSkip: []faults.Skip{}, Notes: []string{}}
	suiteID, _, _ := strings.Cut(id, "@")
	st, err := suites.LoadSuite(ctx, suiteID)
	if err != nil {
		be.Skipped = "cannot load benchmark: " + err.Error()
		return be
	}
	cfgs := make([]*schemas.AgentConfig, len(req.Arms))
	for i, a := range req.Arms {
		path, _ := normalizeAgent(a.Agent)
		cfgs[i] = agents[path]
	}
	tasks, why := evalPickTasks(st, schemas.SplitPublic, req.TasksPerBenchmark, cfgs) // the same tasks the launch runs (J5 included)
	if len(tasks) == 0 {
		be.Skipped = "benchmark has no admitted task"
		if why != "" {
			be.Skipped = why
		}
		return be
	}
	if why != "" {
		be.Notes = append(be.Notes, "some tasks are skipped: "+why)
	}
	if req.MCPRoute != "" && !suiteHasMCPServer(st) { // D4.toolfaults: the route changes nothing here
		be.Notes = append(be.Notes, fmt.Sprintf("The MCP route does not apply to %s: it has no MCP tool server.", id))
	}
	sets := make([]map[string]any, len(req.Arms))
	for i, a := range req.Arms {
		path, _ := normalizeAgent(a.Agent)
		v := deriveTaskImageAgent(agents[path], st, tasks, agents, root)
		if v.Skip != "" {
			be.Skipped = v.Skip
			return be
		}
		if v.Set != nil {
			sets[i] = v.Set
			be.Notes = append(be.Notes, v.Note)
		}
	}
	conds, skipped := faults.Conditions(req.Faults, st)
	be.Tasks, be.Conditions = len(tasks), conds
	if skipped != nil {
		be.FaultsSkip = skipped
	}
	chains := req.armCount() * len(conds) * req.Trials
	be.Runs = chains * len(tasks)
	if req.Stream != nil && req.Stream.Replay > 0 { // stream replay: more runs at the end of every chain (engine/streamplan.go)
		be.Runs += chains * min(req.Stream.Replay, len(tasks))
	}
	if src == nil {
		be.Notes = append(be.Notes, "no forecast on this server")
		return be
	}
	exp := evalExperiment(st, req, tasks, conds, sets)
	f, err := cache.estimate(ctx, src, exp, forecastKey(root, st, exp, req, agents))
	if err != nil {
		be.Notes = append(be.Notes, estimateRefusedNote(err))
		return be
	}
	applyForecast(&be, f, slices.Sorted(maps.Keys(exp.Arms)), req.orgSlots)
	return be
}

// evalAdmittedTasks are the admitted task ids of s, sorted; every task when
// s has no lock (the planner then refuses, which the forecast reports).
func evalAdmittedTasks(st *schemas.Suite) []string {
	return admittedTasks(st, schemas.SplitPublic).IDs
}

// evalExperiment is the experiment an evaluation would launch on st: one
// arm per spec arm, so a single setup is one arm scored on its own and the
// estimate counts exactly the runs the launch plans, with the MCP route and
// the task-image variant set of each arm (sets, by spec arm; nil entries
// add nothing), plus the variants a score evaluation adds
// (withVariantArms: the cache-on copy, the failover arm, the no-memory
// control of a stream with memory carried), the first of them as arm B.
func evalExperiment(st *schemas.Suite, req *EvalEstimateRequest, tasks, conds []string, sets []map[string]any) *schemas.Experiment {
	arms := map[string]schemas.ArmSpec{}
	base := ""
	for i, a := range req.Arms {
		agent, _ := normalizeAgent(a.Agent)
		if a.Version > 0 {
			agent += fmt.Sprintf("@v%d", a.Version)
		}
		if i == 0 {
			base = agent
		}
		arm := evalArmFor(a.Provider, a.Model)
		if id := strings.TrimSuffix(strings.TrimPrefix(a.Agent, "agents/"), ".yaml"); id == utils.BareModelAgent {
			arm = bareModelArm(arm, id, a.Provider, a.Model)
		}
		arm.Agent = agent
		if a.Label != "" {
			arm.Label = a.Label
		}
		arm = withSet(arm, mcpRouteSet(req.MCPRoute))
		if i < len(sets) && sets[i] != nil {
			arm = withSet(arm, sets[i])
		}
		arms[evalArmID(i)] = arm
	}
	arms, fresh := withVariantArms(req.specView(), arms)
	var stream *schemas.StreamSpec
	if req.Stream != nil {
		st := *req.Stream
		st.FreshArm = fresh
		stream = &st
	}
	cs := make([]schemas.ConditionSpec, 0, len(conds))
	for _, c := range conds {
		cs = append(cs, schemas.ConditionSpec{ID: c, Tasks: schemas.TaskSelector{IDs: append([]string(nil), tasks...)}, Trials: req.Trials})
	}
	suite := st.ID
	if st.Version != "" {
		suite += "@" + st.Version
	}
	return &schemas.Experiment{
		Name: "estimate-" + st.ID, Suite: suite, Base: base, Arms: arms,
		Primary:    schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:     []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions: cs,
		// No budget: the cost forecast does not depend on it (estimateBudget
		// compares the cap after), so every budget shares one forecast and the
		// wizard's steps show one range for the same setup.
		Verdict:      schemas.VerdictSpec{Policy: "paired_signflip"},
		Concurrency:  evalConcurrency(schemas.EvaluationSpec{Concurrency: req.Concurrency}),
		ManifestMode: "agent_exposure",
		Stream:       stream,
	}
}

// applyForecast reads the ranges of the experiment's arms (want, by id) out
// of f. Cost and time are known only when every arm has a history range.
func applyForecast(be *EvalBenchmarkEstimate, f *schemas.Forecast, want []string, slots int) {
	if f == nil {
		be.Notes = append(be.Notes, "the forecast returned nothing")
		return
	}
	be.Basis = f.Basis
	be.Concurrency = max(1, f.Concurrency)
	if slots > 0 {
		be.Concurrency = min(be.Concurrency, slots)
	}
	be.Notes = append(be.Notes, f.Notes...)
	nArms := len(want)
	costKnown, timeKnown, found := true, true, 0
	var agentLow, agentHigh float64
	for _, a := range f.Arms {
		if !slices.Contains(want, a.Arm) {
			continue
		}
		found++
		if a.Basis == schemas.BasisLibrary {
			be.FromLibrary = true
			be.Notes = append(be.Notes, fmt.Sprintf("arm %s: %s", a.Arm, a.BasisText))
		}
		if string(a.Basis) == forecast.BasisAgentTokens {
			be.EstimateBasis = "list_price"
			be.Notes = append(be.Notes, fmt.Sprintf("arm %s: %s", a.Arm, a.BasisText))
		}
		lo, hi, ok := rangeBounds(a.CostUSD)
		be.EstCostUSD.Low += lo
		be.EstCostUSD.High += hi
		costKnown = costKnown && ok
		if !ok && a.CostUSD.Reason != "" {
			be.Notes = append(be.Notes, fmt.Sprintf("arm %s cost: %s", cmp.Or(a.Label, a.Arm), a.CostUSD.Reason))
		}
		lo, hi, ok = rangeBounds(a.AgentTimeS)
		agentLow += lo
		agentHigh += hi
		timeKnown = timeKnown && ok
	}
	if found < nArms {
		costKnown, timeKnown = false, false
		be.Notes = append(be.Notes, "the forecast has no estimate for every arm")
	}
	be.EstCostUSD.Known = costKnown
	c := float64(be.Concurrency)
	be.agentLow, be.agentHigh = agentLow, agentHigh
	be.EstMinutes = EvalMinutesRange{Low: agentLow / c / 60, High: agentHigh / c / 60, Known: timeKnown}
}

// rangeBounds is a forecast range as low and high: a history range is known;
// an upper bound gives only its high end; unknown gives zeros.
func rangeBounds(r schemas.ForecastRange) (float64, float64, bool) {
	var lo, hi float64
	if r.Low != nil {
		lo = *r.Low
	}
	if r.High != nil {
		hi = *r.High
	}
	switch r.Kind {
	case schemas.RangeHistory:
		return lo, hi, r.Low != nil && r.High != nil
	case schemas.RangeUpperBound:
		return 0, hi, false
	}
	return 0, 0, false
}
