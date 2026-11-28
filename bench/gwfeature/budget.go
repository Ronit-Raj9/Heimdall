package gwfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Feature 9: the budget curve (W4 design 3, section 12).

func init() {
	Register(Feature{ID: schemas.GatewayFeatureBudgetCurve, Name: "Budget curve", Draft: draftBudget, Compute: computeBudget})
}

// Budget curve limits.
const (
	budgetMinUSD = 0.01
	budgetMaxUSD = 20
)

// p4BaseConfig is the part of the base agent file the budget Draft checks.
type p4BaseConfig struct {
	Model    string `yaml:"model"`
	Fairness string `yaml:"fairness"`
	Gateway  struct {
		Provider string `yaml:"provider"`
		Routing  any    `yaml:"routing"`
	} `yaml:"gateway"`
}

// p4ReadBase reads the base agent file inside the org root.
func p4ReadBase(root, base string) (p4BaseConfig, error) {
	var cfg p4BaseConfig
	rel := filepath.Clean(filepath.FromSlash(base))
	if root == "" || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return cfg, fmt.Errorf("the agent file %s is not in this project", base)
	}
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		return cfg, fmt.Errorf("the agent file %s could not be read", base)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("the agent file %s could not be read", base)
	}
	return cfg, nil
}

func p4USD(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func parseBudgets(s string) ([]float64, error) {
	var out []float64
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		v, err := strconv.ParseFloat(part, 64)
		if err != nil || math.IsNaN(v) || v < budgetMinUSD || v > budgetMaxUSD {
			return nil, fmt.Errorf("each budget is an amount in USD from %s to %d; %q is not", p4USD(budgetMinUSD), budgetMaxUSD, part)
		}
		if len(out) > 0 && v <= out[len(out)-1] {
			return nil, errors.New("list the budgets from the smallest to the largest, each one larger than the one before")
		}
		out = append(out, v)
	}
	if len(out) < 3 || len(out) > 5 {
		return nil, errors.New("give 3 to 5 budgets per task, in USD")
	}
	return out, nil
}

func draftBudget(_ context.Context, env DraftEnv, req DraftRequest) (*schemas.Experiment, []string, error) {
	budgets, err := parseBudgets(req.Options["budgets"])
	if err != nil {
		return nil, nil, err
	}
	current := budgets[len(budgets)-1]
	if s := strings.TrimSpace(req.Options["current"]); s != "" {
		v, err := strconv.ParseFloat(s, 64)
		if err != nil || !slices.Contains(budgets, v) {
			return nil, nil, errors.New("the current budget must be one of the budgets listed")
		}
		current = v
	}
	tell := true
	switch strings.ToLower(strings.TrimSpace(req.Options["tell_agent"])) {
	case "", "yes":
	case "no":
		tell = false
	default:
		return nil, nil, errors.New("tell_agent is yes or no")
	}
	base, err := p4ReadBase(env.Root, req.Base)
	if err != nil {
		return nil, nil, err
	}
	if base.Fairness != "" {
		return nil, nil, errors.New("the agent file sets a fairness rule; the budget curve sets each setup's budget itself, so use an agent file without one")
	}
	if base.Gateway.Routing != nil {
		return nil, nil, errors.New("the agent file sets its own gateway routing; the budget curve sets the routing itself, so use an agent file without one")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = strings.TrimSpace(base.Model)
	}
	if model == "" {
		return nil, nil, errors.New("pick the model this test runs on")
	}
	order := []float64{current}
	for _, b := range budgets {
		if b != current {
			order = append(order, b)
		}
	}
	exp := p4Base(env, req)
	for i, b := range order {
		set := map[string]any{
			"model": model,
			"gateway.routing": map[string]any{
				"strategy": schemas.RoutingFixed,
				"targets":  []any{map[string]any{"model": model}},
				"limits":   map[string]any{"budget_usd": b, "budget_reset": "1d"},
			},
			"budgets.usd": b,
		}
		if req.Provider != "" {
			set["gateway.provider"] = req.Provider
		}
		exp.Arms[p4ArmIDs[i]] = schemas.ArmSpec{Label: p4USD(b) + " USD per task", Set: set}
	}
	exp.Budget.Disclose = tell
	p4Test(exp, schemas.GatewayFeatureBudgetCurve, len(order), req.Options)
	return exp, []string{"Bifrost stops each run's calls when its key has spent the budget; the last call can go slightly over."}, nil
}

// budgetPoint is one setup of the curve.
type budgetPoint struct {
	Arm           string    `json:"arm"`
	BudgetUSD     float64   `json:"budget_usd"`
	Runs          int       `json:"runs"`
	SolveRate     *float64  `json:"solve_rate,omitempty"`
	SolveRange    *Interval `json:"solve_range,omitempty"` // Clopper-Pearson over runs
	CostPerRun    *float64  `json:"cost_per_run,omitempty"`
	CostPerSolved *float64  `json:"cost_per_solved,omitempty"` // total cost over solved runs
	StoppedShare  *float64  `json:"stopped_share,omitempty"`   // runs with a call Bifrost refused for the budget
	MaxRunCostUSD *float64  `json:"max_run_cost_usd,omitempty"`
}

