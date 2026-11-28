package modelswitch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// Options are what Build cannot read from the runs.
type Options struct {
	// Segments returns a task's segment labels, key to value (task_kind,
	// capability, tag, difficulty, metadata keys). nil gives no segments.
	// A key may hold several values joined by "\n" (a task's tags).
	Segments func(benchmark, task string) map[string]string
	// Alpha is the significance level; 0 is DefaultAlpha.
	Alpha float64
}

// conf is the confidence of every range.
const conf = 0.95

// bootstrapResamples is the resample count of every bootstrap range.
const bootstrapResamples = 2000

// OwnModel is the model name shown for an arm that runs the agent's own
// model (an empty arm model).
const OwnModel = "the agent's own model"

// openProviders are the Bifrost providers that serve open models from the
// org's own endpoint (vLLM, SGLang).
var openProviders = map[string]bool{"vllm": true, "sgl": true, "sglang": true}

// Build writes the model switch report of ev from src. It is pure apart
// from reading run files through src.
func Build(ctx context.Context, src leaderboard.Source, ev *schemas.Evaluation, opt Options) (*Report, error) {
	if ev == nil {
		return nil, errors.New("modelswitch: evaluation is nil")
	}
	alpha := opt.Alpha
	if alpha == 0 {
		alpha = DefaultAlpha
	}
	if !(alpha > 0 && alpha < 1) {
		return nil, fmt.Errorf("modelswitch: alpha %v is not in (0, 1)", alpha)
	}
	arms := ev.Spec.Arms
	if len(arms) < 2 {
		return nil, fmt.Errorf("%w: evaluation %s has %d model(s); compare at least two", ErrNotModelSwitch, ev.ID, len(arms))
	}
	agent, version := reportcard.AgentRef(arms[0].Agent)
	if arms[0].Version != 0 {
		version = arms[0].Version
	}
	for _, a := range arms[1:] {
		if id, _ := reportcard.AgentRef(a.Agent); id != agent {
			return nil, fmt.Errorf("%w: evaluation %s runs agents %s and %s; a model switch report compares one agent on several models", ErrNotModelSwitch, ev.ID, agent, id)
		}
	}
	rep := &Report{
		Evaluation: ev.ID, Status: ev.Status, Agent: agent, Version: version, Current: arms[0].Label, Alpha: alpha,
		Models: []Model{}, Comparisons: []Comparison{}, Benchmarks: []Group{}, Segments: []Group{}, Missing: []string{}, Notes: []string{},
	}
	labels := make([]string, len(arms))
	for i, a := range arms {
		labels[i] = a.Label
	}

	var benches []benchRuns
	for _, c := range ev.Children {
		if c.Skipped != "" {
			rep.Missing = append(rep.Missing, fmt.Sprintf("Benchmark %s did not run: %s", c.Benchmark, c.Skipped))
			continue
		}
		br, err := readBench(ctx, src, arms, c)
		if err != nil {
			return nil, err
		}
		rep.PracticeRuns += br.practice
		benches = append(benches, br)
	}
	if rep.PracticeRuns > 0 {
		rep.Notes = append(rep.Notes, fmt.Sprintf("%d practice runs were left out: the scripted practice model answered them, so they are not findings.", rep.PracticeRuns))
	}

	// Models: every clean graded run of every benchmark.
	pooled := map[string][]run{}
	excluded := map[string]map[string]int{}
	for _, br := range benches {
		for _, l := range labels {
			pooled[l] = append(pooled[l], br.arms[l]...)
			for why, n := range br.excluded[l] {
				if excluded[l] == nil {
					excluded[l] = map[string]int{}
				}
				excluded[l][why] += n
			}
		}
	}
	for _, a := range arms {
		m := modelOf(a, providerOf(a, benches))
		fillModel(&m, pooled[a.Label])
		m.Excluded = excluded[a.Label]
		if m.Runs == 0 {
			rep.Missing = append(rep.Missing, fmt.Sprintf("%s has no graded run yet: its numbers are unknown, not zero.", display(m)))
		}
		if m.Open {
			rep.Notes = append(rep.Notes, fmt.Sprintf("%s is an open model served on your own endpoint (%s): its GPU time is billed by your GPU provider, not per token, so its cost reads unknown unless a key price is set. Keep a deployment running before you route traffic to it.", display(m), m.Provider))
		}
		rep.Models = append(rep.Models, m)
	}
	rank(rep.Models)
	// A best model needs a solved task: when every model with runs solved
	// none, no model is best and none is recommended (nothingSolved).
	if rep.Models[0].Runs > 0 && !nothingSolved(rep.Models) {
		rep.Best = rep.Models[0].Label
	}

	// Each model against the best, Holm-adjusted together.
	rates := map[string]map[string]float64{}
	for _, l := range labels {
		rates[l] = taskRates(pooled[l])
	}
	if rep.Best != "" {
		var cs []Comparison
		for _, m := range rep.Models[1:] {
			cs = append(cs, compare(m.Label, rep.Best, rates[m.Label], rates[rep.Best]))
		}
		holmComparisons(cs, alpha)
		rep.Comparisons = cs
	}

	rep.Recommendation = recommend(rep, ev, benches)

	// Benchmarks and segments against the default model.
	def := rep.Recommendation.Model
	if def == "" {
		def = rep.Current
	}
	for _, br := range benches {
		g := Group{Key: "benchmark", Value: br.child.Benchmark, Models: map[string]GroupModel{}, Default: def}
		byLabel := map[string][]run{}
		for _, l := range labels {
			byLabel[l] = br.arms[l]
		}
		fillGroup(&g, labels, byLabel)
		if br.manifest == nil {
			g.Note = "no graded run yet"
			rep.Missing = append(rep.Missing, fmt.Sprintf("Benchmark %s has no graded run yet.", br.child.Benchmark))
		}
		rep.Benchmarks = append(rep.Benchmarks, g)
	}
	holmGroups(rep.Benchmarks, alpha)
	rep.Segments = segments(labels, pooled, opt.Segments, def)
	holmGroups(rep.Segments, alpha)

	rep.Pareto = costPareto(rep.Models)
	rep.LatencyPareto = latencyPareto(rep.Models)
	for i := range rep.Models {
		rep.Models[i].Frontier = slices.Contains(rep.Pareto.Frontier, rep.Models[i].Label)
		rep.Models[i].LatencyFrontier = slices.Contains(rep.LatencyPareto.Frontier, rep.Models[i].Label)
	}
	return rep, nil
}

