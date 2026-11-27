// Package forecast estimates what a planned experiment will cost and how
// long it will take, before anything runs (docs/forecast.md). Every number
// comes from sealed history: runs of the same resolved agent config on the
// same suite, else of the same agent id and model. An arm with neither has
// no estimate; when its config sets a per-run usd budget and the price
// snapshot knows its model, the forecast gives that budget times its runs as
// an upper bound and nothing more. "No history, cannot estimate" is a valid
// answer, and the forecast never fills it with a guess.
//
// The package is pure compute over a planned manifest and history records,
// plus a loader (history.go) that reads the history through the store's
// verified sealed-run index.
package forecast

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// MinTaskRuns is how many history runs a task needs before its own runs
// estimate its slots; below that the suite-wide history of the arm's basis
// does.
const MinTaskRuns = 3

// Percentiles of one run that make a range.
const (
	lowQ  = 0.10
	midQ  = 0.50
	highQ = 0.90
)

// HistoryRun is one sealed, graded, non-synthetic run a forecast may rest
// on. A metric that was not observed stays unknown and never counts as zero.
type HistoryRun struct {
	ExpID      string
	Suite      string // id@version
	JudgeHash  string // the suite judge input hash of its manifest; "" when the suite is not judged
	ConfigHash string
	Agent      string
	Model      string
	Task       string
	CostUSD    schemas.Value
	Tokens     schemas.Value
	WallS      schemas.Value
	JudgeUSD   *schemas.Value // nil when the run has no grade to read a judge record from
}

// Input is everything one forecast reads.
type Input struct {
	Manifest    *schemas.Manifest
	History     []HistoryRun
	Priced      func(model string) bool // the price snapshot's lookup; nil knows no model
	PriceRef    string
	Concurrency int // workers the scheduler will run; the experiment's, else the scheduler default
	Notes       []string
	Now         time.Time
	// Library is every sealed run on the suite version, of any agent and
	// model (LoadLibrary), plus the operator's library runs when the server
	// has them. An arm with no history of its own is estimated from it
	// (library.go); nil keeps the old "cannot estimate" answer.
	Library []HistoryRun
	// Reprice is the price ratio range from one model to another (low,
	// mid, high), from the price snapshot; false when either is unpriced.
	// Nil prices library runs only when they ran on the arm's own model.
	Reprice func(from, to string) (lo, mid, hi float64, ok bool)
	// AgentRuns is this server's sealed runs of any suite and model, read
	// for the rough list-price estimate (tokens.go): an arm with no history
	// and no usable library run is estimated from its agent's typical tokens
	// per run times its model's list price. Nil, or a nil TokenPrice, keeps
	// the old answer.
	AgentRuns []HistoryRun
	// TokenPrice is the list price of one token of a model (low, mid, high
	// cover the input to output price spread), from the price snapshot;
	// false when the model has no price.
	TokenPrice func(model string) (lo, mid, hi float64, ok bool)
}

