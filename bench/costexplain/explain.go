package costexplain

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Report-only measuring costs kept apart from the agent's cost.
const (
	metricJudgeCost     = "judge_cost_usd"
	metricSimulatorCost = "simulator_cost_usd"
	metricCostUSD       = "cost_usd"
)

// splitTolerance is how close the price snapshot must reproduce a row's
// priced cost, relative to it, before the row is split into buckets.
const splitTolerance = 1e-6

// Input is what Explain reads: the experiment's frozen manifest, its sealed
// runs as the store's verified index returns them, a reader for their files
// and the price snapshot used to split each row's cost into its token
// buckets. Prices nil leaves every row unsplit (the unattributed part).
type Input struct {
	Manifest  *schemas.Manifest
	Runs      []schemas.SealedRun
	Read      RunFileReader
	Prices    *evidence.PriceTable
	Synthetic bool
}

// Explain splits the difference in cost between arms B and A of one
// experiment on one condition. It reads the graded runs of both arms
// (Graded), each run's cost rows and gateway rows, and returns the two
// waterfalls, the per-arm cost picture, the measuring costs kept apart and
// any silent cache break.
func Explain(ctx context.Context, in Input, q Query) (*Explanation, error) {
	m := in.Manifest
	if m == nil {
		return nil, fmt.Errorf("%w: no manifest", ErrBadQuery)
	}
	q, err := resolveQuery(m, q)
	if err != nil {
		return nil, err
	}
	byArm, conditions, err := LoadArms(ctx, m, in.Runs, in.Read, q.Condition, []string{q.A, q.B})
	if err != nil {
		return nil, err
	}
	out := ExplainLoaded(m, q, byArm[q.A], byArm[q.B], in.Prices)
	out.Conditions = conditions
	out.Synthetic = in.Synthetic
	if in.Synthetic {
		out.Notes = append([]string{"Synthetic: scripted agent, scripted model or smoke experiment; the numbers are not findings."}, out.Notes...)
	}
	return out, nil
}

// resolveQuery fills the default arms and condition and checks them.
func resolveQuery(m *schemas.Manifest, q Query) (Query, error) {
	arms := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		arms = append(arms, id)
	}
	slices.Sort(arms)
	// Defaults prefer the canonical baseline and candidate, then the other
	// arms, and A/A replicas (A2) last: comparing the baseline with its
	// own copy reports noise, so it is chosen only when asked for.
	order := defaultArmOrder(arms)
	q.A, q.B = strings.TrimSpace(q.A), strings.TrimSpace(q.B)
	if q.A == "" && len(order) > 0 {
		q.A = order[0]
	}
	if q.B == "" {
		for _, id := range order {
			if id != q.A {
				q.B = id
				break
			}
		}
	}
	q.Condition = cmp.Or(strings.TrimSpace(q.Condition), m.Experiment.Primary.Condition, schemas.ConditionClean)
	switch {
	case q.A == "" || q.B == "":
		return q, fmt.Errorf("%w: experiment %s has fewer than two arms", ErrBadQuery, m.ExpID)
	case q.A == q.B:
		return q, fmt.Errorf("%w: arms a and b are both %s", ErrBadQuery, q.A)
	}
	for _, id := range []string{q.A, q.B} {
		if _, ok := m.Arms[id]; !ok {
			return q, fmt.Errorf("%w: experiment %s has no arm %q (arms: %s)", ErrBadQuery, m.ExpID, id, strings.Join(arms, ", "))
		}
	}
	return q, nil
}

// defaultArmOrder ranks sorted arm IDs for default selection: the canonical
// baseline A, the canonical candidate B, other arms, then A/A replicas.
func defaultArmOrder(arms []string) []string {
	rank := func(id string) int {
		switch {
		case id == "A":
			return 0
		case id == "B":
			return 1
		case id == "A2":
			return 3
		}
		return 2
	}
	out := slices.Clone(arms)
	slices.SortStableFunc(out, func(x, y string) int { return cmp.Compare(rank(x), rank(y)) })
	return out
}

