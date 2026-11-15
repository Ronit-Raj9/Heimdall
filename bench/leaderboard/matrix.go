package leaderboard

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
)

// MatrixQuery selects one model comparison matrix (docs/model-matrix.md).
// Zero fields take the defaults: the experiment's primary condition, grade
// source original, each row's default k and alpha 0.05.
type MatrixQuery struct {
	Condition   string
	GradeSource string // the source the sealed runs were read with; reported only
	K           int
	Alpha       float64
}

// RunFileReader reads one file of a sealed run's folder. A missing file
// returns found false and no error.
type RunFileReader func(sr *schemas.SealedRun, name string) (data []byte, found bool, err error)

// Matrix compares every arm of one experiment on one condition, from the
// experiment's sealed runs as the store's SealedRuns returns them (each one
// checked against its seal). Each row carries the same numbers as a
// leaderboard entry, computed by the same code (newEntry): verified success
// with its interval, pass^k, cost per success, tokens, wall time and false
// completion. Every pair of rows is compared by the exact sign-flip test on
// the per-task success rates of the tasks both ran, and the p-values of all
// pairs are Holm-adjusted together, so the grid makes no more claims than
// its family allows. Tiers are read off the leader's pairs: an arm is tier 2
// only when its adjusted p against the leader is below alpha and the leader
// is ahead. When even the most favourable split of the largest task set
// cannot reach alpha after the adjustment, Separable is false and Note says
// so in plain words.
//
// A run enters when its outcome scores (intention to treat, as on the
// board); a run with a seal problem, a duplicate counted run or an outcome
// that does not score is counted in Excluded by reason. synthetic marks a
// matrix whose numbers are not findings (SyntheticArms).
func Matrix(m *schemas.Manifest, runs []schemas.SealedRun, synthetic bool, q MatrixQuery) (*schemas.ModelMatrix, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: no manifest", ErrBadQuery)
	}
	q.Condition = cmp.Or(strings.TrimSpace(q.Condition), m.Experiment.Primary.Condition, schemas.ConditionClean)
	q.GradeSource = cmp.Or(strings.TrimSpace(q.GradeSource), GradeSourceOriginal)
	if q.Alpha == 0 {
		q.Alpha = DefaultAlpha
	}
	switch {
	case !validToken(q.Condition):
		return nil, fmt.Errorf("%w: condition %q is not a condition id", ErrBadQuery, q.Condition)
	case q.K < 0 || q.K == 1:
		return nil, fmt.Errorf("%w: k %d is not 0 (each row's default) or 2 or more", ErrBadQuery, q.K)
	case !(q.Alpha > 0 && q.Alpha < 1):
		return nil, fmt.Errorf("%w: alpha %v is not in (0, 1)", ErrBadQuery, q.Alpha)
	}
	exp := &expInfo{id: m.ExpID, m: m, suite: m.SuiteID + "@" + m.SuiteVersion, trials: slotTrials(m.Slots)}
	byArm, excluded, conditions := matrixRuns(exp, runs, q.Condition)
	out := &schemas.ModelMatrix{
		ExpID: m.ExpID, Suite: exp.suite, Condition: q.Condition, Conditions: conditions, GradeSource: q.GradeSource,
		Alpha: q.Alpha, Synthetic: synthetic, Rows: []schemas.MatrixRow{}, Pairs: []schemas.MatrixPair{}, Excluded: excluded,
	}
	arms := make([]string, 0, len(byArm))
	for arm := range byArm {
		arms = append(arms, arm)
	}
	slices.Sort(arms)
	calcs := make([]*entryCalc, 0, len(arms))
	armOf := map[*entryCalc]string{}
	for _, arm := range arms {
		c := newEntry(byArm[arm][0].rec.hash, byArm[arm], byArm[arm], q.K)
		c.e.Synthetic = synthetic
		calcs = append(calcs, c)
		armOf[c] = arm
	}
	slices.SortStableFunc(calcs, compareEntries)
	out.Tasks = len(commonTasks(calcs))
	out.Pairs = matrixPairs(calcs, armOf, q.Alpha)
	matrixTiers(calcs, armOf, out.Pairs)
	for i, c := range calcs {
		c.e.Rank = i + 1
		arm := armOf[c]
		out.Rows = append(out.Rows, schemas.MatrixRow{Arm: arm, Provider: m.Arms[arm].Config.Gateway.Provider, LeaderboardEntry: c.e})
	}
	out.Separable, out.Note = separability(calcs, out.Pairs, q.Alpha)
	return out, nil
}

