package handlers

// Agent-first evaluations, lane A07: POST /api/bench/evaluations/estimate
// (AGENTFIRST_CONTRACT.md section 3). It builds the same one experiment per
// benchmark an evaluation would launch (arms from the spec, conditions from
// the faults through package faults, the first N admitted tasks, trials) and
// asks the org's forecast (forecast/ over sealed history, the planner and the
// price snapshot) for each. Nothing is written and nothing runs. A cost is
// known only when every benchmark has a history range and the price snapshot
// lists every arm model; otherwise known is false and nothing is guessed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// Evaluation estimate defaults and limits.
const (
	EvalDefaultTasks  = 8
	EvalDefaultTrials = 2
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
}

// EvalCostRange is a cost range in USD. Known false means at least one part
// has no history or no official price; Low and High then cover only what is
// known (High may be an upper bound), never a guess.
type EvalCostRange struct {
	Low   float64 `json:"low"`
	High  float64 `json:"high"`
	Known bool    `json:"known"`
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
	Benchmark   string           `json:"benchmark"`
	Runs        int              `json:"runs"`
	Tasks       int              `json:"tasks"`
	Conditions  []string         `json:"conditions"`
	FaultsSkip  []faults.Skip    `json:"faults_skipped"`
	EstCostUSD  EvalCostRange    `json:"est_cost_usd"`
	EstMinutes  EvalMinutesRange `json:"est_minutes"`
	Basis       string           `json:"basis,omitempty"`
	Skipped     string           `json:"skipped,omitempty"` // the benchmark cannot run at all
	Notes       []string         `json:"notes"`
	Concurrency int              `json:"concurrency,omitempty"`
}

// EvalEstimate is the answer of POST /evaluations/estimate.
type EvalEstimate struct {
	Runs         int                     `json:"runs"`
	EstCostUSD   EvalCostRange           `json:"est_cost_usd"`
	EstMinutes   EvalMinutesRange        `json:"est_minutes"`
	PerBenchmark []EvalBenchmarkEstimate `json:"per_benchmark"`
	Notes        []string                `json:"notes"`
}

// modelPricer is the optional ForecastSource method that says whether the
// price snapshot lists a model (cmd/bifrost-bench serveForecast).
type modelPricer interface {
	Priced(model string) bool
}

func init() {
	RegisterOrgArea("evaluationestimate", func(env OrgEnv, r *router.Router) {
		h := &evalEstimateHandler{env: env, forecast: func() ForecastSource { return quickRunForecast(env.Cfg.Root) }}
		r.POST(APIPrefix+"/evaluations/estimate", env.W(h.post))
	})
}

type evalEstimateHandler struct {
	env      OrgEnv
	forecast func() ForecastSource
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
	est, err := EstimateEvaluationWith(work, h.env.Services.Suites, src, &req, agents, root)
	if err != nil {
		if errors.Is(err, errQuickBad) {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
			return
		}
		sendDepError(ctx, h.env.Mode, "estimate evaluation", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, est)
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
	if err := normalizeEvalRequest(req); err != nil {
		return nil, err
	}
	ids, notes, err := evalBenchmarks(ctx, suites, req)
	if err != nil {
		return nil, err
	}
	out := &EvalEstimate{PerBenchmark: []EvalBenchmarkEstimate{}, Notes: notes}
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
		for _, m := range unpriced {
			if m == "" {
				continue
			}
			out.Notes = append(out.Notes, fmt.Sprintf("model %s has no official price in the price snapshot, so its cost is unknown", m))
		}
	}
	allKnown, timeKnown, ran := true, true, false
	for _, id := range ids {
		be := estimateBenchmark(ctx, suites, src, req, id, agents, root)
		if be.Skipped == "" {
			ran = true
			if len(unpriced) > 0 {
				be.EstCostUSD.Known = false
			}
			allKnown = allKnown && be.EstCostUSD.Known
			timeKnown = timeKnown && be.EstMinutes.Known
			out.Runs += be.Runs
			out.EstCostUSD.Low += be.EstCostUSD.Low
			out.EstCostUSD.High += be.EstCostUSD.High
			out.EstMinutes.Low += be.EstMinutes.Low
			out.EstMinutes.High += be.EstMinutes.High
		}
		out.PerBenchmark = append(out.PerBenchmark, be)
	}
	out.EstCostUSD.Known = ran && allKnown
	out.EstMinutes.Known = ran && timeKnown
	if !ran {
		out.Notes = append(out.Notes, "no benchmark of this evaluation can run")
	}
	return out, nil
}