// Estimate forecasts every arm of a planned manifest and the total, and
// says whether it fits budget.total_usd. Per arm the basis is, in order:
// history of the same config hash on the same suite version, else of the
// same agent id and model, else the per-run budget bound, else none. Each
// planned slot is estimated from its task's own history when the task has
// MinTaskRuns or more runs, else from the arm's suite-wide history; the arm's
// low, median and high are the sums of those per-run 10th, 50th and 90th
// percentiles. It is pure.
func Estimate(in Input) *schemas.Forecast {
	m := in.Manifest
	f := &schemas.Forecast{
		ExpID: m.ExpID, Suite: m.SuiteID + "@" + m.SuiteVersion, GeneratedAt: in.Now.UTC(), PriceRef: in.PriceRef,
		Arms: []schemas.ArmForecast{}, Concurrency: max(1, in.Concurrency), BudgetUSD: m.Experiment.Budget.TotalUSD,
		Notes: append([]string{}, in.Notes...),
	}
	tasks := armTasks(m)
	ids := make([]string, 0, len(tasks))
	for id := range tasks {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		arm, ok := m.Arms[id]
		if id == "compat" && m.Compatibility != nil {
			arm, ok = *m.Compatibility, true
		}
		if !ok {
			continue
		}
		f.Arms = append(f.Arms, estimateArm(id, arm, tasks[id], f.Suite, in))
	}
	f.CostUSD = sumRanges(f.Arms, func(a schemas.ArmForecast) schemas.ForecastRange { return a.CostUSD })
	f.AgentTimeS = sumRanges(f.Arms, func(a schemas.ArmForecast) schemas.ForecastRange { return a.AgentTimeS })
	f.WallClockS = scaleRange(f.AgentTimeS, 1/float64(f.Concurrency))
	if f.WallClockS.Kind != schemas.RangeUnknown {
		f.WallClockS.Reason = fmt.Sprintf("agent time over %d concurrent worker%s, no queueing or setup time", f.Concurrency, plural(f.Concurrency))
	}
	f.Fit, f.FitReason = fit(f.CostUSD, f.BudgetUSD)
	f.Judge = estimateJudge(m, len(m.Slots), in.History)
	f.Basis = basisSummary(f.Arms)
	return f
}

// estimateArm forecasts one arm over its planned slot tasks.
func estimateArm(id string, arm schemas.ArmManifest, tasks []string, suite string, in Input) schemas.ArmForecast {
	a := schemas.ArmForecast{
		Arm: id, Label: arm.Label, Agent: arm.Config.ID, Model: arm.Config.Model, ConfigHash: arm.ConfigHash,
		Runs: len(tasks), Experiments: []string{},
	}
	var byHash, byAgent []HistoryRun
	for _, h := range in.History {
		if h.Suite != suite {
			continue
		}
		if h.ConfigHash == arm.ConfigHash && arm.ConfigHash != "" {
			byHash = append(byHash, h)
		}
		if h.Agent == arm.Config.ID && h.Model == arm.Config.Model && arm.Config.ID != "" {
			byAgent = append(byAgent, h)
		}
	}
	hist := byHash
	switch {
	case len(byHash) > 0:
		a.Basis = schemas.BasisConfigHash
	case len(byAgent) > 0:
		a.Basis, hist = schemas.BasisAgentModel, byAgent
	case len(in.Library) > 0:
		if lib, ok := libraryArm(a, arm, tasks, suite, in); ok {
			return lib
		}
		if tok, ok := agentTokensArm(a, arm, in); ok {
			return tok
		}
		return boundArm(a, arm, in)
	default:
		if tok, ok := agentTokensArm(a, arm, in); ok {
			return tok
		}
		return boundArm(a, arm, in)
	}
	a.HistoryRuns, a.Experiments = len(hist), experimentsOf(hist)
	cost := slotRanges(tasks, hist, func(h HistoryRun) schemas.Value { return h.CostUSD })
	tokens := slotRanges(tasks, hist, func(h HistoryRun) schemas.Value { return h.Tokens })
	wall := slotRanges(tasks, hist, func(h HistoryRun) schemas.Value { return h.WallS })
	a.CostUSD, a.Tokens, a.AgentTimeS = cost.total, tokens.total, wall.total
	a.PerRun = schemas.PerRunRanges{CostUSD: cost.perRun, Tokens: tokens.perRun, WallS: wall.perRun}
	a.TaskLevel = cost.taskLevel
	what := "the same config " + shortHash(arm.ConfigHash)
	if a.Basis == schemas.BasisAgentModel {
		what = fmt.Sprintf("agent %s with model %s (other configs)", arm.Config.ID, arm.Config.Model)
	}
	a.BasisText = fmt.Sprintf("%d sealed run%s of %s on %s, in %d experiment%s; %d of %d planned task%s estimated from their own runs",
		len(hist), plural(len(hist)), what, suite, len(a.Experiments), plural(len(a.Experiments)),
		cost.taskLevel, cost.tasks, plural(cost.tasks))
	if len(hist) < 5 {
		a.BasisText += "; thin history, treat the range as rough"
	}
	return a
}

