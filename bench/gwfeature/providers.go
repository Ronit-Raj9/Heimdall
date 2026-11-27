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
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/modelid"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Feature 8: the same model on several providers (W4 design 3, section 11).

func init() {
	Register(Feature{ID: schemas.GatewayFeatureProviders, Name: "Use the same model from several providers", Draft: draftProviders, Compute: computeProviders})
}

// p4ArmIDs are the arm ids of a feature test in order: A is the control.
var p4ArmIDs = []string{"A", "B", "C", "D", "E"}

// p4Base is the base experiment every P4 Draft starts from (section 3.13).
func p4Base(env DraftEnv, req DraftRequest) *schemas.Experiment {
	tasks := schemas.TaskSelector{All: true}
	if len(req.Tasks) > 0 {
		tasks = schemas.TaskSelector{IDs: slices.Clone(req.Tasks)}
	}
	trials := req.Trials
	if trials < 1 {
		trials = 1
	}
	exp := &schemas.Experiment{
		Suite:        req.Suite,
		Base:         req.Base,
		Arms:         map[string]schemas.ArmSpec{},
		Primary:      schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:       []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions:   []schemas.ConditionSpec{{ID: schemas.ConditionClean, Tasks: tasks, Trials: trials}},
		Verdict:      schemas.VerdictSpec{Policy: "paired_signflip", Alpha: 0.05, AssumedSD: 0.30},
		ExtraGates:   []string{"model_fidelity"},
		Seed:         20261127,
		Concurrency:  2,
		ManifestMode: "agent_exposure",
	}
	if env.Root != "" {
		if _, err := os.Stat(filepath.Join(env.Root, "policies", "default.yaml")); err == nil {
			exp.Policy = "policies/default.yaml"
		}
	}
	return exp
}

// p4Test names the feature and its arms on a drafted experiment.
func p4Test(exp *schemas.Experiment, feature string, arms int, opts map[string]string) {
	t := &schemas.GatewayTest{Feature: feature, Control: "A", Variants: []string{}, Options: map[string]string{}}
	for i := 1; i < arms; i++ {
		t.Variants = append(t.Variants, p4ArmIDs[i])
	}
	for k, v := range opts {
		t.Options[k] = v
	}
	exp.GatewayTest = t
}

// providerTarget is one entry of the targets option.
type providerTarget struct{ Provider, Model string }

// parseProviderTargets reads "provider:model,provider:model". A model may
// itself hold a colon (Bedrock "...-v1:0"), so only the first one splits.
func parseProviderTargets(s string) ([]providerTarget, error) {
	var out []providerTarget
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, m, ok := strings.Cut(part, ":")
		p, m = strings.ToLower(strings.TrimSpace(p)), strings.TrimSpace(m)
		if !ok || p == "" || m == "" {
			return nil, fmt.Errorf("each target is a provider and a model, for example anthropic:claude-sonnet-4-5; %q is not", part)
		}
		out = append(out, providerTarget{Provider: p, Model: m})
	}
	return out, nil
}

func draftProviders(_ context.Context, env DraftEnv, req DraftRequest) (*schemas.Experiment, []string, error) {
	targets, err := parseProviderTargets(req.Options["targets"])
	if err != nil {
		return nil, nil, err
	}
	if len(targets) < 2 || len(targets) > 5 {
		return nil, nil, errors.New("give 2 to 5 targets, each a provider and a model, the first being the one you use today")
	}
	seen := map[string]bool{}
	for _, t := range targets {
		if seen[t.Provider] {
			return nil, nil, fmt.Errorf("the provider %s is listed twice; this test compares one model across different providers", t.Provider)
		}
		seen[t.Provider] = true
		if modelid.Family(t.Model) != modelid.Family(targets[0].Model) {
			return nil, nil, fmt.Errorf("these are different models: %s and %s; this test compares one model across providers", targets[0].Model, t.Model)
		}
	}
	exp := p4Base(env, req)
	for i, t := range targets {
		exp.Arms[p4ArmIDs[i]] = schemas.ArmSpec{Label: t.Provider, Set: map[string]any{"model": t.Model, "gateway.provider": t.Provider}}
	}
	p4Test(exp, schemas.GatewayFeatureProviders, len(targets), req.Options)
	return exp, []string{
		"Bedrock and Vertex calls do not pass Heimdall's fault proxy, so this test runs without injected faults.",
		"Each provider needs your organization's key for it on the Models page.",
	}, nil
}