// display names a model for sentences: provider/model, or the label.
func display(m Model) string {
	switch {
	case m.Model == "":
		return m.Label
	case m.Provider != "":
		return m.Provider + "/" + m.Model
	}
	return m.Model
}

// providerOf is the arm's provider: the spec's, else the provider the
// planned config routes to in the first experiment that has the arm.
func providerOf(a schemas.EvaluationArm, benches []benchRuns) string {
	if a.Provider != "" {
		return a.Provider
	}
	for _, br := range benches {
		if br.manifest == nil {
			continue
		}
		for id, l := range br.armOf {
			if l == a.Label {
				if p := br.manifest.Arms[id].Config.Gateway.Provider; p != "" {
					return p
				}
			}
		}
	}
	return ""
}

func modelOf(a schemas.EvaluationArm, provider string) Model {
	m := Model{Label: a.Label, Model: a.Model, Provider: provider, Open: openProviders[strings.ToLower(provider)]}
	if m.Model == "" {
		m.Model = OwnModel
	}
	return m
}

// fillModel computes one model's numbers from its runs.
func fillModel(m *Model, runs []run) {
	m.Runs = len(runs)
	m.Tasks = len(taskRates(runs))
	m.Quality = quality(runs)
	m.CostPerRun, m.CostPerSuccess, m.CostTotal = costs(runs)
	var lat []float64
	known, asAsked := 0, 0
	for _, r := range runs {
		lat = append(lat, r.latencies...)
		m.ModelCalls += r.calls
		m.CallFailures += r.failures
		m.Fallbacks += r.fallbacks
		known += r.servedKnown
		asAsked += r.servedAsAsked
		m.ServedUnknown += r.servedUnknown
		for id, n := range r.servedModels {
			if m.ServedModels == nil {
				m.ServedModels = map[string]int{}
			}
			m.ServedModels[id] += n
		}
	}
	if known > 0 {
		m.ServedAsAsked = schemas.Observed(round(float64(asAsked)/float64(known)), schemas.EvidenceGateway)
	} else {
		m.ServedAsAsked = schemas.Unknown("no model call in the gateway rows names the model that answered", schemas.EvidenceGateway)
	}
	m.LatencyP50MS = percentileValue(lat, 0.5, "no answered model call with a latency in the gateway rows", schemas.EvidenceGateway)
	m.LatencyP95MS = percentileValue(lat, 0.95, "no answered model call with a latency in the gateway rows", schemas.EvidenceGateway)
	var walls []float64
	for _, r := range runs {
		if r.wall != nil {
			walls = append(walls, *r.wall)
		}
	}
	m.WallP50S = percentileValue(walls, 0.5, "no run recorded its wall time", schemas.EvidenceOutcome)
	if m.ModelCalls > 0 {
		m.Availability = schemas.Observed(round(float64(m.ModelCalls-m.CallFailures)/float64(m.ModelCalls)), schemas.EvidenceGateway)
	} else {
		m.Availability = schemas.Unknown("no model call in the gateway rows", schemas.EvidenceGateway)
	}
}

