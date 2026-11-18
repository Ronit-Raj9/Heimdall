package migration

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/costexplain"
	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/policy"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/taskhealth"
)

// Per-run metrics the report reads.
const (
	metricFalseCompletion = "false_completion"
	metricRetryAmp        = "retry_amplification"
)

// maxTasksNeeded caps the tasks-needed search.
const maxTasksNeeded = 5000

// Input is what Build reads.
type Input struct {
	Manifest *schemas.Manifest
	Runs     []schemas.SealedRun // as the store's verified index returns them
	Read     costexplain.RunFileReader
	Prices   *evidence.PriceTable
	// Tasks are the suite's tasks by id, for the kind, capability, tag and
	// declared difficulty strata; nil leaves those strata out.
	Tasks map[string]*schemas.Task
	// Policy is the release policy the recommendation follows, with the
	// sha256 of its file; nil gives a recommendation from the statistics
	// alone.
	Policy     *schemas.Policy
	PolicyHash string
	// Synthetic marks synthetic arms (leaderboard.SyntheticArms).
	Synthetic map[string]bool
}

// Build writes the migration report. It is pure apart from reading run
// files through in.Read.
func Build(ctx context.Context, in Input, q Query) (*Report, error) {
	m := in.Manifest
	if m == nil {
		return nil, fmt.Errorf("%w: no manifest", ErrBadQuery)
	}
	q.Condition = cmp.Or(strings.TrimSpace(q.Condition), m.Experiment.Primary.Condition, schemas.ConditionClean)
	if q.Alpha == 0 {
		q.Alpha = leaderboard.DefaultAlpha
	}
	if !(q.Alpha > 0 && q.Alpha < 1) {
		return nil, fmt.Errorf("%w: alpha %v is not in (0, 1)", ErrBadQuery, q.Alpha)
	}
	pairs, err := resolvePairs(m, q)
	if err != nil {
		return nil, err
	}
	g := newGraded(m, in.Runs)
	synthetic := false
	arms := []string{pairs[0][0]}
	for _, p := range pairs {
		if !slices.Contains(arms, p[0]) {
			arms = append(arms, p[0])
		}
		arms = append(arms, p[1])
	}
	for _, a := range arms {
		synthetic = synthetic || in.Synthetic[a]
	}
	rep := &Report{
		ExpID: m.ExpID, Suite: m.SuiteID + "@" + m.SuiteVersion, Condition: q.Condition, Conditions: g.conditions,
		Alpha: q.Alpha, Synthetic: synthetic, From: armRef(m, pairs[0][0]), Pairs: []Pair{}, Models: []ModelSummary{},
		Notes: []string{TunedPromptNote},
	}
	if in.Policy != nil {
		rep.Policy = in.Policy.Name
	}
	for _, p := range pairs {
		f, t := m.Arms[p[0]], m.Arms[p[1]]
		switch {
		case f.Config.ID != t.Config.ID:
			rep.Notes = append(rep.Notes, fmt.Sprintf("Arms %s and %s run different agent configs (%s, %s): the pair compares harnesses as well as models.", p[0], p[1], f.Config.ID, t.Config.ID))
		case f.Config.Model == t.Config.Model:
			rep.Notes = append(rep.Notes, fmt.Sprintf("Arms %s and %s request the same model (%s): the pair compares config versions, not models.", p[0], p[1], f.Config.Model))
		}
	}
	if synthetic {
		rep.Notes = append([]string{"Synthetic: scripted agent, scripted model or smoke experiment; the numbers are not findings and the recommendation is withheld."}, rep.Notes...)
	}
	loaded, _, err := costexplain.LoadArms(ctx, m, in.Runs, in.Read, q.Condition, arms)
	if err != nil {
		return nil, err
	}
	explanations := map[string]*costexplain.Explanation{}
	for _, p := range pairs {
		e := costexplain.ExplainLoaded(m, costexplain.Query{A: p[0], B: p[1], Condition: q.Condition}, loaded[p[0]], loaded[p[1]], in.Prices)
		e.Conditions, e.Synthetic = g.conditions, in.Synthetic[p[0]] || in.Synthetic[p[1]]
		explanations[p[1]] = e
	}
	cl, err := clusterReport(ctx, m, in.Runs, in.Read)
	if err != nil {
		return nil, err
	}
	// Overall verdicts are one Holm family across the pairs.
	overall := make([]Verdict, len(pairs))
	for i, p := range pairs {
		overall[i] = compare(g, p[0], p[1], q.Condition, g.tasks(q.Condition))
	}
	adjust(overall, q.Alpha)
	for i, p := range pairs {
		pair := Pair{From: armRef(m, p[0]), To: armRef(m, p[1]), Overall: overall[i], Cost: explanations[p[1]]}
		pair.Strata = strata(g, p[0], p[1], q.Condition, in.Tasks)
		adjust(pair.Strata, q.Alpha)
		pair.Behaviour = behaviour(g, m, p[0], p[1], explanations[p[1]], cl)
		pair.Flipped = flipped(g, p[0], p[1])
		var pr *schemas.PolicyResult
		if in.Policy != nil {
			pr = evaluatePolicy(in.Policy, in.PolicyHash, m, in.Runs, p[0], p[1])
		}
		pair.Recommendation = recommend(pair, pr, synthetic)
		rep.Pairs = append(rep.Pairs, pair)
	}
	mx, err := leaderboard.Matrix(m, in.Runs, synthetic, leaderboard.MatrixQuery{Condition: q.Condition, Alpha: q.Alpha})
	if err != nil {
		return nil, err
	}
	rep.Pareto, rep.Models = models(mx, m, arms, explanations, q)
	return rep, nil
}