func normalizeEvalRequest(req *EvalEstimateRequest) error {
	if len(req.Arms) == 0 || len(req.Arms) > EvalMaxArms {
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
	if req.BudgetUSD < 0 {
		return badf("budget_usd must not be negative")
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
func estimateBenchmark(ctx context.Context, suites SuiteSource, src ForecastSource, req *EvalEstimateRequest, id string, agents map[string]*schemas.AgentConfig, root string) EvalBenchmarkEstimate {
	be := EvalBenchmarkEstimate{Benchmark: id, Conditions: []string{}, FaultsSkip: []faults.Skip{}, Notes: []string{}}
	suiteID, _, _ := strings.Cut(id, "@")
	st, err := suites.LoadSuite(ctx, suiteID)
	if err != nil {
		be.Skipped = "cannot load benchmark: " + err.Error()
		return be
	}
	tasks := evalAdmittedTasks(st)
	if len(tasks) == 0 {
		be.Skipped = "benchmark has no admitted task"
		return be
	}
	for _, a := range req.Arms { // J5: the tasks the evaluation would pick (launchChild)
		path, _ := normalizeAgent(a.Agent)
		if cfg := agents[path]; cfg != nil {
			var why string
			if tasks, why = compat.RunnableTasks(cfg, st, tasks); why != "" && len(tasks) > 0 {
				be.Notes = append(be.Notes, "some tasks are skipped: "+why)
			}
			if len(tasks) == 0 {
				be.Skipped = why
				return be
			}
		}
	}
	if len(tasks) > req.TasksPerBenchmark {
		tasks = tasks[:req.TasksPerBenchmark]
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
	be.Runs = len(req.Arms) * len(tasks) * len(conds) * req.Trials
	if src == nil {
		be.Notes = append(be.Notes, "no forecast on this server")
		return be
	}
	exp := evalExperiment(st, req, tasks, conds)
	for i, set := range sets {
		if set == nil {
			continue
		}
		for id := range exp.Arms {
			if id == evalArmIDs[i] || (len(req.Arms) == 1 && id == "B") {
				exp.Arms[id] = withSet(exp.Arms[id], set)
			}
		}
	}
	f, err := src.Estimate(ctx, exp)
	if err != nil {
		be.Notes = append(be.Notes, "no forecast: "+err.Error())
		return be
	}
	applyForecast(&be, f, len(req.Arms))
	return be
}

// evalAdmittedTasks are the admitted task ids of s, sorted; every task when
// s has no lock (the planner then refuses, which the forecast reports).
func evalAdmittedTasks(st *schemas.Suite) []string {
	var ids []string
	for _, t := range st.Tasks {
		if st.Lock == nil || summarizeTask(st, t, nil).Admitted {
			ids = append(ids, t.ID)
		}
	}
	if len(ids) == 0 && st.Lock == nil {
		ids = append(ids, st.TaskIDs...)
	}
	sort.Strings(ids)
	return ids
}

// evalArmIDs names the spec arms A, B.
var evalArmIDs = []string{"A", "B"}

// evalExperiment is the experiment an evaluation would launch on st. One
// arm gets a twin B (as QuickRun does) because the planner needs two arms;
// the estimate reads only the spec's arms.
func evalExperiment(st *schemas.Suite, req *EvalEstimateRequest, tasks, conds []string) *schemas.Experiment {
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
		arm.Agent = agent
		if a.Label != "" {
			arm.Label = a.Label
		}
		arms[evalArmIDs[i]] = arm
	}
	if len(req.Arms) == 1 {
		twin := arms["A"]
		set := map[string]any{}
		for k, v := range twin.Set {
			set[k] = v
		}
		set["env.HEIMDALL_QUICKRUN_ARM"] = "B"
		twin.Set, twin.Label = set, twin.Label+" (repeat)"
		arms["B"] = twin
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
		Primary:      schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:       []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions:   cs,
		Budget:       schemas.BudgetSpec{TotalUSD: req.BudgetUSD},
		Verdict:      schemas.VerdictSpec{Policy: "paired_signflip"},
		Concurrency:  2,
		ManifestMode: "agent_exposure",
	}
}

// applyForecast reads the spec arms' ranges out of f. Cost and time are
// known only when every spec arm has a history range.
func applyForecast(be *EvalBenchmarkEstimate, f *schemas.Forecast, nArms int) {
	if f == nil {
		be.Notes = append(be.Notes, "the forecast returned nothing")
		return
	}
	be.Basis = f.Basis
	be.Concurrency = max(1, f.Concurrency)
	be.Notes = append(be.Notes, f.Notes...)
	want := evalArmIDs[:nArms]
	costKnown, timeKnown, found := true, true, 0
	var agentLow, agentHigh float64
	for _, a := range f.Arms {
		if !slices.Contains(want, a.Arm) {
			continue
		}
		found++
		lo, hi, ok := rangeBounds(a.CostUSD)
		be.EstCostUSD.Low += lo
		be.EstCostUSD.High += hi
		costKnown = costKnown && ok
		if !ok && a.CostUSD.Reason != "" {
			be.Notes = append(be.Notes, fmt.Sprintf("arm %s cost: %s", a.Arm, a.CostUSD.Reason))
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