// key is a task across benchmarks.
func key(r run) string { return r.bench + "\x00" + r.task }

// taskRates is each task's success rate.
func taskRates(runs []run) map[string]float64 {
	sum, n := map[string]float64{}, map[string]int{}
	for _, r := range runs {
		k := key(r)
		sum[k] += float64(r.success)
		n[k]++
	}
	out := make(map[string]float64, len(sum))
	for k, s := range sum {
		out[k] = s / float64(n[k])
	}
	return out
}

// quality is the pooled verified success rate with its 95% range: a
// task-clustered bootstrap with two or more tasks, Clopper-Pearson with one.
func quality(runs []run) schemas.Value {
	if len(runs) == 0 {
		return schemas.Unknown("no graded run", schemas.EvidenceOutcome)
	}
	sum, n := map[string]float64{}, map[string]int{}
	succ := 0
	for _, r := range runs {
		sum[key(r)] += float64(r.success)
		n[key(r)]++
		succ += r.success
	}
	cs := make([]stats.Cluster, 0, len(sum))
	keys := sortedKeys(sum)
	for _, k := range keys {
		cs = append(cs, stats.Cluster{Sum: sum[k], N: n[k]})
	}
	p := float64(succ) / float64(len(runs))
	lo, hi, ok := stats.ClusterBootstrapCI(cs, conf, bootstrapResamples, uint64(len(runs))<<20|uint64(len(cs)))
	if !ok {
		lo, hi = stats.ClopperPearson(succ, len(runs), conf)
	}
	v := schemas.Observed(round(p), schemas.EvidenceOutcome)
	v.Bounds = &[2]float64{round(lo), round(hi)}
	return v
}

// costs are the mean cost per run, the cost per verified success and the
// total, unknown when any run's cost is not known (an unpriced model is
// never $0).
func costs(runs []run) (perRun, perSuccess, total schemas.Value) {
	if len(runs) == 0 {
		u := schemas.Unknown("no graded run", schemas.EvidenceGateway)
		return u, u, u
	}
	t, known, succ := 0.0, 0, 0
	for _, r := range runs {
		succ += r.success
		if r.cost != nil {
			t += *r.cost
			known++
		}
	}
	if known < len(runs) {
		why := fmt.Sprintf("cost known for %d of %d runs (an unpriced model or a run without a cost ledger)", known, len(runs))
		u := schemas.Unknown(why, schemas.EvidenceGateway)
		return u, u, u
	}
	perRun = schemas.Observed(round6(t/float64(len(runs))), schemas.EvidenceGateway)
	total = schemas.Observed(round6(t), schemas.EvidenceGateway)
	if succ == 0 {
		perSuccess = schemas.Unknown("no verified success", schemas.EvidenceGateway)
	} else {
		perSuccess = schemas.Observed(round6(t/float64(succ)), schemas.EvidenceGateway)
	}
	return perRun, perSuccess, total
}