// boundArm is an arm with no history: an upper bound on cost when the
// config sets a per-run usd budget and the price snapshot knows the model,
// an upper bound on agent time from the wall budget, and unknown otherwise.
func boundArm(a schemas.ArmForecast, arm schemas.ArmManifest, in Input) schemas.ArmForecast {
	a.Basis = schemas.BasisNone
	a.BasisText = "no history, cannot estimate: no sealed run of this config or of this agent and model on this suite"
	none := unknownRange("no history, cannot estimate")
	a.CostUSD, a.Tokens, a.AgentTimeS = none, none, none
	a.PerRun = schemas.PerRunRanges{CostUSD: none, Tokens: none, WallS: none}
	priced := in.Priced != nil && in.Priced(arm.Config.Model)
	usd := arm.Config.Budgets.USD
	switch {
	case usd > 0 && priced:
		a.Basis = schemas.BasisBudgetBound
		a.CostUSD = upperRange(usd*float64(a.Runs), fmt.Sprintf("budgets.usd %s per run times %d runs", money(usd), a.Runs))
		a.PerRun.CostUSD = upperRange(usd, "budgets.usd")
		a.BasisText = fmt.Sprintf("no history; upper bound only, from budgets.usd %s per run times %d runs", money(usd), a.Runs)
	case usd > 0:
		a.BasisText += fmt.Sprintf("; budgets.usd is set but the price snapshot has no price for %s, so no bound either", arm.Config.Model)
	}
	if w := arm.Config.Budgets.WallS; w > 0 {
		a.AgentTimeS = upperRange(float64(w*a.Runs), fmt.Sprintf("budgets.wall_s %d per run times %d runs; setup and grading not included", w, a.Runs))
		a.PerRun.WallS = upperRange(float64(w), "budgets.wall_s")
	}
	return a
}

// rangeSet is one metric's slot-summed range, its per-run range, and how
// many distinct planned tasks were estimated from their own history.
type rangeSet struct {
	total, perRun    schemas.ForecastRange
	taskLevel, tasks int
}

// slotRanges sums, over the planned slot tasks, the per-run percentiles of
// each slot's history: its task's own runs when it has MinTaskRuns observed
// values, else the suite-wide runs.
func slotRanges(tasks []string, hist []HistoryRun, metric func(HistoryRun) schemas.Value) rangeSet {
	var all []float64
	byTask := map[string][]float64{}
	unknown := 0
	for _, h := range hist {
		x, ok := metric(h).Float()
		if !ok || math.IsNaN(x) || math.IsInf(x, 0) {
			unknown++
			continue
		}
		all = append(all, x)
		byTask[h.Task] = append(byTask[h.Task], x)
	}
	distinct := map[string]bool{}
	for _, t := range tasks {
		distinct[t] = true
	}
	var rs rangeSet
	rs.tasks = len(distinct)
	if len(all) == 0 {
		reason := fmt.Sprintf("not observed in any of %d history runs", len(hist))
		rs.total, rs.perRun = unknownRange(reason), unknownRange(reason)
		return rs
	}
	for t := range distinct {
		if len(byTask[t]) >= MinTaskRuns {
			rs.taskLevel++
		}
	}
	var lo, mid, hi float64
	for _, t := range tasks {
		dist := all
		if len(byTask[t]) >= MinTaskRuns {
			dist = byTask[t]
		}
		lo, mid, hi = lo+Quantile(dist, lowQ), mid+Quantile(dist, midQ), hi+Quantile(dist, highQ)
	}
	rs.total = historyRange(lo, mid, hi)
	rs.perRun = historyRange(Quantile(all, lowQ), Quantile(all, midQ), Quantile(all, highQ))
	if unknown > 0 {
		note := fmt.Sprintf("%d of %d history runs did not observe it and are left out", unknown, len(hist))
		rs.total.Reason, rs.perRun.Reason = note, note
	}
	return rs
}