// providerArm is one setup's numbers in the providers report.
type providerArm struct {
	Arm           string         `json:"arm"`
	Provider      string         `json:"provider"`
	Model         string         `json:"model"`
	SolveRate     *float64       `json:"solve_rate,omitempty"`
	CostPerRun    *float64       `json:"cost_per_run,omitempty"`
	LatencyP50MS  *float64       `json:"latency_p50_ms,omitempty"`
	LatencyP95MS  *float64       `json:"latency_p95_ms,omitempty"`
	Calls         int            `json:"calls"`
	Errors        int            `json:"errors"`
	ErrorRate     *Interval      `json:"error_rate,omitempty"`
	ServedAsAsked *float64       `json:"served_as_asked,omitempty"` // over calls whose served model is known
	ServedKnown   int            `json:"served_known"`
	ServedUnknown int            `json:"served_unknown"`
	ServedModels  map[string]int `json:"served_models,omitempty"`
	FallbackShare *float64       `json:"fallback_share,omitempty"`
	SolveDelta    *Interval      `json:"solve_delta,omitempty"` // against A
	CostDelta     *Interval      `json:"cost_delta,omitempty"`
	LatencyDelta  *Interval      `json:"latency_delta,omitempty"`
	PairedTasks   int            `json:"paired_tasks"`
}

// providersDetails is Effect.Details of the providers feature.
type providersDetails struct {
	Arms []providerArm `json:"arms"`
}

// p4Quantile is the q quantile of v (sorted in place), nil when empty.
func p4Quantile(v []float64, q float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	sort.Float64s(v)
	x := stats.Quantile(v, q)
	return &x
}

func p4Interval(p Paired) *Interval {
	d := p.Delta
	return &d
}

