package canary

import (
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/stats"
)

// promptTokenTolerance is how far the first-prompt input token count of a
// task may move before it counts as changed: identical prompts should count
// the same, but a provider may add a few tokens of framing.
const promptTokenTolerance = 0.01

// clustersShown is how many new failure clusters the headline lines name;
// the evidence keeps every one.
const clustersShown = 3

// ArmChanges is the "what changed" evidence of one target.
type ArmChanges struct {
	Arm                 string     `json:"arm"`
	Label               string     `json:"label"`
	ServedBefore        []string   `json:"served_before"`
	ServedNow           []string   `json:"served_now"`
	ServedChanged       bool       `json:"served_changed"` // the served model set differs under the same requested alias
	ProvidersBefore     []string   `json:"providers_before"`
	ProvidersNow        []string   `json:"providers_now"`
	ProviderChanged     bool       `json:"provider_changed"`
	FingerprintsBefore  []string   `json:"fingerprints_before"`
	FingerprintsNow     []string   `json:"fingerprints_now"`
	FingerprintChanged  bool       `json:"fingerprint_changed"`
	LatencyBeforeMS     *float64   `json:"latency_before_ms"` // median over runs of the run's median request latency
	LatencyNowMS        *float64   `json:"latency_now_ms"`
	LatencyShift        *float64   `json:"latency_shift"` // relative
	PromptTasks         int        `json:"prompt_tasks"`  // tasks with a first-prompt token count on both sides
	PromptChanged       int        `json:"prompt_changed"`
	PromptShift         *float64   `json:"prompt_shift"` // median relative change over those tasks
	ErrorRateBefore     float64    `json:"error_rate_before"`
	ErrorRateNow        float64    `json:"error_rate_now"`
	FormatErrorsBefore  int        `json:"format_errors_before"`
	FormatErrorsNow     int        `json:"format_errors_now"`
	Flips               []TaskFlip `json:"flips"`
	SuccessBefore       *float64   `json:"success_before"`
	SuccessNow          *float64   `json:"success_now"`
	RequestsBefore      int        `json:"requests_before"`
	RequestsNow         int        `json:"requests_now"`
	RequestedAliasesNow []string   `json:"requested_now"`
}

// Changes is what moved between the baseline and a look. Moved lists the
// headline lines an alert carries, most telling first.
type Changes struct {
	Moved       []string     `json:"moved"`
	Arms        []ArmChanges `json:"arms"`
	NewClusters []ClusterRef `json:"new_clusters"` // failure clusters of the look that the baseline did not have
}

// WhatChanged builds the evidence of a look against the baseline: served
// model, provider and system fingerprint per target, latency, token counts
// at identical prompts, error rates, per-task flips, new failure clusters,
// and the flagged tests. It is pure.
func WhatChanged(base, cur *Execution, cmp *Comparison, baseClusters []string) *Changes {
	ch := &Changes{Moved: []string{}, Arms: []ArmChanges{}, NewClusters: []ClusterRef{}}
	bServed, cServed := servedByArm(base), servedByArm(cur)
	for _, arm := range sortedKeys(cur.Arms) {
		a := ArmChanges{Arm: arm, Label: cur.Arms[arm], Flips: []TaskFlip{}}
		bs, cs := bServed[arm], cServed[arm]
		a.ServedBefore, a.ServedNow = nonNil(bs.Served), nonNil(cs.Served)
		a.ProvidersBefore, a.ProvidersNow = nonNil(bs.Providers), nonNil(cs.Providers)
		a.FingerprintsBefore, a.FingerprintsNow = nonNil(bs.Fingerprints), nonNil(cs.Fingerprints)
		a.RequestsBefore, a.RequestsNow = bs.Requests, cs.Requests
		a.RequestedAliasesNow = nonNil(cs.Requested)
		// Only answered requests name a served model; a side with none (an
		// outage) says nothing about which model would have answered.
		a.ServedChanged = len(bs.Served) > 0 && len(cs.Served) > 0 && !slices.Equal(bs.Served, cs.Served)
		a.ProviderChanged = len(bs.Providers) > 0 && len(cs.Providers) > 0 && !slices.Equal(bs.Providers, cs.Providers)
		a.FingerprintChanged = len(bs.Fingerprints) > 0 && len(cs.Fingerprints) > 0 && !slices.Equal(bs.Fingerprints, cs.Fingerprints)
		a.LatencyBeforeMS, a.LatencyNowMS = medianOf(base.Runs, arm, MetricLatency), medianOf(cur.Runs, arm, MetricLatency)
		if a.LatencyBeforeMS != nil && a.LatencyNowMS != nil && *a.LatencyBeforeMS > 0 {
			s := *a.LatencyNowMS / *a.LatencyBeforeMS - 1
			a.LatencyShift = &s
		}
		a.PromptTasks, a.PromptChanged, a.PromptShift = promptShift(base.Runs, cur.Runs, arm)
		a.ErrorRateBefore, a.FormatErrorsBefore = errorRate(base.Runs, arm)
		a.ErrorRateNow, a.FormatErrorsNow = errorRate(cur.Runs, arm)
		a.SuccessBefore, a.SuccessNow = successOf(base.Runs, arm), successOf(cur.Runs, arm)
		if cmp != nil {
			for _, f := range cmp.Flips {
				if f.Arm == arm {
					a.Flips = append(a.Flips, f)
				}
			}
		}
		ch.Arms = append(ch.Arms, a)
	}
	for _, c := range cur.Clusters {
		if !slices.Contains(baseClusters, c.Signature) {
			ch.NewClusters = append(ch.NewClusters, c)
		}
	}
	ch.Moved = movedLines(ch, cmp)
	return ch
}