// resolvePairs turns From and To into (old arm, new arm) pairs. A value is
// an arm id, or a model: the arms whose config requests it. Each old arm is
// paired with the new arms of the same agent config.
func resolvePairs(m *schemas.Manifest, q Query) ([][2]string, error) {
	from, err := resolveArms(m, q.From)
	if err != nil {
		return nil, err
	}
	var to []string
	if len(q.To) == 0 {
		for id := range m.Arms {
			if !slices.Contains(from, id) {
				to = append(to, id)
			}
		}
	}
	for _, v := range q.To {
		arms, err := resolveArms(m, v)
		if err != nil {
			return nil, err
		}
		to = append(to, arms...)
	}
	slices.Sort(to)
	to = slices.Compact(to)
	var pairs [][2]string
	for _, f := range from {
		for _, t := range to {
			if t != f && m.Arms[t].Config.ID == m.Arms[f].Config.ID {
				pairs = append(pairs, [2]string{f, t})
			}
		}
	}
	if len(pairs) == 0 {
		// No arm runs the same agent config: compare with every listed arm,
		// and the report says it compares configs, not only models.
		for _, f := range from {
			for _, t := range to {
				if t != f {
					pairs = append(pairs, [2]string{f, t})
				}
			}
		}
	}
	if len(pairs) == 0 {
		return nil, fmt.Errorf("%w: no other arm to compare with %s", ErrBadQuery, q.From)
	}
	return pairs, nil
}

// resolveArms is the arms one From or To value names, sorted.
func resolveArms(m *schemas.Manifest, v string) ([]string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, fmt.Errorf("%w: name the old model (-from) as a model or an arm id", ErrBadQuery)
	}
	if _, ok := m.Arms[v]; ok {
		return []string{v}, nil
	}
	var out, models []string
	for id, a := range m.Arms {
		models = append(models, a.Config.Model)
		if a.Config.Model == v {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		slices.Sort(models)
		return nil, fmt.Errorf("%w: %q is neither an arm nor a model of experiment %s (models: %s)", ErrBadQuery, v, m.ExpID, strings.Join(slices.Compact(models), ", "))
	}
	slices.Sort(out)
	return out, nil
}

func armRef(m *schemas.Manifest, arm string) ArmRef {
	a := m.Arms[arm]
	return ArmRef{Arm: arm, Label: a.Label, Agent: a.Config.ID, Model: a.Config.Model, Provider: a.Config.Gateway.Provider}
}

// graded holds the graded runs by arm and condition.
type graded struct {
	runs       map[string]map[string][]gradedRun // arm -> condition -> runs
	conditions []string
}

type gradedRun struct {
	sr      *schemas.SealedRun
	success bool
}