// budgetDetails is Effect.Details of the budget curve.
type budgetDetails struct {
	Points    []budgetPoint `json:"points"` // in budget order
	Best      string        `json:"best,omitempty"`
	BestValue string        `json:"best_value,omitempty"`
	Verdict   string        `json:"verdict,omitempty"` // too_low | too_high | fits: the budget in use against the best value
}

// budgetOf is a setup's per-run budget from its frozen config.
func budgetOf(m *schemas.Manifest, arm string) float64 {
	if m == nil {
		return 0
	}
	c := m.Arms[arm].Config
	if c.Gateway.Routing != nil && c.Gateway.Routing.Limits != nil && c.Gateway.Routing.Limits.BudgetUSD > 0 {
		return c.Gateway.Routing.Limits.BudgetUSD
	}
	return c.Budgets.USD
}

func computeBudget(_ context.Context, in Input) (e Effect) {
	arms := append([]string{in.Test.Control}, in.Test.Variants...)
	e = Effect{Arms: ArmResults(in.Runs, in.Manifest, arms)}
	det := budgetDetails{Points: []budgetPoint{}}
	for i, arm := range arms {
		p := budgetPoint{Arm: arm, BudgetUSD: budgetOf(in.Manifest, arm), SolveRate: e.Arms[i].SolveRate, CostPerRun: e.Arms[i].CostPerRun}
		solved, graded, stopped := 0, 0, 0
		total, maxCost, costs := 0.0, 0.0, 0
		for _, r := range in.Runs {
			if r.Arm != arm {
				continue
			}
			p.Runs++
			if r.Success != nil {
				graded++
				if *r.Success {
					solved++
				}
			}
			if r.CostUSD != nil {
				total += *r.CostUSD
				maxCost = math.Max(maxCost, *r.CostUSD)
				costs++
			}
			if slices.ContainsFunc(r.Rows, func(row Row) bool { return row.BudgetRefused }) {
				stopped++
			}
		}
		if graded > 0 {
			lo, hi := stats.ClopperPearson(solved, graded, 0.95)
			p.SolveRange = &Interval{Point: float64(solved) / float64(graded), Low: &lo, High: &hi}
		}
		if solved > 0 && costs > 0 {
			v := total / float64(solved)
			p.CostPerSolved = &v
		}
		if costs > 0 {
			p.MaxRunCostUSD = &maxCost
		}
		if p.Runs > 0 {
			v := float64(stopped) / float64(p.Runs)
			p.StoppedShare = &v
		}
		det.Points = append(det.Points, p)
	}
	slices.SortStableFunc(det.Points, func(a, b budgetPoint) int {
		switch {
		case a.BudgetUSD < b.BudgetUSD:
			return -1
		case a.BudgetUSD > b.BudgetUSD:
			return 1
		}
		return 0
	})
	defer func() { e.Details, _ = json.Marshal(det) }()
	v := budgetDecide(det.Points, budgetOf(in.Manifest, in.Test.Control))
	det.Best, det.BestValue, det.Verdict = v.Best, v.BestValue, v.Verdict
	if v.State != StateMeasured {
		e.State, e.Reason = v.State, v.Reason
		e.Headline = v.Headline
		return e
	}
	bv := det.Points[v.bestValue]
	if bv.Arm != in.Test.Control {
		e.Variant = bv.Arm
		solve := PairedTasks(in.Runs, in.Test.Control, bv.Arm, SolveValue)
		e.Tasks = solve.Tasks
		e.SolveDelta = p4Interval(solve)
		e.CostDelta = p4Interval(PairedTasks(in.Runs, in.Test.Control, bv.Arm, CostValue))
		e.LatencyDelta = p4Interval(PairedTasks(in.Runs, in.Test.Control, bv.Arm, LatencyValue))
	}
	e.State = StateMeasured
	e.Headline = budgetHeadline(det.Points, v.best, v.bestValue) + " " + v.Headline
	body, _ := json.MarshalIndent(map[string]any{"budgets": []map[string]any{{"max_limit": bv.BudgetUSD, "reset_duration": "1d"}}}, "", "  ")
	e.Setting = &BifrostSetting{
		Kind:  "virtual_key",
		Title: "Virtual key budget: " + p4USD(bv.BudgetUSD) + " USD",
		Where: "Bifrost dashboard, Virtual keys, budgets; a per-task budget needs one key per task or per session",
		Body:  string(body),
		Note:  "The test reset each key's budget every day (1d). Set the reset duration to how often one key starts a new task or session.",
	}
	switch v.Verdict {
	case budgetTooHigh:
		e.Result = "saved"
	case budgetTooLow:
		e.Result = "lost"
	default:
		e.Result = "worked"
	}
	return e
}

// Budget verdicts on the budget in use.
const (
	budgetTooLow  = "too_low"
	budgetTooHigh = "too_high"
	budgetFits    = "fits"
)