// p4ProviderName is a provider id in plain words.
func p4ProviderName(p string) string {
	switch p {
	case "openai":
		return "OpenAI"
	case "":
		return "an unknown provider"
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

// p4Result is Effect.Result by the rules of section 3.13.
func p4Result(e Effect, failed bool, tolerance float64) string {
	switch {
	case e.State == StateNotApplied || failed:
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

func p4Points(v float64) string { return fmt.Sprintf("%+.0f", math.Round(v*100)) }

func computeProviders(_ context.Context, in Input) Effect {
	arms := append([]string{in.Test.Control}, in.Test.Variants...)
	targets, _ := parseProviderTargets(in.Test.Options["targets"])
	e := Effect{Variant: "", Arms: ArmResults(in.Runs, in.Manifest, arms)}
	det := providersDetails{Arms: []providerArm{}}
	failed := false
	var otherServed string
	for i, arm := range arms {
		pa := providerArm{Arm: arm, ServedModels: map[string]int{}}
		if i < len(targets) {
			pa.Provider, pa.Model = targets[i].Provider, targets[i].Model
		}
		if i < len(e.Arms) {
			pa.SolveRate, pa.CostPerRun = e.Arms[i].SolveRate, e.Arms[i].CostPerRun
		}
		var lat []float64
		matched, fallbacks := 0, 0
		wrongProvider := ""
		for _, r := range in.Runs {
			if r.Arm != arm {
				continue
			}
			for _, row := range r.Rows {
				if row.Kind != "message" {
					continue
				}
				pa.Calls++
				if row.Status == "error" {
					pa.Errors++
				}
				if row.Status == "success" {
					lat = append(lat, row.LatencyMS)
				}
				if row.FallbackIndex > 0 {
					fallbacks++
				}
				if row.Provider != "" && pa.Provider != "" && row.Provider != pa.Provider && wrongProvider == "" {
					wrongProvider = row.Provider
				}
				if row.ModelServed == "" {
					pa.ServedUnknown++
					continue
				}
				pa.ServedKnown++
				pa.ServedModels[row.ModelServed]++
				if modelid.SameModel(row.ModelAsked, row.ModelServed) {
					matched++
				}
			}
		}
		pa.LatencyP50MS, pa.LatencyP95MS = p4Quantile(slices.Clone(lat), 0.5), p4Quantile(lat, 0.95)
		if pa.Calls > 0 {
			lo, hi := stats.ClopperPearson(pa.Errors, pa.Calls, 0.95)
			pa.ErrorRate = &Interval{Point: float64(pa.Errors) / float64(pa.Calls), Low: &lo, High: &hi}
			fs := float64(fallbacks) / float64(pa.Calls)
			pa.FallbackShare = &fs
		}
		if pa.ServedKnown > 0 {
			v := float64(matched) / float64(pa.ServedKnown)
			pa.ServedAsAsked = &v
			if matched < pa.ServedKnown {
				failed = true
				if otherServed == "" {
					otherServed = p4OtherServed(pa)
				}
			}
		}
		if len(pa.ServedModels) == 0 {
			pa.ServedModels = nil
		}
		if i > 0 {
			solve := PairedTasks(in.Runs, in.Test.Control, arm, SolveValue)
			pa.PairedTasks = solve.Tasks
			pa.SolveDelta = p4Interval(solve)
			pa.CostDelta = p4Interval(PairedTasks(in.Runs, in.Test.Control, arm, CostValue))
			pa.LatencyDelta = p4Interval(PairedTasks(in.Runs, in.Test.Control, arm, LatencyValue))
		}
		if wrongProvider != "" && e.State == "" {
			label := arm
			if in.Manifest != nil && in.Manifest.Arms[arm].Label != "" {
				label = in.Manifest.Arms[arm].Label
			}
			e.State, e.Reason = StateNotApplied, fmt.Sprintf("calls of setup %s were answered by %s", label, wrongProvider)
		}
		det.Arms = append(det.Arms, pa)
	}
	e.Details, _ = json.Marshal(det)
	if e.State == StateNotApplied {
		e.Headline = "The test did not run as set up: " + e.Reason + "."
		e.Result = p4Result(e, failed, 0.03)
		return e
	}
	if len(det.Arms) < 2 {
		e.State, e.Reason = StateUnknown, "this test needs at least two setups"
		e.Headline = "Unknown: " + e.Reason
		return e
	}
	// The recommended setup: best solve rate, ties by lower median latency,
	// among setups whose every known call was the model asked for, with at
	// least 20 known calls.
	best := -1
	for i, pa := range det.Arms {
		if pa.ServedAsAsked == nil || *pa.ServedAsAsked != 1 || pa.ServedKnown < 20 || pa.SolveRate == nil {
			continue
		}
		if best < 0 || p4BetterProvider(pa, det.Arms[best]) {
			best = i
		}
	}
	if best >= 0 {
		e.Variant = det.Arms[best].Arm
	}
	focus := det.Arms[1]
	if best > 0 {
		focus = det.Arms[best]
	}
	e.Tasks = focus.PairedTasks
	e.SolveDelta, e.CostDelta, e.LatencyDelta = focus.SolveDelta, focus.CostDelta, focus.LatencyDelta
	if focus.PairedTasks < MinTasks {
		e.State, e.Reason = StateNotEnoughData, fmt.Sprintf("%d of the %d tasks needed have results on both setups", focus.PairedTasks, MinTasks)
		e.Headline = "Not enough data yet: " + e.Reason + "."
		return e
	}
	e.State = StateMeasured
	e.Headline = p4ProvidersHeadline(det.Arms[0], focus, otherServed)
	if best >= 0 {
		e.Setting = p4ProvidersSetting(det.Arms, best)
	}
	e.Result = p4Result(e, failed, 0.03)
	return e
}

// p4BetterProvider orders setups by solve rate, then lower median latency.
func p4BetterProvider(a, b providerArm) bool {
	if *a.SolveRate != *b.SolveRate {
		return *a.SolveRate > *b.SolveRate
	}
	if a.LatencyP50MS != nil && b.LatencyP50MS != nil {
		return *a.LatencyP50MS < *b.LatencyP50MS
	}
	return false
}

// p4OtherServed is the sentence naming the most frequent other served model.
func p4OtherServed(pa providerArm) string {
	other, n := "", 0
	for m, c := range pa.ServedModels {
		if modelid.SameModel(pa.Model, m) {
			continue
		}
		if c > n || (c == n && m < other) {
			other, n = m, c
		}
	}
	if other == "" {
		return ""
	}
	return fmt.Sprintf("%s answered %d of %d calls with %s, not the model asked for.", p4ProviderName(pa.Provider), n, pa.Calls, other)
}

func p4ProvidersHeadline(a, b providerArm, otherServed string) string {
	var s strings.Builder
	verb := "matched"
	if b.SolveDelta.High != nil && *b.SolveDelta.High < 0 {
		verb = "fell behind"
	} else if b.SolveDelta.Low != nil && *b.SolveDelta.Low > 0 {
		verb = "beat"
	}
	fmt.Fprintf(&s, "%s %s %s on solve rate (%s points", p4ProviderName(b.Provider), verb, p4ProviderName(a.Provider), p4Points(b.SolveDelta.Point))
	if b.SolveDelta.Low != nil && b.SolveDelta.High != nil {
		fmt.Fprintf(&s, ", range %s to %s", p4Points(*b.SolveDelta.Low), p4Points(*b.SolveDelta.High))
	}
	s.WriteString(")")
	if a.LatencyP50MS != nil && b.LatencyP50MS != nil && *a.LatencyP50MS > 0 {
		pct := math.Round((*b.LatencyP50MS / *a.LatencyP50MS - 1) * 100)
		switch {
		case pct > 0:
			fmt.Fprintf(&s, ", %.0f%% slower per call", pct)
		case pct < 0:
			fmt.Fprintf(&s, ", %.0f%% faster per call", -pct)
		default:
			s.WriteString(", as fast per call")
		}
	}
	if a.ErrorRate != nil && b.ErrorRate != nil {
		fmt.Fprintf(&s, ", errors %.1f%% against %.1f%%", b.ErrorRate.Point*100, a.ErrorRate.Point*100)
	}
	s.WriteString(".")
	switch {
	case otherServed != "":
		s.WriteString(" " + otherServed)
	case b.ServedKnown == 0:
		fmt.Fprintf(&s, " Heimdall could not see which model %s served.", p4ProviderName(b.Provider))
	default:
		fmt.Fprintf(&s, " Every %s call answered with the model asked for (%d of %d).", p4ProviderName(b.Provider), b.ServedKnown, b.ServedKnown)
	}
	return s.String()
}

// p4ProvidersSetting is the routing rule: the chosen setup first, the others
// as fallbacks ordered by error rate.
func p4ProvidersSetting(arms []providerArm, best int) *BifrostSetting {
	primary := arms[best]
	var rest []providerArm
	for i, pa := range arms {
		if i != best && pa.Provider != "" {
			rest = append(rest, pa)
		}
	}
	errRate := func(pa providerArm) float64 {
		if pa.ErrorRate == nil {
			return 1
		}
		return pa.ErrorRate.Point
	}
	slices.SortStableFunc(rest, func(x, y providerArm) int {
		switch a, b := errRate(x), errRate(y); {
		case a < b:
			return -1
		case a > b:
			return 1
		}
		return 0
	})
	family := modelid.Family(primary.Model)
	rule := RoutingRule{
		Name:          "heimdall-providers-" + family,
		Description:   "Measured by a Heimdall test of one model on several providers.",
		CelExpression: fmt.Sprintf("model == %q", family),
		Targets:       []RoutingTarget{{Provider: primary.Provider, Model: primary.Model, Weight: 1}},
		Scope:         "global",
	}
	for _, pa := range rest {
		rule.Fallbacks = append(rule.Fallbacks, pa.Provider+"/"+pa.Model)
	}
	return RuleSetting("Routing rule: "+family+" on "+p4ProviderName(primary.Provider)+" first", "Bifrost tries fallbacks in order when the primary fails.", rule)
}