// estimateJudge forecasts the judge model's cost for a judged suite from
// past judge records on the same suite version and judge config. A run with
// a grade but no judge record made no judge call and costs nothing; an
// unpriced judge record is left out.
func estimateJudge(m *schemas.Manifest, runs int, hist []HistoryRun) *schemas.JudgeForecast {
	judge, ok := m.Scenario["suite.judge"]
	if !ok {
		return nil
	}
	suite := m.SuiteID + "@" + m.SuiteVersion
	var xs []float64
	for _, h := range hist {
		if h.Suite != suite || h.JudgeHash != judge || h.JudgeUSD == nil {
			continue
		}
		if x, ok := h.JudgeUSD.Float(); ok && !math.IsNaN(x) && !math.IsInf(x, 0) {
			xs = append(xs, x)
		}
	}
	j := &schemas.JudgeForecast{Runs: runs, HistoryRuns: len(xs)}
	if len(xs) == 0 {
		j.CostUSD = unknownRange("no judge records on this suite yet")
		j.BasisText = "judged suite with no priced judge record in its history: judge cost unknown, and never part of the agent cost"
		return j
	}
	n := float64(runs)
	j.CostUSD = historyRange(n*Quantile(xs, lowQ), n*Quantile(xs, midQ), n*Quantile(xs, highQ))
	j.BasisText = fmt.Sprintf("%d past judge record%s on %s times %d runs; reported apart from the agent cost and the budget", len(xs), plural(len(xs)), suite, runs)
	return j
}

// fit compares the total cost range with the budget.
func fit(total schemas.ForecastRange, budget float64) (string, string) {
	switch {
	case budget <= 0:
		return schemas.FitNoBudget, "budget.total_usd is not set"
	case total.Kind == schemas.RangeUnknown:
		return schemas.FitUnknown, "some arm has no estimate: " + total.Reason
	case total.Kind == schemas.RangeUpperBound && *total.High <= budget:
		return schemas.FitFits, fmt.Sprintf("the upper bound %s is within the budget %s", money(*total.High), money(budget))
	case total.Kind == schemas.RangeUpperBound:
		return schemas.FitUnknown, fmt.Sprintf("the upper bound %s is above the budget %s and there is no history to say more", money(*total.High), money(budget))
	case *total.High <= budget:
		return schemas.FitFits, fmt.Sprintf("even the high estimate %s is within the budget %s", money(*total.High), money(budget))
	case *total.Median <= budget:
		return schemas.FitMayExceed, fmt.Sprintf("the median %s fits the budget %s, the high estimate %s does not", money(*total.Median), money(budget), money(*total.High))
	case *total.Low <= budget:
		return schemas.FitLikelyExceeds, fmt.Sprintf("the median %s is over the budget %s; only the low estimate %s fits", money(*total.Median), money(budget), money(*total.Low))
	default:
		return schemas.FitExceeds, fmt.Sprintf("even the low estimate %s is over the budget %s; the scheduler stops leasing slots past it", money(*total.Low), money(budget))
	}
}

// sumRanges adds one range over arms: a history range when every arm has
// one, an upper bound when every arm has at least a bound, else unknown.
func sumRanges(arms []schemas.ArmForecast, get func(schemas.ArmForecast) schemas.ForecastRange) schemas.ForecastRange {
	if len(arms) == 0 {
		return unknownRange("no arm planned")
	}
	var lo, mid, hi float64
	kind := schemas.RangeHistory
	var missing []string
	for _, a := range arms {
		r := get(a)
		switch r.Kind {
		case schemas.RangeHistory:
			lo, mid, hi = lo+*r.Low, mid+*r.Median, hi+*r.High
		case schemas.RangeUpperBound:
			kind, hi = schemas.RangeUpperBound, hi+*r.High
		default:
			missing = append(missing, a.Arm)
		}
	}
	switch {
	case len(missing) > 0:
		return unknownRange(fmt.Sprintf("no estimate for arm %s", strings.Join(missing, ", ")))
	case kind == schemas.RangeUpperBound:
		return upperRange(hi, "sum of history highs and per-run bounds")
	}
	return historyRange(lo, mid, hi)
}

