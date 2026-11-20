package trends

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// ErrBadQuery wraps every query error, so the API answers 400.
var ErrBadQuery = errors.New("bad trends query")

// minTasksForAlpha is the fewest discordant tasks with which the two-sided
// sign-flip test can return p below Alpha: stats.MinPossibleP(6) is 0.031,
// stats.MinPossibleP(5) is 0.0625.
const minTasksForAlpha = 6

// successConf is the level of the descriptive interval on success.
const successConf = 0.95

// Query selects what a trends response covers. Empty fields take the
// defaults: every suite and agent, verified success, clean, live runs only.
type Query struct {
	Suite     string
	Agent     string
	Metric    string
	Condition string
	Synthetic string
	// HeldOut reports whether a task of an experiment is held out (its
	// manifest's held_out_tasks); such runs never enter a trend, as the
	// public leaderboard never counts them. Nil keeps every run.
	HeldOut func(expID, task string) bool `json:"-"`
}

// Normalize fills the defaults and checks the values.
func (q Query) Normalize() (Query, error) {
	q.Suite, q.Agent = strings.TrimSpace(q.Suite), strings.TrimSpace(q.Agent)
	q.Metric = cmp.Or(strings.TrimSpace(q.Metric), MetricSuccess)
	q.Condition = cmp.Or(strings.TrimSpace(q.Condition), schemas.ConditionClean)
	q.Synthetic = cmp.Or(strings.TrimSpace(q.Synthetic), SyntheticExclude)
	if !slices.Contains(Metrics, q.Metric) {
		return q, fmt.Errorf("%w: metric %q is not one of %s", ErrBadQuery, q.Metric, strings.Join(Metrics, ", "))
	}
	if q.Synthetic != SyntheticExclude && q.Synthetic != SyntheticInclude && q.Synthetic != SyntheticOnly {
		return q, fmt.Errorf("%w: synthetic %q is not exclude, include or only", ErrBadQuery, q.Synthetic)
	}
	return q, nil
}

// Build computes the trends of the indexed runs. Runs of an experiment whose
// newest analysis is INVALID are left out, as the leaderboard leaves them
// out: its instrument failed, so its runs are not evidence. Only runs whose
// outcome scores (resultsindex.RunRow.Success set) enter a point, and runs
// that called real external hosts (not repeatable) stay out, as on the
// leaderboard.
func Build(r resultsindex.Reader, q Query, now time.Time) (*Response, error) {
	q, err := q.Normalize()
	if err != nil {
		return nil, err
	}
	resp := &Response{
		Suite: q.Suite, Agent: q.Agent, Metric: q.Metric, Condition: q.Condition, Synthetic: q.Synthetic, Alpha: Alpha,
		Metrics: slices.Clone(Metrics), Suites: []string{}, Agents: []string{}, Conditions: []string{}, Lineages: []Lineage{},
		Rejected: len(r.Rejected()), IndexedAt: r.Status().UpdatedAt, GeneratedAt: now.UTC(),
	}
	newest := newestAnalyses(r.Analyses(""))
	all := r.Runs(resultsindex.Filter{Synthetic: indexSynthetic(q.Synthetic)})
	var kept []resultsindex.RunRow
	for _, row := range all {
		if a, ok := newest[row.Experiment]; ok && a.Decision == string(schemas.DecisionInvalid) {
			continue
		}
		if q.HeldOut != nil && q.HeldOut(row.Experiment, row.Task) {
			continue
		}
		resp.Suites = appendUnique(resp.Suites, row.Suite)
		resp.Agents = appendUnique(resp.Agents, row.Agent)
		resp.Conditions = appendUnique(resp.Conditions, row.Condition)
		if len(row.Problems) > 0 {
			// Kept in the index for diagnosis, but evidence the verdict would
			// call INVALID (e.g. a slot with two counted runs) is never charted.
			resp.Invalid++
			continue
		}
		if row.Success == nil || row.NotRepeatable || row.Condition != q.Condition || (q.Suite != "" && row.Suite != q.Suite) ||
			(q.Agent != "" && row.Agent != q.Agent) {
			continue
		}
		kept = append(kept, row)
	}
	slices.Sort(resp.Suites)
	slices.Sort(resp.Agents)
	slices.Sort(resp.Conditions)
	resp.Lineages = lineages(kept, newest, q.Metric)
	for _, l := range resp.Lineages {
		resp.Points += len(l.Points)
	}
	return resp, nil
}

// pointRuns is one point under construction.
type pointRuns struct {
	p     Point
	lock  string // suite lock hash: tasks pair only within one lock
	rows  []resultsindex.RunRow
	rates map[string]float64
}