// LoadArms reads the graded runs of the listed arms on one condition (all
// arms when arms is empty) and returns them by arm, with the conditions
// that have a graded run, clean first.
func LoadArms(ctx context.Context, m *schemas.Manifest, runs []schemas.SealedRun, read RunFileReader, condition string, arms []string) (map[string][]*RunEvidence, []string, error) {
	out := map[string][]*RunEvidence{}
	seen := map[string]bool{}
	var conditions []string
	for i := range runs {
		if err := ctx.Err(); err != nil {
			return nil, nil, fmt.Errorf("cost explain: %w", err)
		}
		sr := &runs[i]
		if _, ok := m.Arms[sr.Slot.Arm]; !ok {
			continue
		}
		success, ok := Graded(sr)
		if !ok {
			continue
		}
		if !seen[sr.Slot.Condition] {
			seen[sr.Slot.Condition] = true
			conditions = append(conditions, sr.Slot.Condition)
		}
		if sr.Slot.Condition != condition || (len(arms) > 0 && !slices.Contains(arms, sr.Slot.Arm)) {
			continue
		}
		ev, err := LoadRun(sr, success, read)
		if err != nil {
			return nil, nil, err
		}
		out[sr.Slot.Arm] = append(out[sr.Slot.Arm], ev)
	}
	slices.SortFunc(conditions, func(x, y string) int {
		return cmp.Or(compareTrueFirst(x == schemas.ConditionClean, y == schemas.ConditionClean), cmp.Compare(x, y))
	})
	for _, evs := range out {
		slices.SortStableFunc(evs, func(x, y *RunEvidence) int {
			return cmp.Or(x.Run.Run.Timing.Started.Compare(y.Run.Run.Timing.Started), cmp.Compare(x.Run.Run.RunID, y.Run.Run.RunID))
		})
	}
	return out, conditions, nil
}

func compareTrueFirst(x, y bool) int {
	switch {
	case x == y:
		return 0
	case x:
		return -1
	default:
		return 1
	}
}

// ExplainLoaded explains B against A from runs already loaded. It is pure.
func ExplainLoaded(m *schemas.Manifest, q Query, runsA, runsB []*RunEvidence, prices *evidence.PriceTable) *Explanation {
	ga, gb := aggregate(runsA, prices), aggregate(runsB, prices)
	out := &Explanation{
		ExpID: m.ExpID, Suite: m.SuiteID + "@" + m.SuiteVersion, Condition: q.Condition, Conditions: []string{},
		PriceRef: prices.Ref(), Notes: []string{}, Cache: []CacheFinding{},
	}
	out.A = ga.armCost(q.A, m.Arms[q.A])
	out.B = gb.armCost(q.B, m.Arms[q.B])
	out.PerRun = perRunWaterfall(ga, gb)
	out.PerSuccess = perSuccessWaterfall(ga, gb)
	out.Apart = []ApartCost{
		apart("judge", "Judge model (report-only, not in cost_usd)", ga.judge, gb.judge),
		apart("simulator", "User simulator (report-only, not in cost_usd)", ga.sim, gb.sim),
	}
	out.Cache = cacheFindings(m, q, ga, gb)
	out.Notes = append(out.Notes, notes(ga, gb, prices)...)
	return out
}

// agg is one arm's sums over its graded runs. Turn sums cover only the rows
// the price snapshot can split into buckets; every other priced row is
// unattributed, and every unpriced row is unknown.
type agg struct {
	runs []*RunEvidence
	n    int
	succ int

	turns                        float64 // split rows with usage
	fresh, cread, cwrite, out    float64 // tokens of split rows
	costF, costR, costW, costO   float64 // USD of split rows per bucket
	cacheNeutral                 float64 // split rows with every input token at the fresh price
	wasted                       float64 // observed USD of rows without usage
	unattributed                 float64 // observed USD of rows that could not be split
	unkLo, unkHi                 float64 // bounds of unknown rows
	unbounded                    int     // unknown rows without bounds
	unpricedRows, unattrRows     int
	rowsTotal, wastedRows        int
	retries, fallbacks, rejected int
	refusals                     int
	cacheKeyRows, rawRows        int
	failedKnown, failedHi        float64
	failedUnbounded              bool
	runsWithoutRows              int
	allTokens                    schemas.Tokens
	latencySum                   float64
	latencyN                     int
	models, prompts, tools, keys map[string]int
	judge, sim                   []schemas.Value
}