// Budget curve rules (owner's "wrong budget" check), in this order.
const (
	budgetMinRuns    = 10   // runs per budget point before any recommendation
	budgetBestShare  = 0.95 // the best value reaches this share of the best solve rate
	budgetHighFactor = 2.0  // a budget in use above this many times the best value is too high
)

// budgetVerdict is the decision of the budget curve.
type budgetVerdict struct {
	State, Reason, Headline string
	Best, BestValue         string // arm ids; empty when no recommendation
	Verdict                 string // too_low | too_high | fits; empty when no recommendation
	best, bestValue         int    // indexes into the points
}

// budgetDecide applies the rules: (1) nothing solved at any budget: no
// recommendation; (2) under 10 runs on a budget point: too few runs; (3) the
// best value is the cheapest budget reaching 95% of the best solve rate, and
// the budget in use is too low below it, too high above twice it, else fits.
// points are in budget order. Pure.
func budgetDecide(points []budgetPoint, current float64) budgetVerdict {
	v := budgetVerdict{best: -1, bestValue: -1}
	graded := false
	solvedAny := false
	for _, p := range points {
		if p.SolveRate != nil {
			graded = true
			if *p.SolveRate > 0 {
				solvedAny = true
			}
		}
	}
	switch {
	case !graded:
		v.State, v.Reason = StateNotEnoughData, "no setup has a graded run yet"
		v.Headline = "Not enough data yet: " + v.Reason + "."
		return v
	case !solvedAny:
		v.State, v.Reason = StateNotEnoughData, "no setup solved a task"
		v.Headline = "No setup solved a task, so no budget can be recommended."
		return v
	}
	for _, p := range points {
		if p.Runs < budgetMinRuns {
			v.State = StateNotEnoughData
			v.Reason = fmt.Sprintf("too few runs to recommend a budget: %s USD has %d runs, %d are needed on each budget", p4USD(p.BudgetUSD), p.Runs, budgetMinRuns)
			v.Headline = "Too few runs to recommend a budget."
			return v
		}
	}
	// The best: the highest solve rate, ties to the smaller budget.
	for i, p := range points {
		if p.SolveRate != nil && (v.best < 0 || *p.SolveRate > *points[v.best].SolveRate) {
			v.best = i
		}
	}
	top := *points[v.best].SolveRate
	for i, p := range points {
		if p.SolveRate != nil && *p.SolveRate >= budgetBestShare*top {
			v.bestValue = i
			break
		}
	}
	v.State = StateMeasured
	v.Best, v.BestValue = points[v.best].Arm, points[v.bestValue].Arm
	bv := points[v.bestValue]
	cur := -1
	for i, p := range points {
		if p.BudgetUSD == current {
			cur = i
		}
	}
	switch {
	case current < bv.BudgetUSD:
		v.Verdict = budgetTooLow
		more := ""
		if cur >= 0 && points[cur].SolveRate != nil {
			more = fmt.Sprintf(": %s USD would solve %.0f more points (%s instead of %s)", p4USD(bv.BudgetUSD), (*bv.SolveRate-*points[cur].SolveRate)*100, p4Pct(bv.SolveRate), p4Pct(points[cur].SolveRate))
		}
		v.Headline = fmt.Sprintf("Your budget of %s USD is too low%s.", p4USD(current), more)
	case current > budgetHighFactor*bv.BudgetUSD:
		v.Verdict = budgetTooHigh
		v.Headline = fmt.Sprintf("Your budget of %s USD is too high: %s USD solves as many tasks.", p4USD(current), p4USD(bv.BudgetUSD))
	default:
		v.Verdict = budgetFits
		v.Headline = "Budget fits."
	}
	return v
}

func p4Pct(v *float64) string {
	if v == nil {
		return "unknown"
	}
	return fmt.Sprintf("%.0f%%", *v*100)
}

func budgetHeadline(points []budgetPoint, best, bestValue int) string {
	bv, top := points[bestValue], points[best]
	var s strings.Builder
	fmt.Fprintf(&s, "%s USD per task solves %s", p4USD(bv.BudgetUSD), p4Pct(bv.SolveRate))
	if r := bv.SolveRange; r != nil && r.Low != nil && r.High != nil {
		fmt.Fprintf(&s, " (range %.0f%% to %.0f%%)", *r.Low*100, *r.High*100)
	}
	if bestValue != best {
		fmt.Fprintf(&s, ", as many as %s USD (%s)", p4USD(top.BudgetUSD), p4Pct(top.SolveRate))
	} else {
		s.WriteString(", the most of the budgets tried")
	}
	for i := bestValue - 1; i >= 0; i-- {
		fmt.Fprintf(&s, "; %s USD solves %s", p4USD(points[i].BudgetUSD), p4Pct(points[i].SolveRate))
	}
	fmt.Fprintf(&s, ". Best value: %s USD.", p4USD(bv.BudgetUSD))
	return s.String()
}
