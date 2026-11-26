package main

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/failover"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/selfmetrics"
)

// Quality metrics next to Bifrost's own (W4 design 3, feature 13): the solve
// rate, graded runs and cost per solved task of each provider, model and
// benchmark over the last 7 days, on the same provider and model labels
// Bifrost's Prometheus metrics use. One Grafana chart can then show cost from
// Bifrost and quality from Heimdall per model, for example:
//
//	sum by (provider, model) (rate(bifrost_cost_total[1h]))
//	heimdall_solve_rate
//
// The numbers are computed every 5 minutes in the background; a scrape only
// reads the last computed samples. Labels are bounded: provider, model and
// benchmark only (plus org, the organization's slug, on a hosted server that
// turned the metrics on), never a run id, task id, agent name or trace text,
// and at most 100 series; the rest are counted in
// heimdall_quality_series_dropped. A hosted server publishes them only with
// HEIMDALL_QUALITY_METRICS=on, because its /metrics would then mix
// organizations.

const (
	qualityEnv            = "HEIMDALL_QUALITY_METRICS"
	qualityInterval       = 5 * time.Minute
	qualityWindow         = 7 * 24 * time.Hour
	qualityMaxExperiments = 200 // per organization, newest first
	qualityMaxSeries      = 100
)

// qualityOrg is one organization's run index.
type qualityOrg struct {
	slug string
	src  leaderboard.Source
}

// qualitySnapshot is one computed set of samples.
type qualitySnapshot struct {
	solve, graded, costPerSolved []selfmetrics.Sample
	dropped                      float64
}

// qualityMetrics holds the last snapshot the background loop computed.
type qualityMetrics struct {
	orgs    func(ctx context.Context) []qualityOrg
	withOrg bool

	mu   sync.Mutex
	last qualitySnapshot
}

var (
	qualityLoopOnce sync.Once
	qualityCurrent  struct {
		sync.Mutex
		q *qualityMetrics
	}
)

// registerQualityMetrics registers the quality gauges on reg, read from the
// organizations of h, and starts the background loop once per process (the
// server owns it; it never stops). A hosted server without
// HEIMDALL_QUALITY_METRICS=on registers nothing.
func registerQualityMetrics(reg *selfmetrics.Registry, h *jobsHub) {
	if h == nil || reg == nil {
		return
	}
	if h.hosted && strings.TrimSpace(os.Getenv(qualityEnv)) != "on" {
		return
	}
	q := &qualityMetrics{orgs: h.qualityOrgs, withOrg: h.hosted}
	q.register(reg)
	qualityCurrent.Lock()
	qualityCurrent.q = q
	qualityCurrent.Unlock()
	qualityLoopOnce.Do(func() { go qualityLoop() })
}

// qualityLoop recomputes the newest registered metrics every 5 minutes.
func qualityLoop() {
	t := time.NewTicker(qualityInterval)
	defer t.Stop()
	for range t.C {
		qualityCurrent.Lock()
		q := qualityCurrent.q
		qualityCurrent.Unlock()
		if q != nil {
			q.refresh(context.Background())
		}
	}
}

// register adds the four gauges; each reads the last snapshot only.
func (q *qualityMetrics) register(reg *selfmetrics.Registry) {
	read := func(pick func(qualitySnapshot) []selfmetrics.Sample) selfmetrics.CollectFunc {
		return func(context.Context) []selfmetrics.Sample {
			q.mu.Lock()
			defer q.mu.Unlock()
			return slices.Clone(pick(q.last))
		}
	}
	reg.Gauge("heimdall_solve_rate", "Mean over tasks of the verified success rate of the last 7 days, by provider, model and benchmark.",
		read(func(s qualitySnapshot) []selfmetrics.Sample { return s.solve }))
	reg.Gauge("heimdall_graded_runs", "Graded clean runs counted in heimdall_solve_rate.",
		read(func(s qualitySnapshot) []selfmetrics.Sample { return s.graded }))
	reg.Gauge("heimdall_cost_per_solved_task_usd", "Total cost of the counted runs over the solved ones, in USD.",
		read(func(s qualitySnapshot) []selfmetrics.Sample { return s.costPerSolved }))
	reg.Gauge("heimdall_quality_series_dropped", "Quality series left out because of the 100 series limit.",
		read(func(s qualitySnapshot) []selfmetrics.Sample { return []selfmetrics.Sample{{Value: s.dropped}} }))
}