// aggregate sums one arm's runs.
func aggregate(runs []*RunEvidence, prices *evidence.PriceTable) *agg {
	g := &agg{runs: runs, n: len(runs), models: map[string]int{}, prompts: map[string]int{}, tools: map[string]int{}, keys: map[string]int{}}
	for _, ev := range runs {
		if ev.Success {
			g.succ++
		}
		known, lo, hi, unbounded := g.addRun(ev, prices)
		if !ev.Success {
			g.failedKnown += known + lo
			g.failedHi += hi - lo
			g.failedUnbounded = g.failedUnbounded || unbounded
		}
		for k, v := range ev.Prompts {
			g.prompts[k] += v
		}
		for k, v := range ev.Tools {
			g.tools[k] += v
		}
		for k, v := range ev.Keys {
			g.keys[k] += v
		}
		g.refusals += ev.Refusals
		g.cacheKeyRows += ev.CacheKeyRows
		g.rawRows += ev.RawRows
		g.judge = append(g.judge, ev.Run.Metrics[metricJudgeCost])
		g.sim = append(g.sim, ev.Run.Metrics[metricSimulatorCost])
		for _, r := range ev.Requests {
			if r.Kind != "message" {
				continue
			}
			if r.FallbackIndex > 0 {
				g.fallbacks++
			}
			if r.Status == "error" && r.ErrorStatus >= 400 && r.ErrorStatus < 500 {
				g.rejected++
			}
			if r.Status == "success" && r.LatencyMS > 0 {
				g.latencySum += r.LatencyMS
				g.latencyN++
			}
		}
	}
	return g
}

// addRun adds one run's rows and returns its known cost, the bounds of its
// unknown cost and whether any unknown row has no bounds.
func (g *agg) addRun(ev *RunEvidence, prices *evidence.PriceTable) (known, lo, hi float64, unbounded bool) {
	if !ev.RowsFound {
		g.runsWithoutRows++
		v := ev.Run.Metrics[metricCostUSD]
		if x, ok := v.Float(); ok && finite(x) {
			g.unattributed += x
			return x, 0, 0, false
		}
		g.unpricedRows++
		if v.Bounds != nil {
			g.unkLo += v.Bounds[0]
			g.unkHi += v.Bounds[1]
			return 0, v.Bounds[0], v.Bounds[1], false
		}
		g.unbounded++
		return 0, 0, 0, true
	}
	for _, row := range ev.Rows {
		if row.USD.State == schemas.ValueNotApplicable {
			continue
		}
		g.rowsTotal++
		if row.Attempt > 1 {
			g.retries++
		}
		g.allTokens.Fresh += row.Tokens.Fresh
		g.allTokens.CacheRead += row.Tokens.CacheRead
		g.allTokens.CacheWrite += row.Tokens.CacheWrite
		g.allTokens.Out += row.Tokens.Out
		// The served model is recorded for every row that carried tokens,
		// priced or not: an arm on an unpriced model still served that
		// model, and the cache finding needs it to name a model change.
		if tok := row.Tokens; row.Model != "" && tok.Fresh+tok.CacheRead+tok.CacheWrite+tok.Out > 0 {
			g.models[row.Model]++
		}
		usd, ok := row.USD.Float()
		if !ok || !finite(usd) {
			g.unpricedRows++
			if row.USD.Bounds != nil {
				g.unkLo += row.USD.Bounds[0]
				g.unkHi += row.USD.Bounds[1]
				lo += row.USD.Bounds[0]
				hi += row.USD.Bounds[1]
			} else {
				g.unbounded++
				unbounded = true
			}
			continue
		}
		known += usd
		tok := row.Tokens
		if tok.Fresh+tok.CacheRead+tok.CacheWrite+tok.Out == 0 {
			g.wasted += usd
			g.wastedRows++
			continue
		}
		p, ok := prices.Lookup(row.Model)
		if !ok || math.Abs(p.Cost(tok)-usd) > splitTolerance*math.Max(usd, 1e-9) {
			g.unattributed += usd
			g.unattrRows++
			continue
		}
		g.turns++
		g.fresh += float64(tok.Fresh)
		g.cread += float64(tok.CacheRead)
		g.cwrite += float64(tok.CacheWrite)
		g.out += float64(tok.Out)
		g.costF += float64(tok.Fresh) * p.InputPerMTok / 1e6
		g.costR += float64(tok.CacheRead) * p.CacheReadPerMTok / 1e6
		g.costW += float64(tok.CacheWrite) * p.CacheWritePerMTok / 1e6
		g.costO += float64(tok.Out) * p.OutputPerMTok / 1e6
		g.cacheNeutral += (float64(tok.Fresh+tok.CacheRead+tok.CacheWrite)*p.InputPerMTok + float64(tok.Out)*p.OutputPerMTok) / 1e6
	}
	return known, lo, hi, unbounded
}