// percentileValue is the q quantile of xs (nearest rank), unknown when xs
// is empty.
func percentileValue(xs []float64, q float64, why string, ev schemas.EvidenceLevel) schemas.Value {
	if len(xs) == 0 {
		return schemas.Unknown(why, ev)
	}
	s := slices.Clone(xs)
	slices.Sort(s)
	i := int(math.Ceil(q*float64(len(s)))) - 1
	i = max(0, min(i, len(s)-1))
	return schemas.Observed(round2(s[i]), ev)
}

// rank orders models by quality (unknown last), then cost per success
// (unknown last), then label.
func rank(ms []Model) {
	slices.SortStableFunc(ms, func(a, b Model) int {
		qa, oka := a.Quality.Float()
		qb, okb := b.Quality.Float()
		if oka != okb {
			return trueFirst(oka, okb)
		}
		if c := cmp.Compare(qb, qa); c != 0 {
			return c
		}
		ca, oka := a.CostPerSuccess.Float()
		cb, okb := b.CostPerSuccess.Float()
		if oka != okb {
			return trueFirst(oka, okb)
		}
		return cmp.Or(cmp.Compare(ca, cb), cmp.Compare(a.Label, b.Label))
	})
}

func trueFirst(a, b bool) int {
	if a == b {
		return 0
	}
	if a {
		return -1
	}
	return 1
}

// compare tests label against best on their common tasks: the exact
// two-sided sign-flip test on the per-task differences, with a bootstrap
// range of the mean difference. P is raw; holmComparisons adjusts it.
func compare(label, best string, a, b map[string]float64) Comparison {
	c := Comparison{Label: label, Against: best, Result: ResultNoData}
	var d []float64
	for _, k := range sortedKeys(a) {
		if y, ok := b[k]; ok {
			d = append(d, a[k]-y)
		}
	}
	c.Tasks = len(d)
	if len(d) == 0 {
		c.Reason = "no task both models ran"
		return c
	}
	s := 0.0
	cs := make([]stats.Cluster, len(d))
	for i, x := range d {
		s += x
		cs[i] = stats.Cluster{Sum: x, N: 1}
	}
	c.MeanDiff = round(s / float64(len(d)))
	if lo, hi, ok := stats.ClusterBootstrapCI(cs, conf, bootstrapResamples, uint64(len(d))<<8|0x5d); ok {
		lo, hi = round(lo), round(hi)
		c.DiffLow, c.DiffHigh = &lo, &hi
	}
	p := stats.SignFlip(d).P
	c.P = &p
	return c
}

// holmComparisons adjusts the comparisons' p values together (Holm) and
// sets each result and reason.
func holmComparisons(cs []Comparison, alpha float64) {
	var idx []int
	var ps []float64
	for i, c := range cs {
		if c.P != nil {
			idx, ps = append(idx, i), append(ps, *c.P)
		}
	}
	adj := holm(ps)
	for k, i := range idx {
		a := round4(adj[k])
		c := &cs[i]
		c.PAdjusted = &a
		pct := math.Abs(c.MeanDiff) * 100
		switch {
		case a < alpha && c.MeanDiff < 0:
			c.Result = ResultWorse
			c.Reason = fmt.Sprintf("%.0f points lower than %s on %d tasks (p %.3g after Holm)", pct, c.Against, c.Tasks, a)
		case a < alpha && c.MeanDiff > 0:
			// Only possible when the best was picked on point estimates that
			// differ from the paired tasks; say it as measured.
			c.Result = ResultCannotTell
			c.Reason = fmt.Sprintf("%.0f points higher than %s on the %d tasks both ran (p %.3g after Holm)", pct, c.Against, c.Tasks, a)
		default:
			c.Result = ResultCannotTell
			c.Reason = fmt.Sprintf("cannot be told apart from %s on %d tasks (difference %+.0f points, p %.3g after Holm)", c.Against, c.Tasks, c.MeanDiff*100, a)
		}
	}
	for i := range cs {
		if cs[i].P == nil && cs[i].Reason == "" {
			cs[i].Reason = "no task both models ran"
		}
	}
}