func newGraded(m *schemas.Manifest, runs []schemas.SealedRun) *graded {
	g := &graded{runs: map[string]map[string][]gradedRun{}, conditions: []string{}}
	for i := range runs {
		sr := &runs[i]
		if _, ok := m.Arms[sr.Slot.Arm]; !ok {
			continue
		}
		success, ok := costexplain.Graded(sr)
		if !ok {
			continue
		}
		if g.runs[sr.Slot.Arm] == nil {
			g.runs[sr.Slot.Arm] = map[string][]gradedRun{}
		}
		g.runs[sr.Slot.Arm][sr.Slot.Condition] = append(g.runs[sr.Slot.Arm][sr.Slot.Condition], gradedRun{sr: sr, success: success})
		if !slices.Contains(g.conditions, sr.Slot.Condition) {
			g.conditions = append(g.conditions, sr.Slot.Condition)
		}
	}
	slices.SortFunc(g.conditions, func(x, y string) int {
		return cmp.Or(trueFirst(x == schemas.ConditionClean, y == schemas.ConditionClean), cmp.Compare(x, y))
	})
	return g
}

func trueFirst(x, y bool) int {
	switch {
	case x == y:
		return 0
	case x:
		return -1
	default:
		return 1
	}
}

// rates is each task's verified success rate for one arm and condition.
func (g *graded) rates(arm, condition string) map[string]float64 {
	type tally struct{ s, n int }
	t := map[string]tally{}
	for _, r := range g.runs[arm][condition] {
		x := t[r.sr.Slot.Task]
		x.n++
		if r.success {
			x.s++
		}
		t[r.sr.Slot.Task] = x
	}
	out := map[string]float64{}
	for task, x := range t {
		out[task] = float64(x.s) / float64(x.n)
	}
	return out
}

// tasks is every task with a graded run under condition, sorted.
func (g *graded) tasks(condition string) []string {
	var out []string
	for _, byCond := range g.runs {
		for _, r := range byCond[condition] {
			if !slices.Contains(out, r.sr.Slot.Task) {
				out = append(out, r.sr.Slot.Task)
			}
		}
	}
	slices.Sort(out)
	return out
}

// compare tests the new arm against the old on the listed tasks of one
// condition (those both arms ran). P is raw; adjust sets the rest.
func compare(g *graded, from, to, condition string, tasks []string) Verdict {
	rf, rt := g.rates(from, condition), g.rates(to, condition)
	v := Verdict{Condition: condition}
	var d []float64
	for _, task := range tasks {
		a, okA := rf[task]
		b, okB := rt[task]
		if !okA || !okB {
			continue
		}
		d = append(d, b-a)
		switch {
		case b > a:
			v.Better++
		case b < a:
			v.Worse++
		}
	}
	v.Tasks = len(d)
	if len(d) == 0 {
		return v
	}
	res := stats.SignFlip(d)
	v.MeanDiff = stats.Mean(d)
	p := res.P
	v.P = &p
	if len(d) >= 2 {
		v.sd = stats.SD(d)
	}
	return v
}

// adjust Holm-adjusts the verdicts that have a test together and writes
// each one's result, reason and task counts.
func adjust(vs []Verdict, alpha float64) {
	var idx []int
	var raw []float64
	for i := range vs {
		if vs[i].P != nil {
			idx = append(idx, i)
			raw = append(raw, *vs[i].P)
		}
	}
	adj := holm(raw)
	family := len(idx)
	for i := range vs {
		vs[i].Family = family
		vs[i].Result = ResultCannotTell
		vs[i].MinTasks = minTasks(alpha, max(family, 1))
	}
	for j, i := range idx {
		v := &vs[i]
		a := adj[j]
		v.PAdjusted = &a
		n := v.Better + v.Worse
		switch {
		case n > 0 && a < alpha && v.MeanDiff > 0:
			v.Result = ResultBetter
			v.Reason = fmt.Sprintf("new model better: p %s after Holm across %d, on %d tasks (%d better, %d worse)", formatP(a), family, v.Tasks, v.Better, v.Worse)
		case n > 0 && a < alpha && v.MeanDiff < 0:
			v.Result = ResultWorse
			v.Reason = fmt.Sprintf("new model worse: p %s after Holm across %d, on %d tasks (%d better, %d worse)", formatP(a), family, v.Tasks, v.Better, v.Worse)
		case n == 0:
			v.Reason = fmt.Sprintf("cannot tell: the same success on all %d tasks", v.Tasks)
		case stats.MinPossibleP(n) > alpha:
			v.Reason = fmt.Sprintf("cannot tell: %d differing tasks of %d; the test needs at least %d differing tasks to reach alpha %s", n, v.Tasks, minNonzero(alpha, 1), fmtAlpha(alpha))
		default:
			v.Reason = fmt.Sprintf("cannot tell: p %s after Holm across %d, on %d tasks (%d better, %d worse)", formatP(a), family, v.Tasks, v.Better, v.Worse)
		}
		if v.Result == ResultCannotTell {
			v.TasksNeeded = estimateTasks(vs[i], alpha/float64(max(family, 1)))
		} else {
			v.TasksNeeded = 0
		}
	}
	for i := range vs {
		if vs[i].P == nil {
			vs[i].Reason = "cannot tell: no task both models ran"
			vs[i].TasksNeeded = 0
		}
	}
}

