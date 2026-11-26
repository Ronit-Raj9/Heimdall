package gwfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Complexity router test (audit/W4_DESIGN_3_BIFROST.md section 4): three
// setups (strong model only, router on, cheap model only) give the cost and
// solve rate per complexity tier, and an estimated best routing cut.

func init() {
	Register(Feature{ID: schemas.GatewayFeatureComplexityRouter, Name: "Complexity router", Draft: draftRouter, Compute: computeRouter})
}

// Complexity tiers, from the lowest to the highest.
var routerTiers = []string{"SIMPLE", "MEDIUM", "COMPLEX"}

const (
	routerDefaultWhen      = `complexity_tier == "COMPLEX"`
	routerDefaultTolerance = 0.03
	routerClassifyAll      = `complexity_tier != ""`
)

type routerDetails struct {
	Tiers          []tierRow `json:"tiers"`         // SIMPLE, MEDIUM, COMPLEX, in that order
	Unclassified   int       `json:"unclassified"`  // tasks no setup's rows gave a tier
	Disagreements  int       `json:"disagreements"` // tasks whose setups got different tiers
	Recommendation routerCut `json:"recommendation"`
}

type tierRow struct {
	Tier       string              `json:"tier"`
	Tasks      int                 `json:"tasks"`
	SolveRate  map[string]*float64 `json:"solve_rate"`   // arm id -> mean over the tier's tasks
	CostPerRun map[string]*float64 `json:"cost_per_run"` // arm id -> mean USD
	SolveDelta *Interval           `json:"solve_delta,omitempty"`
	CostDelta  *Interval           `json:"cost_delta,omitempty"`
	State      string              `json:"state"`
	Reason     string              `json:"reason,omitempty"`
}

type routerCut struct {
	CheapTiers []string  `json:"cheap_tiers"` // tiers sent to the cheap model; empty: keep the strong model for all
	SolveDelta *Interval `json:"solve_delta,omitempty"`
	CostDelta  *Interval `json:"cost_delta,omitempty"`
	SavedShare *float64  `json:"saved_share,omitempty"` // 1 - cost(cut) / cost(A)
	State      string    `json:"state"`
	Reason     string    `json:"reason,omitempty"`
	Estimated  bool      `json:"estimated"` // true: built from setups A and C, not run as a router
}

// p3Experiment is the base experiment of section 3.13 with the given arms.
func p3Experiment(env DraftEnv, req DraftRequest, feature string, control string, variants []string, arms map[string]schemas.ArmSpec) *schemas.Experiment {
	trials := req.Trials
	if trials <= 0 {
		trials = 2
	}
	tasks := schemas.TaskSelector{All: len(req.Tasks) == 0, IDs: slices.Clone(req.Tasks)}
	exp := &schemas.Experiment{
		Suite:        req.Suite,
		Base:         req.Base,
		Arms:         arms,
		Primary:      schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:       []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions:   []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: tasks, Trials: trials}},
		Verdict:      schemas.VerdictSpec{Policy: "paired_signflip", Alpha: 0.05, AssumedSD: 0.30},
		ExtraGates:   []string{"model_fidelity"},
		Seed:         20261127,
		Concurrency:  2,
		ManifestMode: "agent_exposure",
		GatewayTest:  &schemas.GatewayTest{Feature: feature, Control: control, Variants: variants, Options: req.Options},
	}
	if env.Root != "" {
		if _, err := os.Stat(filepath.Join(env.Root, "policies", "default.yaml")); err == nil {
			exp.Policy = "policies/default.yaml"
		}
	}
	if req.Provider != "" {
		for id, a := range arms {
			a.Set["gateway.provider"] = req.Provider
			arms[id] = a
		}
	}
	return exp
}

func routerArm(label, model, when, target string) schemas.ArmSpec {
	return schemas.ArmSpec{Label: label, Set: map[string]any{
		"model":           model,
		"gateway.routing": map[string]any{"strategy": "rule", "when": when, "targets": []any{map[string]any{"model": target}}},
	}}
}