// SyntheticArms reports whether an experiment's runs are synthetic by the
// board's rules (classify): a smoke experiment, an arm whose config is a
// scripted control agent, or an arm with any sealed run whose proxy ledger
// shows the scripted model. The result maps arm id to true for each
// synthetic arm; read nil skips the ledger check.
func SyntheticArms(ctx context.Context, m *schemas.Manifest, runs []schemas.SealedRun, read RunFileReader) (map[string]bool, error) {
	out := map[string]bool{}
	if m == nil {
		return out, nil
	}
	for id, arm := range m.Arms {
		out[id] = m.Experiment.Smoke || arm.Config.Adapter == adapterScripted || arm.Config.Scripted != nil
	}
	if m.Compatibility != nil {
		out["compat"] = m.Experiment.Smoke || m.Compatibility.Config.Adapter == adapterScripted || m.Compatibility.Config.Scripted != nil
	}
	if read == nil {
		return out, nil
	}
	for i := range runs {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("synthetic arms: %w", err)
		}
		sr := &runs[i]
		if out[sr.Slot.Arm] || len(sr.Problems) > 0 {
			continue
		}
		data, found, err := read(sr, fileLedger)
		if err != nil {
			return nil, fmt.Errorf("synthetic arms: run %s: %w", sr.Run.RunID, err)
		}
		if !found {
			continue
		}
		scripted, err := scriptedLedger(data)
		if err != nil {
			return nil, fmt.Errorf("synthetic arms: run %s: %w", sr.Run.RunID, err)
		}
		out[sr.Slot.Arm] = out[sr.Slot.Arm] || scripted
	}
	return out, nil
}

// matrixRuns turns sealed runs into graded runs by arm for one condition,
// the runs left out by reason, and the conditions with a graded run, clean
// first. Success is read exactly as grade does.
func matrixRuns(exp *expInfo, runs []schemas.SealedRun, condition string) (map[string][]gradedRun, []schemas.LeaderboardExclusion, []string) {
	byArm := map[string][]gradedRun{}
	reasons := map[string]int{}
	seen := map[string]bool{}
	conditions := []string{}
	for i := range runs {
		sr := &runs[i]
		rec := &record{exp: exp, slot: sr.Slot, run: sr.Run, sealed: true, metrics: sr.Metrics, counted: 1, source: sr.GradeSource}
		if a, ok := exp.m.Arms[sr.Slot.Arm]; ok {
			rec.arm, rec.hash = &a, a.ConfigHash
		}
		reason := ""
		_, scores := sr.Run.Outcome.Score()
		switch {
		case !sr.Slot.Counted || rec.arm == nil:
			continue // compatibility runs never enter a comparison
		case len(sr.Problems) > 0 && strings.Contains(strings.Join(sr.Problems, " "), "counted runs"):
			reason = ReasonDuplicateSlot
		case len(sr.Problems) > 0:
			reason = ReasonSealMismatch
		case !scores && sr.Run.Outcome == "":
			reason = reasonNoOutcome
		case !scores:
			reason = string(sr.Run.Outcome)
		}
		if reason != "" {
			if sr.Slot.Condition == condition {
				reasons[reason]++
			}
			continue
		}
		if !seen[sr.Slot.Condition] {
			seen[sr.Slot.Condition] = true
			conditions = append(conditions, sr.Slot.Condition)
		}
		if sr.Slot.Condition != condition {
			continue
		}
		metrics := sr.Metrics
		if metrics == nil {
			metrics = map[string]schemas.Value{}
		}
		y, _ := sr.Run.Outcome.Score()
		if v, ok := metrics[metricVerifiedSuccess].Float(); ok {
			y = 0
			if v >= 0.5 {
				y = 1
			}
		}
		byArm[sr.Slot.Arm] = append(byArm[sr.Slot.Arm], gradedRun{rec: rec, success: y, metrics: metrics})
	}
	slices.SortFunc(conditions, func(x, y string) int {
		return cmp.Or(compareTrueFirst(x == schemas.ConditionClean, y == schemas.ConditionClean), cmp.Compare(x, y))
	})
	excluded := make([]schemas.LeaderboardExclusion, 0, len(reasons))
	for reason, n := range reasons {
		excluded = append(excluded, schemas.LeaderboardExclusion{Reason: reason, Runs: n, Experiments: []string{exp.id}})
	}
	slices.SortFunc(excluded, func(x, y schemas.LeaderboardExclusion) int { return cmp.Compare(x.Reason, y.Reason) })
	return byArm, excluded, conditions
}

// matrixPairs compares every pair of rows in rank order on their common
// tasks and Holm-adjusts the p-values of all pairs that have common tasks
// together.
func matrixPairs(calcs []*entryCalc, armOf map[*entryCalc]string, alpha float64) []schemas.MatrixPair {
	type test struct {
		i   int
		res stats.SignFlipResult
	}
	pairs := []schemas.MatrixPair{}
	var tests []test
	for i, x := range calcs {
		for _, y := range calcs[i+1:] {
			var d []float64
			for _, task := range sortedKeys(x.rates) {
				if r, ok := y.rates[task]; ok {
					d = append(d, x.rates[task]-r)
				}
			}
			p := schemas.MatrixPair{ArmX: armOf[x], ArmY: armOf[y], Tasks: len(d)}
			if len(d) == 0 {
				p.Reason = "cannot compare: no tasks in common"
				pairs = append(pairs, p)
				continue
			}
			res := stats.SignFlip(d)
			p.NNonzero, p.MeanDiff = res.NNonzero, stats.Mean(d)
			raw := res.P
			p.P = &raw
			tests = append(tests, test{i: len(pairs), res: res})
			pairs = append(pairs, p)
		}
	}
	raw := make([]float64, len(tests))
	for i, t := range tests {
		raw[i] = t.res.P
	}
	adjusted := holm(raw)
	for i, t := range tests {
		p := &pairs[t.i]
		adj := adjusted[i]
		p.PAdjusted = &adj
		p.Reason, p.Significant = pairReason(p, t.res, adj, alpha, len(tests))
	}
	return pairs
}