// estimateTasks is the paired t estimate (the verdict's MDE rule) of the
// tasks needed to detect the observed mean difference with 80% power at
// alpha (Bonferroni, the worst case of Holm), with the observed spread of
// the per-task differences; never fewer than the differing tasks a test
// needs to decide at all. Zero when the difference is zero.
func estimateTasks(v Verdict, alpha float64) int {
	if v.MeanDiff == 0 || v.Tasks < 2 {
		return 0
	}
	n := stats.TasksNeeded(v.MeanDiff, v.sd, alpha, Power, v.Tasks+1, maxTasksNeeded)
	if n == 0 {
		return 0
	}
	return max(n, minTasks(alpha, 1))
}

// minTasks is the fewest differing tasks with which the smallest possible
// sign-flip p, times the Holm family size, falls below alpha.
func minTasks(alpha float64, family int) int {
	return minNonzero(alpha, family)
}

func minNonzero(alpha float64, family int) int {
	for n := 1; n <= 64; n++ {
		if stats.MinPossibleP(n)*float64(family) < alpha {
			return n
		}
	}
	return 0
}

// strata compares the pair within each stratum: every condition, and on the
// overall condition every task kind, capability, tag, declared difficulty
// and observed difficulty (the pooled success of the task over every arm of
// the experiment on that condition, which a label swap within a task does
// not change, so stratifying on it keeps the test valid).
func strata(g *graded, from, to, condition string, tasks map[string]*schemas.Task) []Verdict {
	out := []Verdict{}
	add := func(dim, value, cond string, ts []string) {
		if len(ts) == 0 {
			return
		}
		v := compare(g, from, to, cond, ts)
		if v.Tasks == 0 {
			return
		}
		v.Dimension, v.Value = dim, value
		out = append(out, v)
	}
	for _, c := range g.conditions {
		add(DimCondition, c, c, g.tasks(c))
	}
	all := g.tasks(condition)
	group := func(dim string, values func(*schemas.Task) []string) {
		by := map[string][]string{}
		for _, id := range all {
			t := tasks[id]
			if t == nil {
				continue
			}
			for _, v := range values(t) {
				if v != "" {
					by[v] = append(by[v], id)
				}
			}
		}
		for _, v := range sortedKeys(by) {
			add(dim, v, condition, by[v])
		}
	}
	if tasks != nil {
		group(DimTaskKind, func(t *schemas.Task) []string { return []string{string(t.Kind)} })
		group(DimCapability, func(t *schemas.Task) []string { return t.Capabilities })
		group(DimTag, func(t *schemas.Task) []string { return t.Tags })
		group(DimDifficultyDeclared, func(t *schemas.Task) []string { return []string{t.Difficulty} })
	}
	observed := map[string][]string{}
	for _, id := range all {
		succ, n, configs := 0, 0, 0
		for _, byCond := range g.runs {
			runs := 0
			for _, r := range byCond[condition] {
				if r.sr.Slot.Task != id {
					continue
				}
				runs++
				n++
				if r.success {
					succ++
				}
			}
			if runs > 0 {
				configs++
			}
		}
		if n == 0 {
			continue
		}
		label := taskhealth.DifficultyLabel(float64(succ)/float64(n), n, configs, taskhealth.Options{})
		observed[label] = append(observed[label], id)
	}
	for _, v := range sortedKeys(observed) {
		add(DimDifficultyObserved, v, condition, observed[v])
	}
	return out
}