// knownPerRun is the arm's observed cost per run: split rows, wasted rows
// and unattributed rows.
func (g *agg) knownPerRun() float64 {
	if g.n == 0 {
		return 0
	}
	return (g.costF + g.costR + g.costW + g.costO + g.wasted + g.unattributed) / float64(g.n)
}

// unknownPerRun is the bounds of the arm's unknown cost per run, and false
// when some of it has no bounds.
func (g *agg) unknownPerRun() (lo, hi float64, bounded bool) {
	if g.n == 0 {
		return 0, 0, true
	}
	return g.unkLo / float64(g.n), g.unkHi / float64(g.n), g.unbounded == 0
}

// hasUnknown reports whether any row's cost is unknown.
func (g *agg) hasUnknown() bool { return g.unpricedRows > 0 }

// costValue is known plus the unknown part: observed when nothing is
// unknown, else unknown with bounds when every unknown is bounded.
func (g *agg) costValue(known float64, scale float64, reason string) schemas.Value {
	if !g.hasUnknown() {
		return schemas.Observed(known*scale, schemas.EvidenceGateway)
	}
	lo, hi, bounded := g.unknownPerRun()
	if !bounded {
		return schemas.Unknown(reason, schemas.EvidenceGateway)
	}
	return schemas.UnknownWithin(reason, schemas.EvidenceGateway, (known+lo)*scale, (known+hi)*scale)
}

func (g *agg) unknownReason() string {
	return fmt.Sprintf("%d of %d cost rows unpriced", g.unpricedRows, max(g.rowsTotal, g.unpricedRows))
}

// factors are the multiplicative factors of the split cost per run:
// turns n, input tokens per turn I, output tokens per turn o, cache read
// share sr and cache write share sw of input, and the four bucket prices
// in USD per token. defined says which can be read from the arm: a factor
// whose denominator is zero is undefined and takes the other arm's value.
type factors struct {
	v       [nFactors]float64
	defined [nFactors]bool
}

// Factor indexes.
const (
	fN = iota
	fI
	fO
	fSR
	fSW
	fPF
	fPR
	fPW
	fPO
	nFactors
)

func (g *agg) factors() factors {
	var f factors
	if g.n > 0 {
		f.v[fN], f.defined[fN] = g.turns/float64(g.n), true
	} else {
		f.defined[fN] = true
	}
	input := g.fresh + g.cread + g.cwrite
	if g.turns > 0 {
		f.v[fI], f.defined[fI] = input/g.turns, true
		f.v[fO], f.defined[fO] = g.out/g.turns, true
	}
	if input > 0 {
		f.v[fSR], f.defined[fSR] = g.cread/input, true
		f.v[fSW], f.defined[fSW] = g.cwrite/input, true
	}
	set := func(i int, cost, tokens float64) {
		if tokens > 0 {
			f.v[i], f.defined[i] = cost/tokens, true
		}
	}
	set(fPF, g.costF, g.fresh)
	set(fPR, g.costR, g.cread)
	set(fPW, g.costW, g.cwrite)
	set(fPO, g.costO, g.out)
	return f
}

// modelCost is the split cost per run from the factors.
func modelCost(v [nFactors]float64) float64 {
	return v[fN] * (v[fI]*((1-v[fSR]-v[fSW])*v[fPF]+v[fSR]*v[fPR]+v[fSW]*v[fPW]) + v[fO]*v[fPO])
}

// shapley returns each factor's exact Shapley share of modelCost(b) minus
// modelCost(a): the average, over every order of switching the factors from
// a's value to b's, of the change when that factor switches. The shares sum
// to the difference exactly (up to floating error), whatever the order.
func shapley(a, b [nFactors]float64) [nFactors]float64 {
	const subsets = 1 << nFactors
	var values [subsets]float64
	for mask := 0; mask < subsets; mask++ {
		var v [nFactors]float64
		for i := 0; i < nFactors; i++ {
			if mask&(1<<i) != 0 {
				v[i] = b[i]
			} else {
				v[i] = a[i]
			}
		}
		values[mask] = modelCost(v)
	}
	var weight [nFactors]float64 // weight[k] = k! (n-k-1)! / n!
	for k := 0; k < nFactors; k++ {
		weight[k] = factorial(k) * factorial(nFactors-k-1) / factorial(nFactors)
	}
	var phi [nFactors]float64
	for i := 0; i < nFactors; i++ {
		bit := 1 << i
		for mask := 0; mask < subsets; mask++ {
			if mask&bit != 0 {
				continue
			}
			phi[i] += weight[popcount(mask)] * (values[mask|bit] - values[mask])
		}
	}
	return phi
}