// pairReason says in plain words what one pair's test shows. m is the number
// of pairs in the Holm family.
func pairReason(p *schemas.MatrixPair, res stats.SignFlipResult, adj, alpha float64, m int) (string, bool) {
	n := p.Tasks
	switch {
	case res.NNonzero == 0:
		return fmt.Sprintf("cannot separate: same success on all %d common tasks", n), false
	case stats.MinPossibleP(res.NNonzero) > alpha && res.NNonzero == n:
		return fmt.Sprintf("cannot separate: too few common tasks (%d)", n), false
	case stats.MinPossibleP(res.NNonzero) > alpha:
		return fmt.Sprintf("cannot separate: too few differing tasks (%d of %d common)", res.NNonzero, n), false
	case adj >= alpha:
		return fmt.Sprintf("cannot separate: p %s after Holm across %d pairs, on %d common tasks", formatP(adj), m, n), false
	case p.MeanDiff > 0:
		return fmt.Sprintf("%s ahead: p %s after Holm across %d pairs, on %d common tasks", p.ArmX, formatP(adj), m, n), true
	default:
		return fmt.Sprintf("%s ahead: p %s after Holm across %d pairs, on %d common tasks", p.ArmY, formatP(adj), m, n), true
	}
}

// matrixTiers assigns Tier, TierReason and PVsLeader from the leader's
// pairs: calcs is in rank order, so the leader is first and every pair with
// it has the leader as ArmX.
func matrixTiers(calcs []*entryCalc, armOf map[*entryCalc]string, pairs []schemas.MatrixPair) {
	if len(calcs) == 0 {
		return
	}
	leader := armOf[calcs[0]]
	calcs[0].e.Tier, calcs[0].e.TierReason, calcs[0].e.PVsLeader = TierLeader, "leader", nil
	for _, c := range calcs[1:] {
		c.e.Tier, c.e.PVsLeader = TierLeader, nil
		i := slices.IndexFunc(pairs, func(p schemas.MatrixPair) bool { return p.ArmX == leader && p.ArmY == armOf[c] })
		if i < 0 {
			continue
		}
		p := pairs[i]
		c.e.PVsLeader, c.e.TierReason = p.PAdjusted, p.Reason
		if p.Significant && p.MeanDiff > 0 {
			c.e.Tier = TierBehind
			c.e.TierReason = fmt.Sprintf("behind the leader: p %s after Holm across all pairs, on %d common tasks", formatP(*p.PAdjusted), p.Tasks)
		}
	}
}

// separability decides whether any pair could reach alpha at all: Holm
// multiplies the smallest p by the number of pairs, and the smallest p a
// sign-flip test on n differing tasks can give is stats.MinPossibleP(n), so
// with the largest common task set n no pair is separable when
// MinPossibleP(n) times the pair count is not below alpha. The note says it
// in plain words, or explains the tiers when separation is possible.
func separability(calcs []*entryCalc, pairs []schemas.MatrixPair, alpha float64) (bool, string) {
	if len(calcs) < 2 {
		return false, fmt.Sprintf("Cannot compare: %d arm%s with graded runs on this condition.", len(calcs), plural(len(calcs)))
	}
	maxTasks, tested := 0, 0
	for _, p := range pairs {
		if p.PAdjusted != nil {
			tested++
			maxTasks = max(maxTasks, p.Tasks)
		}
	}
	if tested == 0 {
		return false, "Cannot separate: no two arms have a task in common."
	}
	best := math.Min(1, stats.MinPossibleP(maxTasks)*float64(tested))
	if best >= alpha || stats.MinPossibleP(maxTasks) > alpha {
		return false, fmt.Sprintf("Cannot separate any two arms on this suite: with at most %d common tasks the smallest possible p is %s, "+
			"and Holm across %d pairs makes it %s, not below alpha %s whatever the results. More tasks are needed; more trials per task do not help, "+
			"since the test is over tasks.", maxTasks, formatP(stats.MinPossibleP(maxTasks)), tested, formatP(best), strconv.FormatFloat(alpha, 'g', -1, 64))
	}
	return true, fmt.Sprintf("Tier 1: cannot be told apart from the leader; tier 2: significantly behind it. Exact sign-flip test on the tasks each pair ran, "+
		"Holm-adjusted across all %d pairs at alpha %s. Rank alone is not a claim that one arm beats another.", tested, strconv.FormatFloat(alpha, 'g', -1, 64))
}

// commonTasks returns the tasks every row has a graded run on.
func commonTasks(calcs []*entryCalc) []string {
	var common []string
	for i, c := range calcs {
		tasks := sortedKeys(c.rates)
		if i == 0 {
			common = tasks
			continue
		}
		common = slices.DeleteFunc(common, func(t string) bool { _, ok := c.rates[t]; return !ok })
	}
	return common
}