func routerTolerance(opts map[string]string) (float64, error) {
	s := strings.TrimSpace(opts["tolerance"])
	if s == "" {
		return routerDefaultTolerance, nil
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || !(v > 0 && v <= 0.5) {
		return 0, errors.New("The tolerance must be a number above 0 and at most 0.5.")
	}
	return v, nil
}

func draftRouter(ctx context.Context, env DraftEnv, req DraftRequest) (*schemas.Experiment, []string, error) {
	strong, cheap := strings.TrimSpace(req.Options["strong_model"]), strings.TrimSpace(req.Options["cheap_model"])
	if strong == "" || cheap == "" {
		return nil, nil, errors.New("Pick a strong model and a cheap model.")
	}
	if strong == cheap {
		return nil, nil, errors.New("The strong model and the cheap model must be different models.")
	}
	when := strings.TrimSpace(req.Options["router_when"])
	if when == "" {
		when = routerDefaultWhen
	}
	if !strings.Contains(when, "complexity_tier") {
		return nil, nil, errors.New("The router rule must read complexity_tier, or Bifrost never classifies the requests.")
	}
	if _, err := routerTolerance(req.Options); err != nil {
		return nil, nil, err
	}
	arms := map[string]schemas.ArmSpec{
		"A": routerArm("strong model only", strong, routerClassifyAll, strong),
		"B": routerArm("router on", cheap, when, strong),
		"C": routerArm("cheap model only", cheap, routerClassifyAll, cheap),
	}
	exp := p3Experiment(env, req, schemas.GatewayFeatureComplexityRouter, "A", []string{"B", "C"}, arms)
	notes := []string{
		"Bifrost classifies the first request of each task (the one with your task text); later requests keep the model unless the router's session setting carries the tier forward.",
		"The classifier's own embedding calls are billed to the gateway, not to these runs.",
	}
	return exp, notes, nil
}

// routerRunTier is the tier of the run's first message row that has one.
func routerRunTier(r Run) string {
	for _, row := range r.Rows {
		if row.Kind == "message" && row.ComplexityTier != "" {
			return row.ComplexityTier
		}
	}
	return ""
}

func routerTierRank(t string) int { return slices.Index(routerTiers, t) }

// routerTaskTiers gives each task the most frequent run tier over the arms;
// a tie takes the higher tier. It also counts tasks whose runs disagree and
// tasks no run classified.
func routerTaskTiers(runs []Run, arms []string) (tiers map[string]string, disagreements, unclassified int) {
	counts := map[string]map[string]int{}
	for _, r := range runs {
		if !slices.Contains(arms, r.Arm) {
			continue
		}
		if counts[r.Task] == nil {
			counts[r.Task] = map[string]int{}
		}
		if t := routerRunTier(r); t != "" {
			counts[r.Task][t]++
		}
	}
	tiers = map[string]string{}
	for task, c := range counts {
		if len(c) == 0 {
			unclassified++
			continue
		}
		if len(c) > 1 {
			disagreements++
		}
		best, bestN := "", -1
		for t, n := range c {
			if n > bestN || (n == bestN && routerTierRank(t) > routerTierRank(best)) {
				best, bestN = t, n
			}
		}
		tiers[task] = best
	}
	return tiers, disagreements, unclassified
}

// routerWhenTiers is the tiers a rule expression names in quotes; nil when
// the expression negates (then which tiers it matches is not read here).
var routerTierQuoted = regexp.MustCompile(`"(SIMPLE|MEDIUM|COMPLEX)"`)

func routerWhenTiers(when string) []string {
	if strings.Contains(when, "!") || strings.Contains(strings.ToLower(when), "not") {
		return nil
	}
	var out []string
	for _, m := range routerTierQuoted.FindAllStringSubmatch(when, -1) {
		if !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

// taskMeans is the per task mean of value over one arm's runs.
func taskMeans(runs []Run, arm string, value func(Run) (float64, bool)) map[string]float64 {
	type acc struct{ sum, n float64 }
	m := map[string]*acc{}
	for _, r := range runs {
		if r.Arm != arm {
			continue
		}
		if v, ok := value(r); ok {
			a := m[r.Task]
			if a == nil {
				a = &acc{}
				m[r.Task] = a
			}
			a.sum += v
			a.n++
		}
	}
	out := make(map[string]float64, len(m))
	for t, a := range m {
		out[t] = a.sum / a.n
	}
	return out
}

func p3Interval(d []float64) *Interval {
	if len(d) == 0 {
		return nil
	}
	iv := &Interval{Point: stats.Mean(d)}
	if lo, hi, ok := stats.PairedT(d, 0.95); ok {
		iv.Low, iv.High = &lo, &hi
	}
	return iv
}

func pairedInterval(p Paired) *Interval {
	if p.Tasks == 0 {
		return nil
	}
	iv := p.Delta
	return &iv
}

func p3Ptr(v float64) *float64 { return &v }

func notEnoughTasks(n int) string {
	return fmt.Sprintf("%d of the %d tasks needed have results on both setups", n, MinTasks)
}

// p3Result is Effect.Result by section 3.13.
func p3Result(e Effect, tolerance float64) string {
	switch {
	case e.State == StateNotApplied:
		return "failed"
	case e.State != StateMeasured:
		return ""
	case e.SolveDelta != nil && e.SolveDelta.High != nil && *e.SolveDelta.High < 0:
		return "lost"
	case e.CostDelta != nil && e.CostDelta.Point < 0 && e.SolveDelta != nil && e.SolveDelta.Low != nil && *e.SolveDelta.Low >= -tolerance:
		return "saved"
	}
	return "worked"
}

func filterTasks(runs []Run, keep func(task string) bool) []Run {
	var out []Run
	for _, r := range runs {
		if keep(r.Task) {
			out = append(out, r)
		}
	}
	return out
}

// costWords is "fell 41% (range 30% to 50%)" for a USD delta against base.
func costWords(d *Interval, base float64) string {
	if d == nil || base <= 0 {
		return ""
	}
	pct := func(v float64) float64 { return math.Round(math.Abs(v) / base * 100) }
	verb := "rose"
	if d.Point < 0 {
		verb = "fell"
	}
	s := fmt.Sprintf("%s %.0f%%", verb, pct(d.Point))
	if d.Low != nil && d.High != nil && (*d.Low < 0) == (*d.High < 0) {
		a, b := pct(*d.Low), pct(*d.High)
		if a > b {
			a, b = b, a
		}
		s += fmt.Sprintf(" (range %.0f%% to %.0f%%)", a, b)
	}
	return s
}

// pointWords is "by -2 points (range -6 to +2)" for a solve rate delta.
func pointWords(d *Interval) string {
	if d == nil {
		return ""
	}
	pt := func(v float64) string { return fmt.Sprintf("%+.0f", math.Round(v*100)) }
	s := "by " + strings.TrimPrefix(pt(d.Point), "+") + " points"
	if d.Low != nil && d.High != nil {
		s += fmt.Sprintf(" (range %s to %s)", strings.TrimPrefix(pt(*d.Low), "+"), pt(*d.High))
	}
	return s
}

func computeRouter(ctx context.Context, in Input) Effect {
	a, b, c := in.Test.Control, "B", "C"
	if len(in.Test.Variants) > 0 {
		b = in.Test.Variants[0]
	}
	if len(in.Test.Variants) > 1 {
		c = in.Test.Variants[1]
	}
	arms := []string{a, b, c}
	strong, cheap := in.Test.Options["strong_model"], in.Test.Options["cheap_model"]
	when := in.Test.Options["router_when"]
	if when == "" {
		when = routerDefaultWhen
	}
	tolerance, err := routerTolerance(in.Test.Options)
	if err != nil {
		tolerance = routerDefaultTolerance
	}
	e := Effect{Variant: b, Arms: ArmResults(in.Runs, in.Manifest, arms)}
	tiers, dis, uncl := routerTaskTiers(in.Runs, arms)
	det := routerDetails{Unclassified: uncl, Disagreements: dis}

	// Applied checks.
	classified, matched, routed := false, false, false
	matchTiers := routerWhenTiers(when)
	provider := ""
	for _, r := range in.Runs {
		if !slices.Contains(arms, r.Arm) {
			continue
		}
		for _, row := range r.Rows {
			if row.ComplexityTier != "" {
				classified = true
			}
			if provider == "" && row.ModelServed == strong {
				provider = row.Provider
			}
			if r.Arm != b {
				continue
			}
			if slices.Contains(matchTiers, row.ComplexityTier) {
				matched = true
			}
			if row.ModelServed == strong {
				routed = true
			}
		}
	}
	for _, r := range in.Runs {
		for _, row := range r.Rows {
			if provider == "" && row.Provider != "" {
				provider = row.Provider
			}
		}
	}
	finish := func() Effect {
		raw, _ := json.Marshal(det)
		e.Details = raw
		e.Result = p3Result(e, tolerance)
		return e
	}
	for _, t := range routerTiers {
		det.Tiers = append(det.Tiers, tierRow{Tier: t, State: StateNotEnoughData, Reason: notEnoughTasks(0), SolveRate: map[string]*float64{}, CostPerRun: map[string]*float64{}})
	}
	det.Recommendation = routerCut{CheapTiers: []string{}, State: StateNotEnoughData, Estimated: true}
	if !classified {
		e.State = StateNotApplied
		e.Reason = "Bifrost gave no request a complexity tier, so the router never ran. Check that a classifier is configured in Bifrost."
		e.Headline = e.Reason
		return finish()
	}
	if matched && !routed {
		e.State = StateNotApplied
		e.Reason = fmt.Sprintf("The router rule matched no request: no call of the router setup went to %s.", strong)
		e.Headline = e.Reason
		return finish()
	}

	// Overall.
	solve := PairedTasks(in.Runs, a, b, SolveValue)
	cost := PairedTasks(in.Runs, a, b, CostValue)
	lat := PairedTasks(in.Runs, a, b, LatencyValue)
	e.Tasks = solve.Tasks
	e.SolveDelta, e.CostDelta, e.LatencyDelta = pairedInterval(solve), pairedInterval(cost), pairedInterval(lat)

	// Per tier.
	solveMeans := map[string]map[string]float64{}
	costMeans := map[string]map[string]float64{}
	for _, arm := range arms {
		solveMeans[arm] = taskMeans(in.Runs, arm, SolveValue)
		costMeans[arm] = taskMeans(in.Runs, arm, CostValue)
	}
	for i, t := range routerTiers {
		row := &det.Tiers[i]
		inTier := func(task string) bool { return tiers[task] == t }
		for task := range tiers {
			if inTier(task) {
				row.Tasks++
			}
		}
		sub := filterTasks(in.Runs, inTier)
		for _, arm := range arms {
			var sv []float64
			for task, v := range solveMeans[arm] {
				if inTier(task) {
					sv = append(sv, v)
				}
			}
			if len(sv) > 0 {
				row.SolveRate[arm] = p3Ptr(stats.Mean(sv))
			}
			if res := ArmResults(sub, nil, []string{arm}); res[0].CostPerRun != nil {
				row.CostPerRun[arm] = res[0].CostPerRun
			}
		}
		ps, pc := PairedTasks(sub, a, b, SolveValue), PairedTasks(sub, a, b, CostValue)
		row.SolveDelta, row.CostDelta = pairedInterval(ps), pairedInterval(pc)
		if ps.Tasks < MinTasks {
			row.Reason = notEnoughTasks(ps.Tasks)
		} else {
			row.State, row.Reason = StateMeasured, ""
		}
	}

	// Recommendation: estimated per task value of each cut, against A.
	var classTasks []string
	for task := range tiers {
		if _, okA := solveMeans[a][task]; okA {
			if _, okC := solveMeans[c][task]; okC {
				classTasks = append(classTasks, task)
			}
		}
	}
	slices.Sort(classTasks)
	rec := &det.Recommendation
	if len(classTasks) < MinTasks {
		rec.Reason = fmt.Sprintf("%d of the %d classified tasks needed have results on the strong and the cheap setup", len(classTasks), MinTasks)
	} else {
		var best *routerCut
		for k := 0; k <= len(routerTiers); k++ {
			cut := slices.Clone(routerTiers[:k])
			var ds, dc []float64
			var costA, costCut float64
			for _, task := range classTasks {
				src := a
				if slices.Contains(cut, tiers[task]) {
					src = c
				}
				ds = append(ds, solveMeans[src][task]-solveMeans[a][task])
				ca, okA := costMeans[a][task]
				cs, okS := costMeans[src][task]
				if okA && okS {
					dc = append(dc, cs-ca)
					costA += ca
					costCut += cs
				}
			}
			cand := routerCut{CheapTiers: cut, SolveDelta: p3Interval(ds), CostDelta: p3Interval(dc), State: StateMeasured, Estimated: true}
			if costA > 0 {
				cand.SavedShare = p3Ptr(1 - costCut/costA)
			}
			if cand.SolveDelta == nil || cand.SolveDelta.Low == nil || *cand.SolveDelta.Low < -tolerance {
				continue
			}
			if best == nil || (cand.CostDelta != nil && (best.CostDelta == nil || cand.CostDelta.Point < best.CostDelta.Point)) {
				cc := cand
				best = &cc
			}
		}
		if best != nil {
			*rec = *best
		} else {
			rec.Reason = "no cut keeps the solve rate within the tolerance"
		}
	}
	if rec.State == StateMeasured && len(rec.CheapTiers) > 0 && len(rec.CheapTiers) < len(routerTiers) {
		var rest []string
		for _, t := range routerTiers {
			if !slices.Contains(rec.CheapTiers, t) {
				rest = append(rest, `"`+t+`"`)
			}
		}
		rule := RoutingRule{
			Name:          "heimdall-router-" + in.ExpID,
			Enabled:       false,
			CelExpression: "complexity_tier in [" + strings.Join(rest, ", ") + "]",
			Targets:       []RoutingTarget{{Provider: provider, Model: strong, Weight: 1}},
			Scope:         "global",
			Priority:      10,
		}
		note := fmt.Sprintf("Requests the rule does not match keep the model the app asks for, so set your app's default model to %s. The rule is created switched off: review it, then switch it on.", cheap)
		title := fmt.Sprintf("Routing rule: %s requests to %s", strings.ToLower(strings.Join(rest, " and ")), strong)
		e.Setting = RuleSetting(strings.ReplaceAll(title, `"`, ""), note, rule)
	}

	if solve.Tasks < MinTasks {
		e.State = StateNotEnoughData
		e.Reason = notEnoughTasks(solve.Tasks)
		e.Headline = "Not enough data yet: " + e.Reason + "."
		return finish()
	}
	e.State = StateMeasured
	var baseCost []float64
	for task, v := range costMeans[a] {
		if _, ok := costMeans[b][task]; ok {
			baseCost = append(baseCost, v)
		}
	}
	cw := ""
	if len(baseCost) > 0 {
		cw = costWords(e.CostDelta, stats.Mean(baseCost))
	}
	if cw != "" {
		e.Headline = fmt.Sprintf("With the router on, cost per task %s and solve rate changed %s.", cw, pointWords(e.SolveDelta))
	} else {
		e.Headline = fmt.Sprintf("With the router on, solve rate changed %s.", pointWords(e.SolveDelta))
	}
	return finish()
}