// holm is the Holm step-down adjustment of p.
func holm(p []float64) []float64 {
	n := len(p)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(p[a], p[b]) })
	out := make([]float64, n)
	running := 0.0
	for rank, i := range order {
		v := math.Min(1, float64(n-rank)*p[i])
		if math.IsNaN(p[i]) {
			v = 1
		}
		running = math.Max(running, v)
		out[i] = running
	}
	return out
}

// fillGroup computes every model's numbers in a group, the group's best
// model and, when it differs from the default, the raw comparison of the
// best with the default.
func fillGroup(g *Group, labels []string, byLabel map[string][]run) {
	tasks := map[string]bool{}
	type cand struct {
		label string
		q     float64
		cost  float64
		okC   bool
	}
	var cands []cand
	for _, l := range labels {
		runs := byLabel[l]
		gm := GroupModel{Runs: len(runs), Tasks: len(taskRates(runs)), Quality: quality(runs)}
		gm.CostPerRun, _, _ = costs(runs)
		var lat []float64
		for _, r := range runs {
			tasks[key(r)] = true
			lat = append(lat, r.latencies...)
		}
		gm.LatencyP50 = percentileValue(lat, 0.5, "no answered model call with a latency", schemas.EvidenceGateway)
		g.Models[l] = gm
		if q, ok := gm.Quality.Float(); ok {
			c, okC := gm.CostPerRun.Float()
			cands = append(cands, cand{l, q, c, okC})
		}
	}
	g.Tasks = len(tasks)
	if len(cands) == 0 {
		return
	}
	slices.SortStableFunc(cands, func(a, b cand) int {
		if c := cmp.Compare(b.q, a.q); c != 0 {
			return c
		}
		if a.okC != b.okC {
			return trueFirst(a.okC, b.okC)
		}
		return cmp.Compare(a.cost, b.cost)
	})
	g.Best = cands[0].label
	if g.Default == "" || g.Best == g.Default {
		return
	}
	if dq, ok := g.Models[g.Default].Quality.Float(); ok && dq == cands[0].q {
		g.Best = g.Default // a tie keeps the default
		return
	}
	c := compare(g.Best, g.Default, taskRates(byLabel[g.Best]), taskRates(byLabel[g.Default]))
	g.VsDefault = &c
}

// holmGroups adjusts the groups' best-against-default p values together
// and words each result: "better" here means the group's best beats the
// default model on the group's tasks.
func holmGroups(gs []Group, alpha float64) {
	var idx []int
	var ps []float64
	for i, g := range gs {
		if g.VsDefault != nil && g.VsDefault.P != nil && !g.Small {
			idx, ps = append(idx, i), append(ps, *g.VsDefault.P)
		}
	}
	adj := holm(ps)
	for k, i := range idx {
		a := round4(adj[k])
		c := gs[i].VsDefault
		c.PAdjusted = &a
		pts := c.MeanDiff * 100
		if a < alpha && c.MeanDiff > 0 {
			c.Result = "better"
			c.Reason = fmt.Sprintf("%s scores %.0f points higher than %s on these %d tasks (p %.3g after Holm across %d groups)", c.Label, pts, c.Against, c.Tasks, a, len(ps))
		} else {
			c.Result = ResultCannotTell
			c.Reason = fmt.Sprintf("%s scores %+.0f points against %s on these %d tasks; not significant after Holm across %d groups (p %.3g)", c.Label, pts, c.Against, c.Tasks, len(ps), a)
		}
	}
	for i := range gs {
		if c := gs[i].VsDefault; c != nil && c.Reason == "" {
			c.Result = ResultNoData
			c.Reason = "no task both models ran"
			if gs[i].Small {
				c.Result = ResultCannotTell
				c.Reason = fmt.Sprintf("too few tasks to compare (%d; at least %d)", gs[i].Tasks, MinSegmentTasks)
			}
		}
	}
}

// Segment caps.
const (
	maxSegmentKeys   = 12
	maxSegmentValues = 30
)