func factorial(n int) float64 {
	out := 1.0
	for i := 2; i <= n; i++ {
		out *= float64(i)
	}
	return out
}

func popcount(x int) int {
	n := 0
	for ; x != 0; x &= x - 1 {
		n++
	}
	return n
}

// alignFactors gives each factor undefined in one arm the other arm's
// value (zero when undefined in both). An undefined factor always multiplies
// a zero in its own arm, so the arm's cost is unchanged, and the change is
// carried by the factor that made it zero.
func alignFactors(fa, fb factors) (a, b [nFactors]float64) {
	for i := 0; i < nFactors; i++ {
		a[i], b[i] = fa.v[i], fb.v[i]
		switch {
		case !fa.defined[i] && fb.defined[i]:
			a[i] = fb.v[i]
		case fa.defined[i] && !fb.defined[i]:
			b[i] = fa.v[i]
		}
	}
	return a, b
}

// costParts is the known difference in cost per run split into parts, in
// waterfall order, without the unknown part.
func costParts(ga, gb *agg) []Part {
	a, b := alignFactors(ga.factors(), gb.factors())
	phi := shapley(a, b)
	perMTok := func(x float64) string { return money(x * 1e6) }
	n := func(g *agg) float64 { return float64(max(g.n, 1)) }
	return []Part{
		{ID: PartPrice, Label: "Price of the served model", USD: observed(phi[fPF] + phi[fPR] + phi[fPW] + phi[fPO]),
			Detail: fmt.Sprintf("USD per million tokens, A then B: input %s vs %s, cache read %s vs %s, cache write %s vs %s, output %s vs %s",
				perMTok(a[fPF]), perMTok(b[fPF]), perMTok(a[fPR]), perMTok(b[fPR]), perMTok(a[fPW]), perMTok(b[fPW]), perMTok(a[fPO]), perMTok(b[fPO]))},
		{ID: PartInputTokens, Label: "Input tokens per turn", USD: observed(phi[fI]),
			Detail: fmt.Sprintf("%.0f vs %.0f input tokens per turn (fresh, cache read and cache write together)", a[fI], b[fI])},
		{ID: PartOutputTokens, Label: "Output tokens per turn", USD: observed(phi[fO]),
			Detail: fmt.Sprintf("%.0f vs %.0f output tokens per turn, reasoning included", a[fO], b[fO])},
		{ID: PartCacheRead, Label: "Cache read share", USD: observed(phi[fSR]),
			Detail: fmt.Sprintf("%s vs %s of input tokens read from the prompt cache", pct(a[fSR]), pct(b[fSR]))},
		{ID: PartCacheWrite, Label: "Cache write share", USD: observed(phi[fSW]),
			Detail: fmt.Sprintf("%s vs %s of input tokens written to the prompt cache", pct(a[fSW]), pct(b[fSW]))},
		{ID: PartTurns, Label: "Model turns per run", USD: observed(phi[fN]),
			Detail: fmt.Sprintf("%.1f vs %.1f priced model turns per run", a[fN], b[fN])},
		{ID: PartWasted, Label: "Wasted attempts (retries, fallbacks, rejected requests)", USD: observed(gb.wasted/n(gb) - ga.wasted/n(ga)),
			Detail: fmt.Sprintf("%.1f vs %.1f attempts per run that returned no usage; providers bill none of a failed attempt, so this is usually $0 and the time is the cost",
				float64(ga.wastedRows)/n(ga), float64(gb.wastedRows)/n(gb))},
		{ID: PartUnattributed, Label: "Priced rows the snapshot cannot split", USD: observed(gb.unattributed/n(gb) - ga.unattributed/n(ga)),
			Detail: fmt.Sprintf("%d vs %d priced rows whose model is not in the price snapshot or was priced with another one, and %d vs %d runs without cost rows (cost_usd only)",
				ga.unattrRows, gb.unattrRows, ga.runsWithoutRows, gb.runsWithoutRows)},
	}
}