// lineages groups runs into lineages and points and computes every point.
func lineages(rows []resultsindex.RunRow, newest map[string]resultsindex.AnalysisRow, metric string) []Lineage {
	type lineageRuns struct {
		l      Lineage
		points map[string]*pointRuns
	}
	byKey := map[string]*lineageRuns{}
	for _, row := range rows {
		key := row.Agent + "|" + row.Model
		lr, ok := byKey[key]
		if !ok {
			lr = &lineageRuns{l: Lineage{Key: key, Agent: row.Agent, Model: row.Model}, points: map[string]*pointRuns{}}
			byKey[key] = lr
		}
		pk := row.Experiment + "|" + row.ConfigHash
		pr, ok := lr.points[pk]
		if !ok {
			pr = &pointRuns{p: Point{Experiment: row.Experiment, Date: row.ExperimentCreated, Suite: row.Suite,
				Arm: row.Arm, ConfigHash: row.ConfigHash}, lock: row.SuiteLockHash}
			lr.points[pk] = pr
		}
		if !slices.Contains(strings.Split(pr.p.Arm, ","), row.Arm) {
			pr.p.Arm += "," + row.Arm
		}
		pr.rows = append(pr.rows, row)
	}
	out := make([]Lineage, 0, len(byKey))
	for _, lr := range byKey {
		prs := make([]*pointRuns, 0, len(lr.points))
		for _, pr := range lr.points {
			computePoint(pr, newest[pr.p.Experiment], metric)
			prs = append(prs, pr)
		}
		slices.SortFunc(prs, func(a, b *pointRuns) int {
			return cmp.Or(a.p.Date.Compare(b.p.Date), cmp.Compare(a.p.Experiment, b.p.Experiment),
				cmp.Compare(a.p.Arm, b.p.Arm), cmp.Compare(a.p.ConfigHash, b.p.ConfigHash))
		})
		for i, pr := range prs {
			if i == 0 {
				pr.p.Regression = Regression{Status: StatusFirst, Reason: "first point of this lineage"}
			} else {
				pr.p.ConfigChanged = pr.p.ConfigHash != prs[i-1].p.ConfigHash
				pr.p.Regression = compareSameSuite(prs[:i], pr)
			}
			lr.l.Synthetic = lr.l.Synthetic || pr.p.Synthetic
			lr.l.Points = append(lr.l.Points, pr.p)
		}
		lr.l.Regression = lr.l.Points[len(lr.l.Points)-1].Regression
		out = append(out, lr.l)
	}
	slices.SortFunc(out, func(a, b Lineage) int { return cmp.Or(cmp.Compare(a.Agent, b.Agent), cmp.Compare(a.Model, b.Model)) })
	return out
}

// compareSameSuite compares a point with the newest earlier point of the
// same suite (id@version) and suite lock. Task IDs are only paired within
// one locked suite: two suites, two versions of one suite, or a relocked
// suite can reuse an ID such as t01 for different tasks.
func compareSameSuite(earlier []*pointRuns, pr *pointRuns) Regression {
	for j := len(earlier) - 1; j >= 0; j-- {
		if earlier[j].p.Suite == pr.p.Suite && earlier[j].lock == pr.lock {
			reg := Compare(earlier[j].rates, pr.rates)
			reg.Against = earlier[j].p.Experiment
			return reg
		}
	}
	return Regression{Status: StatusCannotTell,
		Reason: fmt.Sprintf("no earlier point of suite %s with the same suite lock in this lineage; tasks of other suites are not paired", pr.p.Suite)}
}

// computePoint fills a point's numbers from its runs and its experiment's
// newest analysis.
func computePoint(pr *pointRuns, a resultsindex.AnalysisRow, metric string) {
	p := &pr.p
	type tally struct{ succ, n int }
	tasks := map[string]*tally{}
	var cost, tokens, wall, falseComp []float64
	succ, costUnknown := 0, false
	for _, row := range pr.rows {
		p.Synthetic = p.Synthetic || row.Synthetic
		t := tasks[row.Task]
		if t == nil {
			t = &tally{}
			tasks[row.Task] = t
		}
		t.n++
		if *row.Success == 1 {
			t.succ++
			succ++
		}
		if row.CostUSD != nil {
			cost = append(cost, *row.CostUSD)
		} else {
			costUnknown = true
		}
		tokens = appendMetric(tokens, row.Metrics, MetricTokens)
		wall = appendMetric(wall, row.Metrics, MetricWallTime)
		falseComp = appendMetric(falseComp, row.Metrics, MetricFalseCompletion)
	}
	pr.rates = make(map[string]float64, len(tasks))
	rates := make([]float64, 0, len(tasks))
	for task, t := range tasks {
		pr.rates[task] = float64(t.succ) / float64(t.n)
	}
	for _, task := range sortedKeys(pr.rates) {
		rates = append(rates, pr.rates[task])
	}
	p.Tasks, p.Runs = len(tasks), len(pr.rows)
	p.Success = ptr(stats.Mean(rates))
	if lo, hi, ok := stats.PairedT(rates, successConf); ok {
		p.SuccessLow, p.SuccessHigh = ptr(math.Max(0, lo)), ptr(math.Min(1, hi))
	}
	if succ > 0 && !costUnknown && len(cost) > 0 {
		p.CostPerSuccess = ptr(sum(cost) / float64(succ))
	}
	p.Tokens, p.WallTime, p.FalseCompletion = median(tokens), median(wall), mean(falseComp)
	p.Value = map[string]*float64{
		MetricSuccess: p.Success, MetricCostPerSuccess: p.CostPerSuccess, MetricTokens: p.Tokens,
		MetricWallTime: p.WallTime, MetricFalseCompletion: p.FalseCompletion,
	}[metric]
	p.Decision, p.Policy = a.Decision, a.Policy
	switch {
	case a.Decision == string(schemas.DecisionRegressed):
		p.Release = ReleaseRegressed
	case a.Policy == string(schemas.PolicyBlock):
		p.Release = ReleaseBlocked
	}
}