// movedLines turns the evidence into the alert's lines: flagged tests
// first, then deterministic changes (served model, provider, fingerprint,
// prompt tokens), then shifts and new clusters.
func movedLines(ch *Changes, cmp *Comparison) []string {
	var out []string
	if cmp != nil {
		out = append(out, cmp.FlaggedSummary()...)
	}
	for _, a := range ch.Arms {
		if a.ServedChanged {
			out = append(out, fmt.Sprintf("%s: served model changed under the same alias (%s): %s before, %s now",
				a.Label, joinOr(a.RequestedAliasesNow, "?"), joinOr(a.ServedBefore, "none"), joinOr(a.ServedNow, "none")))
		}
		if a.ProviderChanged {
			out = append(out, fmt.Sprintf("%s: provider changed: %s before, %s now", a.Label, joinOr(a.ProvidersBefore, "none"), joinOr(a.ProvidersNow, "none")))
		}
		if a.FingerprintChanged {
			out = append(out, fmt.Sprintf("%s: system fingerprint changed: %s before, %s now", a.Label, joinOr(a.FingerprintsBefore, "none"), joinOr(a.FingerprintsNow, "none")))
		}
		if a.PromptChanged > 0 && a.PromptShift != nil {
			out = append(out, fmt.Sprintf("%s: input tokens at the identical first prompt changed on %d of %d tasks (median %+.1f%%)", a.Label, a.PromptChanged, a.PromptTasks, 100**a.PromptShift))
		}
		// A latency shift is headline evidence only when its test says it is
		// not noise (or when no comparison was made, as in a provider pair).
		if a.LatencyShift != nil && math.Abs(*a.LatencyShift) >= DefaultFloorLatency && significant(cmp, a.Arm, MetricLatency) {
			out = append(out, fmt.Sprintf("%s: median model latency %+.0f%% (%.0f ms to %.0f ms)", a.Label, 100**a.LatencyShift, *a.LatencyBeforeMS, *a.LatencyNowMS))
		}
		if a.ErrorRateNow-a.ErrorRateBefore >= 0.05 {
			out = append(out, fmt.Sprintf("%s: model request errors %.0f%% (from %.0f%%)", a.Label, 100*a.ErrorRateNow, 100*a.ErrorRateBefore))
		}
		if worse := countFlips(a.Flips, true); worse > 0 {
			out = append(out, fmt.Sprintf("%s: %d task(s) lost success: %s", a.Label, worse, flipTasks(a.Flips, true)))
		}
	}
	for i, c := range ch.NewClusters {
		if i == clustersShown {
			out = append(out, fmt.Sprintf("%d more new failure clusters", len(ch.NewClusters)-clustersShown))
			break
		}
		out = append(out, fmt.Sprintf("new failure cluster %s %q (%d runs; tasks %s)", c.Signature, c.Title, c.Count, strings.Join(c.Tasks, ", ")))
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// significant reports whether a comparison found a metric of an arm worse
// or better at its level; true without a comparison.
func significant(cmp *Comparison, arm, metric string) bool {
	if cmp == nil {
		return true
	}
	for _, t := range cmp.Tests {
		if t.Arm == arm && t.Metric == metric && (t.Status == TestWorse || t.Status == TestBetter) {
			return true
		}
	}
	return false
}

// servedByArm indexes ServedSummary by arm.
func servedByArm(x *Execution) map[string]Served {
	out := map[string]Served{}
	if x == nil {
		return out
	}
	for _, s := range ServedSummary(x) {
		out[s.Arm] = s
	}
	return out
}

// medianOf is the median of a metric over an arm's runs.
func medianOf(runs []RunObs, arm, metric string) *float64 {
	var xs []float64
	for _, r := range runs {
		if r.Arm == arm {
			if v := r.Value(metric); v != nil {
				xs = append(xs, *v)
			}
		}
	}
	if len(xs) == 0 {
		return nil
	}
	m := stats.Median(xs)
	return &m
}

// successOf is the mean over tasks of the per-task success rate of an arm.
func successOf(runs []RunObs, arm string) *float64 {
	means := taskMeans(runs, MetricSuccess)
	var xs []float64
	for k, v := range means {
		if k.arm == arm {
			xs = append(xs, v)
		}
	}
	if len(xs) == 0 {
		return nil
	}
	m := stats.Mean(xs)
	return &m
}

// promptShift compares the median first-prompt input tokens per task.
func promptShift(base, cur []RunObs, arm string) (tasks, changed int, shift *float64) {
	med := func(runs []RunObs) map[string]float64 {
		by := map[string][]float64{}
		for _, r := range runs {
			if r.Arm == arm && r.FirstPromptTokens != nil {
				by[r.Task] = append(by[r.Task], float64(*r.FirstPromptTokens))
			}
		}
		out := map[string]float64{}
		for t, xs := range by {
			out[t] = stats.Median(xs)
		}
		return out
	}
	b, c := med(base), med(cur)
	var rel []float64
	for _, t := range sortedKeys(c) {
		bv, ok := b[t]
		if !ok || bv <= 0 {
			continue
		}
		tasks++
		r := c[t]/bv - 1
		rel = append(rel, r)
		if math.Abs(r) > promptTokenTolerance {
			changed++
		}
	}
	if len(rel) > 0 {
		m := stats.Median(rel)
		shift = &m
	}
	return tasks, changed, shift
}

// errorRate is the share of an arm's model requests that errored, and its
// format errors.
func errorRate(runs []RunObs, arm string) (float64, int) {
	req, errs, format := 0, 0, 0
	for _, r := range runs {
		if r.Arm == arm {
			req += r.ModelRequests
			errs += r.ModelErrors
			format += r.FormatErrors
		}
	}
	if req == 0 {
		return 0, format
	}
	return float64(errs) / float64(req), format
}

// countFlips counts flips that lost (worse) or gained success.
func countFlips(fs []TaskFlip, worse bool) int {
	n := 0
	for _, f := range fs {
		if (f.Current < f.Baseline) == worse {
			n++
		}
	}
	return n
}

// flipTasks names the tasks of flips in one direction.
func flipTasks(fs []TaskFlip, worse bool) string {
	var out []string
	for _, f := range fs {
		if (f.Current < f.Baseline) == worse {
			out = append(out, fmt.Sprintf("%s %.0f%%->%.0f%%", f.Task, 100*f.Baseline, 100*f.Current))
		}
	}
	return strings.Join(out, ", ")
}

// nonNil returns xs, or an empty slice for nil, so JSON says [] not null.
func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// ServedChangedSince reports, per arm, whether the served models of a look
// differ from the baseline's pinned fingerprint.
func ServedChangedSince(b *Baseline, now []Served) []string {
	var out []string
	before := map[string]Served{}
	for _, s := range b.Served {
		before[s.Arm] = s
	}
	for _, s := range now {
		p, ok := before[s.Arm]
		if ok && len(p.Served) > 0 && len(s.Served) > 0 && !slices.Equal(p.Served, s.Served) {
			out = append(out, fmt.Sprintf("%s: %s -> %s", s.Label, joinOr(p.Served, "none"), joinOr(s.Served, "none")))
		}
	}
	return out
}