// perRunWaterfall splits B's cost per run minus A's.
func perRunWaterfall(ga, gb *agg) Waterfall {
	w := Waterfall{Basis: BasisPerRun, Parts: []Part{}}
	if ga.n == 0 || gb.n == 0 {
		w.From, w.To, w.Total = schemas.NotApplicable(), schemas.NotApplicable(), schemas.NotApplicable()
		w.Reason = fmt.Sprintf("no graded runs: arm A has %d, arm B %d", ga.n, gb.n)
		return w
	}
	ka, kb := ga.knownPerRun(), gb.knownPerRun()
	w.From = ga.costValue(ka, 1, ga.unknownReason())
	w.To = gb.costValue(kb, 1, gb.unknownReason())
	w.Parts = costParts(ga, gb)
	w.Known = sumParts(w.Parts)
	if ga.hasUnknown() || gb.hasUnknown() {
		w.Parts = append(w.Parts, unknownPart(ga, gb, 1/1.0, 1/1.0))
	}
	w.Total = totalOf(kb-ka, w.Parts)
	return w
}

// perSuccessWaterfall splits B's cost per verified success minus A's: the
// cost per run parts scaled by the mean of 1/success rate, plus the part
// the success rate moves (the cost of failed runs spread over fewer or more
// successes), by the two-factor Shapley split of cost per run times
// 1/success rate.
func perSuccessWaterfall(ga, gb *agg) Waterfall {
	w := Waterfall{Basis: BasisPerSuccess, Parts: []Part{}}
	if ga.succ == 0 || gb.succ == 0 {
		w.From, w.To, w.Total = cpsValue(ga), cpsValue(gb), schemas.NotApplicable()
		w.Reason = fmt.Sprintf("cost per verified success is undefined: arm A has %d of %d verified successes, arm B %d of %d", ga.succ, ga.n, gb.succ, gb.n)
		return w
	}
	ua := float64(ga.n) / float64(ga.succ) // 1 / success rate
	ub := float64(gb.n) / float64(gb.succ)
	ka, kb := ga.knownPerRun(), gb.knownPerRun()
	scale := (ua + ub) / 2
	for _, p := range costParts(ga, gb) {
		x, _ := p.USD.Float()
		p.USD = observed(x * scale)
		w.Parts = append(w.Parts, p)
	}
	w.Parts = append(w.Parts, Part{ID: PartSuccessRate, Label: "Success rate (cost of failed runs)", USD: observed((ka + kb) / 2 * (ub - ua)),
		Detail: fmt.Sprintf("%d of %d vs %d of %d runs verified; every failed run's cost is carried by the successes", ga.succ, ga.n, gb.succ, gb.n)})
	w.From, w.To = ga.costValue(ka, ua, ga.unknownReason()), gb.costValue(kb, ub, gb.unknownReason())
	w.Known = sumParts(w.Parts)
	if ga.hasUnknown() || gb.hasUnknown() {
		w.Parts = append(w.Parts, unknownPart(ga, gb, ua, ub))
	}
	w.Total = totalOf(kb*ub-ka*ua, w.Parts)
	return w
}

// cpsValue is an arm's cost per verified success.
func cpsValue(g *agg) schemas.Value {
	if g.n == 0 {
		return schemas.Unknown("no graded run", schemas.EvidenceGateway)
	}
	if g.succ == 0 {
		v := schemas.NotApplicable()
		v.Evidence, v.Reason = schemas.EvidenceGateway, fmt.Sprintf("undefined (0 of %d)", g.n)
		return v
	}
	return g.costValue(g.knownPerRun(), float64(g.n)/float64(g.succ), g.unknownReason())
}

// unknownPart is B's unknown cost minus A's, each scaled (1 per run,
// 1/success rate per success): unknown with bounds when both are bounded,
// unknown without bounds otherwise. Never zero.
func unknownPart(ga, gb *agg, sa, sb float64) Part {
	loA, hiA, okA := ga.unknownPerRun()
	loB, hiB, okB := gb.unknownPerRun()
	p := Part{ID: PartUnpriced, Label: "Cost the rows do not know",
		Detail: fmt.Sprintf("%d vs %d unpriced rows (a model not in the price snapshot, usage not recorded, or a failed attempt without a status)", ga.unpricedRows, gb.unpricedRows)}
	reason := fmt.Sprintf("%d unpriced rows in A, %d in B", ga.unpricedRows, gb.unpricedRows)
	if !okA || !okB {
		p.USD = schemas.Unknown(reason+"; some have no bounds", schemas.EvidenceGateway)
		return p
	}
	p.USD = schemas.UnknownWithin(reason, schemas.EvidenceGateway, loB*sb-hiA*sa, hiB*sb-loA*sa)
	return p
}