// behaviour collects what changed besides success.
func behaviour(g *graded, m *schemas.Manifest, from, to string, e *costexplain.Explanation, cl *clusters.Report) Behaviour {
	b := Behaviour{NewClusters: []ClusterRef{}, FixedClusters: []ClusterRef{}, Gates: []GateChange{}, Conditions: []ConditionDiff{}}
	fromHash, toHash := m.Arms[from].ConfigHash, m.Arms[to].ConfigHash
	if cl != nil {
		for _, c := range cl.Clusters {
			inFrom, inTo := slices.Contains(c.ConfigHashes, fromHash), slices.Contains(c.ConfigHashes, toHash)
			ref := ClusterRef{Signature: c.Signature, Title: c.Title, Count: c.Count, Tasks: c.Tasks, Examples: c.Examples}
			switch {
			case inTo && !inFrom:
				b.NewClusters = append(b.NewClusters, ref)
			case inFrom && !inTo:
				b.FixedClusters = append(b.FixedClusters, ref)
			}
		}
	}
	gates := map[string]*GateChange{}
	refusals := [2]int{}
	runs := [2]int{}
	for side, arm := range []string{from, to} {
		for _, c := range g.conditions {
			for _, r := range g.runs[arm][c] {
				runs[side]++
				if r.sr.Run.Outcome == schemas.OutcomeRefusal {
					refusals[side]++
				}
				for _, gr := range r.sr.Gates {
					if !gr.Fired {
						continue
					}
					gc := gates[gr.Gate]
					if gc == nil {
						gc = &GateChange{Gate: gr.Gate, Effect: string(gr.Effect)}
						gates[gr.Gate] = gc
					}
					if side == 0 {
						gc.From++
					} else {
						gc.To++
					}
				}
			}
		}
	}
	for _, id := range sortedKeys(gates) {
		gc := gates[id]
		switch {
		case gc.From == 0:
			gc.Status = "new"
		case gc.To == 0:
			gc.Status = "fixed"
		default:
			gc.Status = "both"
		}
		b.Gates = append(b.Gates, *gc)
	}
	b.FalseCompletion = Change{From: rate(g, from, "", metricFalseCompletion), To: rate(g, to, "", metricFalseCompletion)}
	if e != nil {
		obs := func(x float64) schemas.Value { return schemas.Observed(x, schemas.EvidenceGateway) }
		b.Rejected = Change{From: obs(e.A.RejectedPerRun), To: obs(e.B.RejectedPerRun)}
		b.Refusals = Change{From: obs(e.A.RefusalsPerRun + perRun(refusals[0], runs[0])), To: obs(e.B.RefusalsPerRun + perRun(refusals[1], runs[1]))}
		b.Retries = Change{From: obs(e.A.RetriesPerRun), To: obs(e.B.RetriesPerRun)}
		b.Fallbacks = Change{From: obs(e.A.FallbacksPerRun), To: obs(e.B.FallbacksPerRun)}
		b.LatencyMS = Change{From: e.A.LatencyMSPerTurn, To: e.B.LatencyMSPerTurn}
	}
	for _, c := range g.conditions {
		b.Conditions = append(b.Conditions, ConditionDiff{
			Condition: c, FromRuns: len(g.runs[from][c]), ToRuns: len(g.runs[to][c]),
			FromSuccess: meanRate(g.rates(from, c)), ToSuccess: meanRate(g.rates(to, c)),
			FalseCompletion: Change{From: rate(g, from, c, metricFalseCompletion), To: rate(g, to, c, metricFalseCompletion)},
			Retries:         Change{From: rate(g, from, c, metricRetryAmp), To: rate(g, to, c, metricRetryAmp)},
		})
	}
	return b
}

func perRun(n, runs int) float64 {
	if runs == 0 {
		return 0
	}
	return float64(n) / float64(runs)
}