// scaleRange multiplies every bound of r by k.
func scaleRange(r schemas.ForecastRange, k float64) schemas.ForecastRange {
	out := schemas.ForecastRange{Kind: r.Kind, Reason: r.Reason}
	scale := func(p *float64) *float64 {
		if p == nil {
			return nil
		}
		v := *p * k
		return &v
	}
	out.Low, out.Median, out.High = scale(r.Low), scale(r.Median), scale(r.High)
	return out
}

// basisSummary is the one sentence that says where the numbers come from.
func basisSummary(arms []schemas.ArmForecast) string {
	counts := map[string]int{}
	for _, a := range arms {
		counts[a.Basis]++
	}
	if counts[schemas.BasisConfigHash]+counts[schemas.BasisAgentModel]+counts[schemas.BasisLibrary]+counts[BasisAgentTokens] == 0 {
		if counts[schemas.BasisBudgetBound] > 0 {
			return "No history for any arm: upper bounds from per-run usd budgets only, no estimate."
		}
		return "No history for any arm: cannot estimate."
	}
	var parts []string
	for _, b := range []struct{ key, words string }{
		{schemas.BasisConfigHash, "sealed runs of the same config"},
		{schemas.BasisAgentModel, "sealed runs of the same agent and model"},
		{schemas.BasisLibrary, "library runs of other agents on these tasks (first run: your agent may differ)"},
		{BasisAgentTokens, "a rough list-price estimate from the agent's typical tokens per run"},
		{schemas.BasisBudgetBound, "a per-run usd budget bound only"},
		{schemas.BasisNone, "no history (cannot estimate)"},
	} {
		if n := counts[b.key]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d arm%s from %s", n, plural(n), b.words))
		}
	}
	return fmt.Sprintf("History on this suite: %s. Each slot uses its task's runs when it has %d or more, else the suite-wide runs; low and high sum the per-run 10th and 90th percentiles, a planning range, not a confidence interval.",
		strings.Join(parts, ", "), MinTaskRuns)
}

// armTasks lists, per arm id, the task of every planned slot.
func armTasks(m *schemas.Manifest) map[string][]string {
	out := map[string][]string{}
	for _, s := range m.Slots {
		out[s.Arm] = append(out[s.Arm], s.Task)
	}
	return out
}

// experimentsOf returns the distinct experiments of runs, sorted.
func experimentsOf(runs []HistoryRun) []string {
	out := []string{}
	for _, h := range runs {
		if !slices.Contains(out, h.ExpID) {
			out = append(out, h.ExpID)
		}
	}
	slices.Sort(out)
	return out
}

// Quantile is the p-quantile of xs by linear interpolation between order
// statistics (the default of R and NumPy). It does not modify xs. It is NaN
// for an empty xs.
func Quantile(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	h := p * float64(len(s)-1)
	i := int(math.Floor(h))
	if i+1 >= len(s) {
		return s[len(s)-1]
	}
	return s[i] + (h-float64(i))*(s[i+1]-s[i])
}

func historyRange(lo, mid, hi float64) schemas.ForecastRange {
	return schemas.ForecastRange{Kind: schemas.RangeHistory, Low: &lo, Median: &mid, High: &hi}
}

func upperRange(hi float64, reason string) schemas.ForecastRange {
	return schemas.ForecastRange{Kind: schemas.RangeUpperBound, High: &hi, Reason: reason}
}

func unknownRange(reason string) schemas.ForecastRange {
	return schemas.ForecastRange{Kind: schemas.RangeUnknown, Reason: reason}
}

// money formats dollars with cents, or four decimals below a dollar.
func money(v float64) string {
	if math.Abs(v) < 1 {
		return fmt.Sprintf("$%.4f", v)
	}
	return fmt.Sprintf("$%.2f", v)
}

// shortHash is the first 12 characters of a hash, as the leaderboard shows it.
func shortHash(h string) string {
	return h[:min(12, len(h))]
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