// totalOf is the total difference: observed when every part is, unknown
// with the bounds of known plus the unknown part's bounds otherwise.
func totalOf(known float64, parts []Part) schemas.Value {
	for _, p := range parts {
		if p.USD.IsObserved() {
			continue
		}
		if p.USD.Bounds == nil {
			return schemas.Unknown("a part of the difference is unknown without bounds", schemas.EvidenceGateway)
		}
		return schemas.UnknownWithin("a part of the difference is unknown", schemas.EvidenceGateway, known+p.USD.Bounds[0], known+p.USD.Bounds[1])
	}
	return schemas.Observed(known, schemas.EvidenceGateway)
}

func sumParts(parts []Part) float64 {
	s := 0.0
	for _, p := range parts {
		if x, ok := p.USD.Float(); ok {
			s += x
		}
	}
	return s
}

// armCost is the arm's cost picture.
func (g *agg) armCost(arm string, am schemas.ArmManifest) ArmCost {
	out := ArmCost{Arm: arm, Label: am.Label, Model: am.Config.Model, Runs: g.n, Successes: g.succ,
		ServedModels: sortedKeys(g.models), PromptHashes: sortedKeys(g.prompts), ToolHashes: sortedKeys(g.tools), Keys: sortedKeys(g.keys),
		Tokens: g.allTokens, UnpricedRows: g.unpricedRows, UnattributedRows: g.unattrRows, RunsWithoutRows: g.runsWithoutRows}
	if g.n == 0 {
		none := schemas.Unknown("no graded run", schemas.EvidenceGateway)
		out.CostPerRun, out.CostPerSuccess, out.CacheNeutralPerRun, out.CacheNeutralPerSuccess = none, none, none, none
		out.FailedRunCostPerRun, out.CacheReadShare, out.CacheWriteShare, out.LatencyMSPerTurn = none, none, none, none
		return out
	}
	n := float64(g.n)
	known := g.knownPerRun()
	out.CostPerRun = g.costValue(known, 1, g.unknownReason())
	out.CostPerSuccess = cpsValue(g)
	cn := (g.cacheNeutral + g.wasted + g.unattributed) / n
	out.CacheNeutralPerRun = g.costValue(cn, 1, g.unknownReason())
	if g.succ > 0 {
		out.CacheNeutralPerSuccess = g.costValue(cn, n/float64(g.succ), g.unknownReason())
	} else {
		out.CacheNeutralPerSuccess = cpsValue(g)
	}
	switch {
	case g.failedUnbounded:
		out.FailedRunCostPerRun = schemas.Unknown("a failed run has an unpriced row without bounds", schemas.EvidenceGateway)
	case g.failedHi > 0:
		out.FailedRunCostPerRun = schemas.UnknownWithin("a failed run has unpriced rows", schemas.EvidenceGateway, g.failedKnown/n, (g.failedKnown+g.failedHi)/n)
	default:
		out.FailedRunCostPerRun = schemas.Observed(g.failedKnown/n, schemas.EvidenceGateway)
	}
	input := float64(g.allTokens.Fresh + g.allTokens.CacheRead + g.allTokens.CacheWrite)
	if input > 0 {
		out.CacheReadShare = schemas.Observed(float64(g.allTokens.CacheRead)/input, schemas.EvidenceGateway)
		out.CacheWriteShare = schemas.Observed(float64(g.allTokens.CacheWrite)/input, schemas.EvidenceGateway)
	} else {
		out.CacheReadShare = schemas.Unknown("no input tokens recorded", schemas.EvidenceGateway)
		out.CacheWriteShare = out.CacheReadShare
	}
	f := g.factors()
	out.TurnsPerRun, out.InputTokensPerTurn, out.OutputTokensPerTurn = f.v[fN], f.v[fI], f.v[fO]
	price := func(i int) *float64 {
		if !f.defined[i] {
			return nil
		}
		x := f.v[i] * 1e6
		return &x
	}
	out.PriceInput, out.PriceCacheRead, out.PriceCacheWrite, out.PriceOutput = price(fPF), price(fPR), price(fPW), price(fPO)
	out.AttemptsPerRun = float64(g.rowsTotal) / n
	out.WastedAttemptsPerRun = float64(g.wastedRows) / n
	out.RetriesPerRun = float64(g.retries) / n
	out.FallbacksPerRun = float64(g.fallbacks) / n
	out.RejectedPerRun = float64(g.rejected) / n
	out.RefusalsPerRun = float64(g.refusals) / n
	if g.latencyN > 0 {
		out.LatencyMSPerTurn = schemas.Observed(g.latencySum/float64(g.latencyN), schemas.EvidenceGateway)
	} else {
		out.LatencyMSPerTurn = schemas.Unknown("no answered gateway row with a latency", schemas.EvidenceGateway)
	}
	return out
}