func (q *qualityMetrics) store(s qualitySnapshot) {
	q.mu.Lock()
	q.last = s
	q.mu.Unlock()
}

// refresh computes a new snapshot; a panic leaves the last one in place.
func (q *qualityMetrics) refresh(ctx context.Context) {
	defer func() { _ = recover() }()
	ctx, cancel := context.WithTimeout(ctx, qualityInterval)
	defer cancel()
	q.store(computeQualitySnapshot(ctx, q.orgs(ctx), time.Now(), q.withOrg))
}

// qualityOrgs is each organization launcher's run index.
func (h *jobsHub) qualityOrgs(ctx context.Context) []qualityOrg {
	h.mu.Lock()
	ls := make([]*launcher, 0, len(h.orgs))
	for _, l := range h.orgs {
		ls = append(ls, l)
	}
	h.mu.Unlock()
	var out []qualityOrg
	for _, l := range ls {
		if l == nil || l.ix == nil || l.a == nil || l.a.store == nil {
			continue
		}
		slug := l.org
		if h.hosted && h.db != nil {
			if o, err := h.db.OrgByID(ctx, l.org); err == nil && o != nil {
				slug = o.Slug
			}
		}
		out = append(out, qualityOrg{slug: slug, src: handlers.LeaderboardSource(indexAdapter{ix: l.ix, root: l.a.store.Root()})})
	}
	return out
}

// qualityKey is one series.
type qualityKey struct{ org, provider, model, benchmark string }

type qualityAcc struct {
	graded, solved int
	cost           float64
	tasks          map[string][2]int // task: solved, graded
}

// computeQualitySnapshot reads the counted runs of every organization's
// experiments of the last 7 days into bounded samples.
func computeQualitySnapshot(ctx context.Context, orgs []qualityOrg, now time.Time, withOrg bool) qualitySnapshot {
	accs := map[qualityKey]*qualityAcc{}
	for _, o := range orgs {
		qualityReadOrg(ctx, o, now, withOrg, accs)
	}
	keys := make([]qualityKey, 0, len(accs))
	for k, a := range accs {
		if a.graded > 0 {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b qualityKey) int {
		if c := cmp.Compare(accs[b].graded, accs[a].graded); c != 0 {
			return c
		}
		return cmp.Or(cmp.Compare(a.org, b.org), cmp.Compare(a.provider, b.provider), cmp.Compare(a.model, b.model), cmp.Compare(a.benchmark, b.benchmark))
	})
	snap := qualitySnapshot{solve: []selfmetrics.Sample{}, graded: []selfmetrics.Sample{}, costPerSolved: []selfmetrics.Sample{}}
	if len(keys) > qualityMaxSeries {
		snap.dropped = float64(len(keys) - qualityMaxSeries)
		keys = keys[:qualityMaxSeries]
	}
	for _, k := range keys {
		a := accs[k]
		labels := map[string]string{"provider": k.provider, "model": k.model, "benchmark": k.benchmark}
		if withOrg {
			labels["org"] = k.org
		}
		sum := 0.0
		for _, t := range a.tasks {
			sum += float64(t[0]) / float64(t[1])
		}
		snap.solve = append(snap.solve, selfmetrics.Sample{Labels: labels, Value: sum / float64(len(a.tasks))})
		snap.graded = append(snap.graded, selfmetrics.Sample{Labels: labels, Value: float64(a.graded)})
		if a.solved > 0 {
			snap.costPerSolved = append(snap.costPerSolved, selfmetrics.Sample{Labels: labels, Value: a.cost / float64(a.solved)})
		}
	}
	return snap
}