// rate is the mean of a per-run metric over an arm's graded runs on one
// condition (every condition when empty). A not-applicable false
// completion counts as 0 (a passing run), as the board reads it; unknowns
// are left out and never read as 0.
func rate(g *graded, arm, condition, metric string) schemas.Value {
	var xs []float64
	unknown := 0
	for _, c := range g.conditions {
		if condition != "" && c != condition {
			continue
		}
		for _, r := range g.runs[arm][c] {
			v, found := r.sr.Metrics[metric]
			if x, ok := v.Float(); ok {
				xs = append(xs, x)
			} else if found && v.State == schemas.ValueNotApplicable && metric == metricFalseCompletion {
				xs = append(xs, 0)
			} else if found {
				unknown++
			}
		}
	}
	if len(xs) == 0 {
		if unknown > 0 {
			return schemas.Unknown(fmt.Sprintf("unknown in all %d runs", unknown), schemas.EvidenceGateway)
		}
		return schemas.Unknown("not recorded", schemas.EvidenceGateway)
	}
	v := schemas.Observed(stats.Mean(xs), schemas.EvidenceGateway)
	if unknown > 0 {
		v.Reason = fmt.Sprintf("%d of %d runs unknown", unknown, len(xs)+unknown)
	}
	return v
}

func meanRate(rates map[string]float64) schemas.Value {
	if len(rates) == 0 {
		return schemas.Unknown("no graded run", schemas.EvidenceOutcome)
	}
	keys := make([]string, 0, len(rates))
	for k := range rates {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	xs := make([]float64, 0, len(keys))
	for _, k := range keys {
		xs = append(xs, rates[k])
	}
	return schemas.Observed(stats.Mean(xs), schemas.EvidenceOutcome)
}

// flipped lists, per condition, the tasks whose success rate differs
// between the models, regressions first, with every run of both arms.
func flipped(g *graded, from, to string) []FlippedTask {
	out := []FlippedTask{}
	for _, c := range g.conditions {
		rf, rt := g.rates(from, c), g.rates(to, c)
		for _, task := range sortedKeysF(rf) {
			b, ok := rt[task]
			a := rf[task]
			if !ok || a == b {
				continue
			}
			ft := FlippedTask{Task: task, Condition: c, Kind: "fixed", FromRate: a, ToRate: b, Runs: []RunRef{}}
			if b < a {
				ft.Kind = "regressed"
			}
			for _, arm := range []string{from, to} {
				for _, r := range g.runs[arm][c] {
					if r.sr.Slot.Task == task {
						ft.Runs = append(ft.Runs, RunRef{RunID: r.sr.Run.RunID, PairID: r.sr.Slot.PairID, Arm: arm, Trial: r.sr.Slot.Trial, Outcome: string(r.sr.Run.Outcome)})
					}
				}
			}
			slices.SortStableFunc(ft.Runs, func(x, y RunRef) int { return cmp.Or(cmp.Compare(x.Trial, y.Trial), cmp.Compare(x.Arm, y.Arm)) })
			out = append(out, ft)
		}
	}
	slices.SortStableFunc(out, func(x, y FlippedTask) int {
		return cmp.Or(trueFirst(x.Kind == "regressed", y.Kind == "regressed"), cmp.Compare(math.Abs(y.ToRate-y.FromRate), math.Abs(x.ToRate-x.FromRate)))
	})
	return out
}

// evaluatePolicy runs the release policy on the pair with the old model as
// baseline arm A and the new one as candidate B, exactly as `check` would on
// a two-arm experiment of those runs.
func evaluatePolicy(p *schemas.Policy, hash string, m *schemas.Manifest, runs []schemas.SealedRun, from, to string) *schemas.PolicyResult {
	var pairRuns []schemas.SealedRun
	for _, r := range runs {
		switch r.Slot.Arm {
		case from:
			r.Slot.Arm = policy.BaselineArm
		case to:
			r.Slot.Arm = policy.DefaultArm
		default:
			continue
		}
		pairRuns = append(pairRuns, r)
	}
	mc := *m
	mc.PolicyHash = ""
	return policy.Evaluate(p, hash, &mc, nil, pairRuns)
}

// recommend turns the statistics and the policy into the plain answer. It
// is never stronger than the statistics: migrate needs a significant gain
// overall, no significant loss in any stratum and a policy that does not
// block; do not migrate needs a significant loss or a failed blocking
// check; everything else is not enough evidence.
func recommend(p Pair, pr *schemas.PolicyResult, synthetic bool) Recommendation {
	r := Recommendation{Policy: pr, Caveats: []string{TunedPromptNote}}
	var worseStrata []string
	for _, s := range p.Strata {
		if s.Result == ResultWorse {
			worseStrata = append(worseStrata, fmt.Sprintf("%s %s (%s)", s.Dimension, s.Value, s.Reason))
		}
	}
	var failed, unknown []string
	if pr != nil {
		for _, c := range pr.Checks {
			switch {
			case c.Blocking && c.Status == schemas.PolicyFail:
				failed = append(failed, c.ID+": "+c.Reason)
			case c.Status != schemas.PolicyPass:
				unknown = append(unknown, c.ID+" "+string(c.Status))
			}
		}
	}
	switch {
	case synthetic:
		r.Action, r.Reason = ActionNotEnough, "synthetic experiment: its numbers are not findings"
	case p.Overall.Result == ResultWorse:
		r.Action, r.Reason = ActionDoNotMigrate, "overall on "+p.Overall.Condition+": "+p.Overall.Reason
	case len(worseStrata) > 0:
		r.Action, r.Reason = ActionDoNotMigrate, "regresses on "+strings.Join(worseStrata, "; ")
	case len(failed) > 0:
		r.Action, r.Reason = ActionDoNotMigrate, fmt.Sprintf("release policy %s blocks: %s", pr.PolicyName, strings.Join(failed, "; "))
	case p.Overall.Result == ResultBetter && (pr == nil || pr.Decision != schemas.PolicyBlock):
		r.Action, r.Reason = ActionMigrate, "overall on "+p.Overall.Condition+": "+p.Overall.Reason
		if pr == nil {
			r.Caveats = append(r.Caveats, "no release policy was evaluated")
		}
	default:
		r.Action = ActionNotEnough
		r.Reason = "overall on " + p.Overall.Condition + ": " + p.Overall.Reason
		if p.Overall.TasksNeeded > 0 {
			r.Reason += fmt.Sprintf("; about %d tasks would detect a difference of this size with %d%% power", p.Overall.TasksNeeded, int(Power*100))
		} else if p.Overall.MinTasks > p.Overall.Better+p.Overall.Worse {
			r.Reason += fmt.Sprintf("; the test needs at least %d differing tasks to decide at all", p.Overall.MinTasks)
		}
		if pr != nil && pr.Decision == schemas.PolicyBlock {
			r.Reason += fmt.Sprintf("; release policy %s blocks on unknowns (%s)", pr.PolicyName, strings.Join(unknown, ", "))
		}
	}
	if pr != nil && pr.Decision == schemas.PolicyWarn && r.Action != ActionDoNotMigrate {
		r.Caveats = append(r.Caveats, fmt.Sprintf("release policy %s warns: %s", pr.PolicyName, strings.Join(unknown, ", ")))
	}
	for _, s := range p.Strata {
		if s.Result == ResultBetter && r.Action != ActionMigrate {
			r.Caveats = append(r.Caveats, fmt.Sprintf("better on %s %s (%s)", s.Dimension, s.Value, s.Reason))
		}
	}
	return r
}

// models is the per-arm summary and the Pareto plane from the matrix rows
// of the overall condition.
func models(mx *schemas.ModelMatrix, m *schemas.Manifest, arms []string, ex map[string]*costexplain.Explanation, q Query) (*costexplain.Pareto, []ModelSummary) {
	rows := []schemas.MatrixRow{}
	for _, r := range mx.Rows {
		if slices.Contains(arms, r.Arm) {
			rows = append(rows, r)
		}
	}
	sub := *mx
	sub.Rows = rows
	pareto, err := costexplain.ParetoFromMatrix(&sub, costexplain.XCostPerSuccess)
	if err != nil {
		pareto = nil
	}
	position := map[string]string{}
	if pareto != nil {
		for _, p := range pareto.Points {
			switch {
			case !p.Placed:
				position[p.ID] = "not placed: " + p.Reason
			case p.Frontier:
				position[p.ID] = "frontier"
			default:
				position[p.ID] = "dominated by " + strings.Join(p.DominatedBy, ", ")
			}
		}
	}
	latency := map[string]schemas.Value{}
	for _, e := range ex {
		latency[e.A.Arm], latency[e.B.Arm] = e.A.LatencyMSPerTurn, e.B.LatencyMSPerTurn
	}
	out := []ModelSummary{}
	for _, arm := range arms {
		s := ModelSummary{ArmRef: armRef(m, arm), Pareto: position[arm], LatencyMS: latency[arm]}
		i := slices.IndexFunc(rows, func(r schemas.MatrixRow) bool { return r.Arm == arm })
		if i < 0 {
			none := schemas.Unknown("no graded run on "+q.Condition, schemas.EvidenceOutcome)
			s.Success, s.Reliability, s.CostPerSuccess, s.CostPerRun, s.Tokens, s.WallTimeS, s.Steps = none, none, none, none, none, none, none
			out = append(out, s)
			continue
		}
		e := rows[i].LeaderboardEntry
		s.Success, s.Reliability, s.K = e.Success, e.Reliability, e.Trials
		s.CostPerSuccess, s.CostPerRun = e.Metrics[leaderboard.MetricCostPerSuccess], e.Metrics[leaderboard.MetricCostUSD]
		s.Tokens, s.WallTimeS, s.Steps = e.Metrics[leaderboard.MetricTokens], e.Metrics[leaderboard.MetricWallTime], e.Metrics[leaderboard.MetricSteps]
		if s.LatencyMS.State == "" {
			s.LatencyMS = schemas.Unknown("not read", schemas.EvidenceGateway)
		}
		out = append(out, s)
	}
	return pareto, out
}

// clusterReport clusters the experiment's failures, every run kept
// (synthetic included: the report labels synthetic itself).
func clusterReport(ctx context.Context, m *schemas.Manifest, runs []schemas.SealedRun, read costexplain.RunFileReader) (*clusters.Report, error) {
	src := &oneExperiment{m: m, runs: runs, read: read, byID: map[string]*schemas.SealedRun{}}
	for i := range runs {
		src.byID[runs[i].Run.RunID] = &runs[i]
	}
	rep, err := clusters.Build(ctx, src, clusters.Query{Experiments: []string{m.ExpID}, Synthetic: clusters.SyntheticInclude, Min: 1})
	if err != nil {
		return nil, fmt.Errorf("failure clusters: %w", err)
	}
	return rep, nil
}

// oneExperiment is a clusters.Source over one experiment's sealed runs.
type oneExperiment struct {
	m    *schemas.Manifest
	runs []schemas.SealedRun
	read costexplain.RunFileReader
	byID map[string]*schemas.SealedRun
}

func (s *oneExperiment) Experiments() []string { return []string{s.m.ExpID} }

func (s *oneExperiment) Manifest(expID string) (*schemas.Manifest, bool) {
	return s.m, expID == s.m.ExpID
}

func (s *oneExperiment) VerifiedRuns(_ context.Context, expID string) ([]schemas.SealedRun, error) {
	if expID != s.m.ExpID {
		return nil, nil
	}
	var out []schemas.SealedRun
	for _, r := range s.runs {
		if r.Slot.Counted {
			out = append(out, r)
		}
	}
	return out, nil
}

func (s *oneExperiment) RunEvidence(runID, name string) ([]byte, error) {
	sr, ok := s.byID[runID]
	if !ok || s.read == nil {
		return nil, fs.ErrNotExist
	}
	data, found, err := s.read(sr, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fs.ErrNotExist
	}
	return data, nil
}

// holm is Holm's step-down adjustment in input order (leaderboard/rank.go).
func holm(p []float64) []float64 {
	m := len(p)
	order := make([]int, m)
	clean := make([]float64, m)
	for i, v := range p {
		order[i], clean[i] = i, v
		if math.IsNaN(v) {
			clean[i] = 1
		}
	}
	slices.SortStableFunc(order, func(a, b int) int { return cmp.Compare(clean[a], clean[b]) })
	out := make([]float64, m)
	running := 0.0
	for rank, i := range order {
		running = math.Max(running, math.Min(1, float64(m-rank)*clean[i]))
		out[i] = running
	}
	return out
}

func formatP(p float64) string {
	if p < 0.1 {
		return strconv.FormatFloat(p, 'g', 2, 64)
	}
	return strconv.FormatFloat(p, 'f', 2, 64)
}

func fmtAlpha(a float64) string { return strconv.FormatFloat(a, 'g', -1, 64) }

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func sortedKeysF(m map[string]float64) []string { return sortedKeys(m) }