// apart compares one report-only measuring cost: the mean of the observed
// per-run values, unknown when no run observed it.
func apart(id, label string, a, b []schemas.Value) ApartCost {
	va, vb := meanValue(a), meanValue(b)
	out := ApartCost{ID: id, Label: label, A: va, B: vb}
	xa, okA := va.Float()
	xb, okB := vb.Float()
	switch {
	case okA && okB:
		out.Diff = schemas.Observed(xb-xa, schemas.EvidenceGateway)
	case va.State == schemas.ValueNotApplicable && vb.State == schemas.ValueNotApplicable:
		out.Diff = schemas.NotApplicable()
	default:
		out.Diff = schemas.Unknown("one arm's value is unknown", schemas.EvidenceGateway)
	}
	return out
}

// meanValue is the mean of the observed values; not applicable when no run
// recorded the metric, unknown when runs recorded it without a value.
func meanValue(vs []schemas.Value) schemas.Value {
	var sum float64
	n, unknown := 0, 0
	for _, v := range vs {
		if x, ok := v.Float(); ok && finite(x) {
			sum += x
			n++
		} else if v.State == schemas.ValueUnknown {
			unknown++
		}
	}
	switch {
	case n == 0 && unknown == 0:
		v := schemas.NotApplicable()
		v.Reason = "not recorded: no judge or simulator in these runs"
		return v
	case n == 0:
		return schemas.Unknown(fmt.Sprintf("unknown in all %d runs", unknown), schemas.EvidenceGateway)
	}
	v := schemas.Observed(sum/float64(n), schemas.EvidenceGateway)
	if unknown > 0 {
		v.Reason = fmt.Sprintf("%d of %d runs unknown", unknown, n+unknown)
	}
	return v
}

// notes says what the reader must know to trust the waterfall.
func notes(ga, gb *agg, prices *evidence.PriceTable) []string {
	var out []string
	if prices == nil {
		out = append(out, "No price snapshot: rows cannot be split into token buckets, so every priced row is in the unattributed part.")
	}
	if ga.unattrRows+gb.unattrRows > 0 {
		out = append(out, fmt.Sprintf("%d priced rows could not be split by the current price snapshot (another snapshot priced them, or the model is not listed); their observed cost is in the unattributed part.", ga.unattrRows+gb.unattrRows))
	}
	if ga.hasUnknown() || gb.hasUnknown() {
		out = append(out, "Some rows are unpriced: their cost is unknown, carried as bounds and never as zero, so the total is a range.")
	}
	out = append(out, "Parts are an exact Shapley split of cost per run over its factors (turns, tokens per turn, cache shares, bucket prices): they sum to the total difference, and the split does not depend on an order.")
	return out
}

// observed is an observed part. A share below snapTolerance is floating
// noise from the Shapley sums and reads as exactly zero.
func observed(x float64) schemas.Value {
	if math.Abs(x) < snapTolerance {
		x = 0
	}
	return schemas.Observed(x, schemas.EvidenceGateway)
}

// snapTolerance is a millionth of a cent.
const snapTolerance = 1e-8

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func money(x float64) string {
	if x < 0 {
		return "-" + money(-x)
	}
	switch {
	case x == 0:
		return "$0"
	case x < 0.00001:
		return fmt.Sprintf("$%.2g", x) // below a thousandth of a cent: keep the digits, not "$0.00000"
	case math.Abs(x) < 0.01:
		return fmt.Sprintf("$%.5f", x)
	default:
		return fmt.Sprintf("$%.3f", x)
	}
}

func pct(x float64) string { return fmt.Sprintf("%.1f%%", 100*x) }