// segments groups the pooled runs by every segment key and value.
func segments(labels []string, pooled map[string][]run, segOf func(string, string) map[string]string, def string) []Group {
	if segOf == nil {
		return []Group{}
	}
	type kv struct{ k, v string }
	byKV := map[kv]map[string][]run{}
	cache := map[string]map[string]string{}
	for _, l := range labels {
		for _, r := range pooled[l] {
			ck := key(r)
			seg, ok := cache[ck]
			if !ok {
				seg = segOf(r.bench, r.task)
				cache[ck] = seg
			}
			for k, vs := range seg {
				for _, v := range strings.Split(vs, "\n") {
					if v = strings.TrimSpace(v); v == "" {
						continue
					}
					x := kv{k, v}
					if byKV[x] == nil {
						byKV[x] = map[string][]run{}
					}
					byKV[x][l] = append(byKV[x][l], r)
				}
			}
		}
	}
	keys := map[string][]string{}
	for x := range byKV {
		keys[x.k] = append(keys[x.k], x.v)
	}
	names := sortedKeys(keys)
	if len(names) > maxSegmentKeys {
		names = names[:maxSegmentKeys]
	}
	out := []Group{}
	for _, k := range names {
		vals := keys[k]
		slices.Sort(vals)
		if len(vals) < 2 {
			continue // one value splits nothing
		}
		if len(vals) > maxSegmentValues {
			vals = vals[:maxSegmentValues]
		}
		for _, v := range vals {
			g := Group{Key: k, Value: v, Models: map[string]GroupModel{}, Default: def}
			fillGroup(&g, labels, byKV[kv{k, v}])
			g.Small = g.Tasks < MinSegmentTasks
			out = append(out, g)
		}
	}
	return out
}

// costPareto places every model on quality against cost per success.
func costPareto(ms []Model) costexplain.Pareto {
	pts := make([]costexplain.ParetoPoint, 0, len(ms))
	for _, m := range ms {
		pts = append(pts, costexplain.ParetoPoint{ID: m.Label, Label: display(m), Model: m.Model, X: m.CostPerSuccess, Y: m.Quality, DominatedBy: []string{}})
	}
	pts = costexplain.Frontier(pts)
	p := costexplain.Pareto{X: costexplain.XCostPerSuccess, XLabel: "Cost per verified success (USD, failures included)", Y: "verified_success", Points: pts, Frontier: []string{}}
	placed := 0
	for _, pt := range pts {
		if pt.Frontier {
			p.Frontier = append(p.Frontier, pt.ID)
		}
		if pt.Placed {
			placed++
		}
	}
	p.Note = fmt.Sprintf("%d of %d models placed; %d on the frontier. The frontier is read from point estimates: where the quality ranges overlap, the order between two models is not established.", placed, len(pts), len(p.Frontier))
	return p
}

// latencyPareto places every model on quality against median latency.
func latencyPareto(ms []Model) LatencyPareto {
	pts := make([]costexplain.ParetoPoint, 0, len(ms))
	for _, m := range ms {
		pts = append(pts, costexplain.ParetoPoint{ID: m.Label, Label: display(m), Model: m.Model, X: m.LatencyP50MS, Y: m.Quality})
	}
	pts = costexplain.Frontier(pts)
	out := LatencyPareto{Points: []LatencyPoint{}, Frontier: []string{}}
	placed := 0
	for _, pt := range pts {
		lp := LatencyPoint{Label: pt.ID, Model: pt.Model, LatencyMS: pt.X, Quality: pt.Y, Placed: pt.Placed, Frontier: pt.Frontier, Reason: strings.Replace(pt.Reason, "cost unknown", "latency unknown", 1)}
		out.Points = append(out.Points, lp)
		if pt.Frontier {
			out.Frontier = append(out.Frontier, pt.ID)
		}
		if pt.Placed {
			placed++
		}
	}
	out.Note = fmt.Sprintf("%d of %d models placed; %d on the frontier (no other model is both faster and better).", placed, len(pts), len(out.Frontier))
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func round(x float64) float64  { return math.Round(x*1e4) / 1e4 }
func round4(x float64) float64 { return math.Round(x*1e4) / 1e4 }
func round2(x float64) float64 { return math.Round(x*100) / 100 }
func round6(x float64) float64 { return math.Round(x*1e6) / 1e6 }