// Compare tests newer per-task success rates against older ones on the tasks
// both ran, with the exact sign-flip test. Fewer than 6 common tasks, or
// fewer than 6 tasks whose rate changed, can never reach p < Alpha, so they
// are StatusCannotTell rather than a regression or "no change".
func Compare(older, newer map[string]float64) Regression {
	var d []float64
	for _, task := range sortedKeys(newer) {
		if o, ok := older[task]; ok {
			d = append(d, newer[task]-o)
		}
	}
	reg := Regression{CommonTasks: len(d)}
	if len(d) == 0 {
		reg.Status, reg.Reason = StatusCannotTell, "no common tasks"
		return reg
	}
	res := stats.SignFlip(d)
	reg.P, reg.Delta, reg.Discordant = ptr(res.P), ptr(stats.Mean(d)), res.NNonzero
	switch {
	case len(d) < minTasksForAlpha:
		reg.Status = StatusCannotTell
		reg.Reason = fmt.Sprintf("%d common tasks; the sign-flip test needs at least %d to reach p < %.2f", len(d), minTasksForAlpha, Alpha)
	case res.NNonzero == 0:
		reg.Status = StatusNoChange
		reg.Reason = fmt.Sprintf("no common task changed (%d common tasks)", len(d))
	case stats.MinPossibleP(res.NNonzero) > Alpha:
		reg.Status = StatusCannotTell
		reg.Reason = fmt.Sprintf("only %d of %d common tasks changed; at least %d must change for p < %.2f", res.NNonzero, len(d), minTasksForAlpha, Alpha)
	case res.P < Alpha && *reg.Delta < 0:
		reg.Status = StatusRegressed
		reg.Reason = fmt.Sprintf("p %.3f on %d common tasks (%d changed), success down %.1f points", res.P, len(d), res.NNonzero, -100**reg.Delta)
	case res.P < Alpha && *reg.Delta > 0:
		reg.Status = StatusImproved
		reg.Reason = fmt.Sprintf("p %.3f on %d common tasks (%d changed), success up %.1f points", res.P, len(d), res.NNonzero, 100**reg.Delta)
	default:
		reg.Status = StatusNoChange
		reg.Reason = fmt.Sprintf("p %.3f on %d common tasks (%d changed): no difference detected", res.P, len(d), res.NNonzero)
	}
	return reg
}

// newestAnalyses returns each experiment's newest analysis (created_at, ties
// by the larger id).
func newestAnalyses(rows []resultsindex.AnalysisRow) map[string]resultsindex.AnalysisRow {
	out := map[string]resultsindex.AnalysisRow{}
	for _, a := range rows {
		cur, ok := out[a.Experiment]
		if !ok || a.CreatedAt.After(cur.CreatedAt) || (a.CreatedAt.Equal(cur.CreatedAt) && a.AnalysisID > cur.AnalysisID) {
			out[a.Experiment] = a
		}
	}
	return out
}

// indexSynthetic maps the trends synthetic filter to the index's.
func indexSynthetic(s string) string {
	switch s {
	case SyntheticOnly:
		return resultsindex.SyntheticOnly
	case SyntheticInclude:
		return resultsindex.SyntheticInclude
	}
	return resultsindex.SyntheticExclude
}

// appendMetric appends an observed, finite metric value.
func appendMetric(xs []float64, m map[string]float64, id string) []float64 {
	if v, ok := m[id]; ok && !math.IsNaN(v) && !math.IsInf(v, 0) {
		return append(xs, v)
	}
	return xs
}

// appendUnique appends s when it is not empty and not present.
func appendUnique(xs []string, s string) []string {
	if s == "" || slices.Contains(xs, s) {
		return xs
	}
	return append(xs, s)
}

// sortedKeys returns the keys of m, sorted.
func sortedKeys(m map[string]float64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// median is the median of xs, nil when empty.
func median(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	return ptr(stats.Median(xs))
}

// mean is the mean of xs, nil when empty.
func mean(xs []float64) *float64 {
	if len(xs) == 0 {
		return nil
	}
	return ptr(stats.Mean(xs))
}

// sum adds xs.
func sum(xs []float64) float64 {
	s := 0.0
	for _, x := range xs {
		s += x
	}
	return s
}

// ptr returns a pointer to v.
func ptr(v float64) *float64 { return &v }