func qualityReadOrg(ctx context.Context, o qualityOrg, now time.Time, withOrg bool, accs map[qualityKey]*qualityAcc) {
	type exp struct {
		id string
		m  *schemas.Manifest
	}
	var exps []exp
	for _, id := range o.src.Experiments() {
		m, ok := o.src.Manifest(id)
		if !ok || m == nil || now.Sub(m.CreatedAt) > qualityWindow || m.CreatedAt.After(now) {
			continue
		}
		e := m.Experiment
		if e.Smoke || e.ScriptedUpstream || e.NoPaidTraffic() || e.Debug != nil || e.RerunOf != "" {
			continue
		}
		if d, ok := o.src.LatestDecision(id); ok && d == schemas.DecisionInvalid {
			continue
		}
		exps = append(exps, exp{id, m})
	}
	slices.SortFunc(exps, func(a, b exp) int { return cmp.Or(b.m.CreatedAt.Compare(a.m.CreatedAt), cmp.Compare(a.id, b.id)) })
	if len(exps) > qualityMaxExperiments {
		exps = exps[:qualityMaxExperiments]
	}
	for _, x := range exps {
		if ctx.Err() != nil {
			return
		}
		synthetic, _ := leaderboard.SyntheticArms(ctx, x.m, nil, nil)
		variants := map[string]bool{}
		for _, suffix := range []string{schemas.EvaluationCacheSuffix, schemas.StreamControlSuffix, failover.ArmLabelSuffix} {
			if id := schemas.VariantArmID(x.m, suffix); id != "" {
				variants[id] = true
			}
		}
		bySlot := map[string]leaderboard.Run{}
		for _, r := range o.src.Runs(x.id) {
			if !r.Sealed || r.Superseded || r.Run.Outcome == schemas.OutcomeAbortedInfra || cmp.Or(r.Slot.Condition, schemas.ConditionClean) != schemas.ConditionClean {
				continue
			}
			if prev, ok := bySlot[r.Slot.SlotID]; !ok || r.Run.Attempt > prev.Run.Attempt {
				bySlot[r.Slot.SlotID] = r
			}
		}
		benchmark, _, _ := strings.Cut(x.m.SuiteID, "@")
		for _, r := range bySlot {
			arm := r.Slot.Arm
			if synthetic[arm] || variants[arm] || schemas.CacheVariantArm(x.m, arm) || r.Run.Practice || r.Run.HumanAssisted || r.Slot.StreamReplay {
				continue
			}
			cfg := x.m.Arms[arm].Config
			metrics := qualityRunMetrics(o.src, r.Run.RunID)
			y, ok := leaderboard.VerifiedSuccess(r.Run.Outcome, metrics)
			if !ok {
				continue
			}
			model := cfg.Model
			if i := strings.LastIndex(model, "/"); i >= 0 {
				model = model[i+1:]
			}
			k := qualityKey{provider: cfg.Gateway.Provider, model: model, benchmark: benchmark}
			if withOrg {
				k.org = o.slug
			}
			a := accs[k]
			if a == nil {
				a = &qualityAcc{tasks: map[string][2]int{}}
				accs[k] = a
			}
			a.graded++
			t := a.tasks[r.Slot.Task]
			t[1]++
			if y >= 1 {
				a.solved++
				t[0]++
			}
			a.tasks[r.Slot.Task] = t
			if v, ok := metrics["cost_usd"].Float(); ok {
				a.cost += v
			}
		}
	}
}

func qualityRunMetrics(src leaderboard.Source, runID string) map[string]schemas.Value {
	data, err := src.RunFile(runID, "metrics.json")
	if err != nil {
		return nil
	}
	var m map[string]schemas.Value
	if json.Unmarshal(data, &m) != nil {
		return nil
	}
	return m
}
