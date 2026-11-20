package costexplain

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/evidence"
	"github.com/maximhq/bifrost/bench/schemas"
)

// testPrices has two models with different prices and a cache discount.
func testPrices() *evidence.PriceTable {
	return &evidence.PriceTable{ID: "test-prices", Models: map[string]evidence.ModelPrice{
		"small": {InputPerMTok: 1, OutputPerMTok: 4, CacheReadPerMTok: 0.1, CacheWritePerMTok: 1.25},
		"large": {InputPerMTok: 5, OutputPerMTok: 20, CacheReadPerMTok: 0.5, CacheWritePerMTok: 6.25},
	}}
}

// fixture is an experiment held in memory: a manifest, its sealed runs and
// their files.
type fixture struct {
	m     *schemas.Manifest
	runs  []schemas.SealedRun
	files map[string]map[string][]byte
}

func newFixture() *fixture {
	m := &schemas.Manifest{ExpID: "e_test", SuiteID: "s", SuiteVersion: "1", Arms: map[string]schemas.ArmManifest{
		"A": {Label: "baseline", Config: schemas.AgentConfig{ID: "agent", Model: "small"}, ConfigHash: "hashA"},
		"B": {Label: "candidate", Config: schemas.AgentConfig{ID: "agent", Model: "large"}, ConfigHash: "hashB"},
	}}
	m.Experiment.Primary.Condition = schemas.ConditionClean
	return &fixture{m: m, files: map[string]map[string][]byte{}}
}

// add adds one graded run with its cost rows (and gateway rows when given).
func (f *fixture) add(arm, task string, pass bool, rows []schemas.CostRow, reqs []map[string]any) {
	n := len(f.runs)
	id := fmt.Sprintf("r_%s_%d", arm, n)
	outcome := schemas.OutcomeFail
	if pass {
		outcome = schemas.OutcomePass
	}
	sr := schemas.SealedRun{
		Slot: schemas.Slot{SlotID: "s_" + id, Task: task, Arm: arm, Condition: schemas.ConditionClean, Counted: true, PairID: "p_" + task},
		Run:  schemas.Run{RunID: id, Outcome: outcome, Timing: schemas.RunTiming{Started: time.Date(2026, 11, 1, 0, 0, n, 0, time.UTC)}},
		Dir:  id,
	}
	f.runs = append(f.runs, sr)
	f.files[id] = map[string][]byte{}
	if rows != nil {
		b, _ := json.Marshal(rows)
		f.files[id][FileCost] = b
	}
	if reqs != nil {
		var lines []string
		for _, r := range reqs {
			b, _ := json.Marshal(r)
			lines = append(lines, string(b))
		}
		f.files[id][FileRequests] = []byte(strings.Join(lines, "\n") + "\n")
	}
}

func (f *fixture) read(sr *schemas.SealedRun, name string) ([]byte, bool, error) {
	b, ok := f.files[sr.Dir][name]
	return b, ok, nil
}

func (f *fixture) explain(t *testing.T, prices *evidence.PriceTable) *Explanation {
	t.Helper()
	e, err := Explain(context.Background(), Input{Manifest: f.m, Runs: f.runs, Read: f.read, Prices: prices}, Query{})
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	return e
}

// row prices tokens with the table, as the cost ledger does.
func row(model string, fresh, read, write, out int64, prices *evidence.PriceTable) schemas.CostRow {
	tok := schemas.Tokens{Fresh: fresh, CacheRead: read, CacheWrite: write, Out: out}
	p, _ := prices.Lookup(model)
	return schemas.CostRow{RequestID: fmt.Sprintf("q%d%d%d%d", fresh, read, write, out), Attempt: 1, Model: model, Tokens: tok,
		PriceRef: prices.Ref(), USD: schemas.Observed(p.Cost(tok), schemas.EvidenceGateway)}
}

func failedAttempt(n int) schemas.CostRow {
	return schemas.CostRow{RequestID: "qfail", Attempt: n, Model: "small", USD: schemas.Observed(0, schemas.EvidenceGateway)}
}

func sumRows(rows ...[]schemas.CostRow) float64 {
	s := 0.0
	for _, rs := range rows {
		for _, r := range rs {
			if x, ok := r.USD.Float(); ok {
				s += x
			}
		}
	}
	return s
}

func mustFloat(t *testing.T, what string, v schemas.Value) float64 {
	t.Helper()
	x, ok := v.Float()
	if !ok {
		t.Fatalf("%s = %+v, want observed", what, v)
	}
	return x
}

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-12+1e-9*math.Max(math.Abs(a), math.Abs(b)) }

// checkIdentity asserts the observed parts sum to the total difference.
func checkIdentity(t *testing.T, w Waterfall, want float64) {
	t.Helper()
	total := mustFloat(t, w.Basis+" total", w.Total)
	if !near(total, want) {
		t.Errorf("%s total = %.15g, want %.15g", w.Basis, total, want)
	}
	if !near(sumParts(w.Parts), total) {
		t.Errorf("%s parts sum to %.15g, total %.15g", w.Basis, sumParts(w.Parts), total)
	}
}

func partUSD(t *testing.T, w Waterfall, id string) schemas.Value {
	t.Helper()
	for _, p := range w.Parts {
		if p.ID == id {
			return p.USD
		}
	}
	t.Fatalf("%s has no part %s", w.Basis, id)
	return schemas.Value{}
}

func TestWaterfallPartsSumToTheDifference(t *testing.T) {
	prices := testPrices()
	cases := []struct {
		name string
		a, b [][]schemas.CostRow // per run
		pass [2][]bool
	}{
		{
			name: "every factor moves",
			a:    [][]schemas.CostRow{{row("small", 1000, 9000, 0, 300, prices), row("small", 800, 9500, 200, 250, prices)}, {row("small", 5000, 0, 1000, 900, prices)}},
			b:    [][]schemas.CostRow{{row("large", 4000, 2000, 500, 700, prices), row("large", 3000, 1000, 0, 100, prices), row("small", 100, 0, 0, 50, prices)}, {row("large", 9000, 0, 0, 1200, prices), failedAttempt(2)}, {row("large", 100, 50, 0, 10, prices)}},
			pass: [2][]bool{{true, false}, {true, true, false}},
		},
		{
			name: "arm A answered no turn",
			a:    [][]schemas.CostRow{{failedAttempt(1)}, {failedAttempt(1), failedAttempt(2)}},
			b:    [][]schemas.CostRow{{row("large", 1000, 0, 0, 100, prices)}},
			pass: [2][]bool{{true, false}, {true}},
		},
		{
			name: "a model the snapshot does not list is unattributed but still counted",
			a:    [][]schemas.CostRow{{row("small", 1000, 0, 0, 100, prices)}},
			b:    [][]schemas.CostRow{{row("small", 1000, 0, 0, 100, prices), {RequestID: "x", Attempt: 1, Model: "other", Tokens: schemas.Tokens{Fresh: 10}, USD: schemas.Observed(0.25, schemas.EvidenceGateway)}}},
			pass: [2][]bool{{true}, {true}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture()
			for i, rows := range tc.a {
				f.add("A", fmt.Sprintf("t%d", i), tc.pass[0][i], rows, nil)
			}
			for i, rows := range tc.b {
				f.add("B", fmt.Sprintf("t%d", i), tc.pass[1][i], rows, nil)
			}
			e := f.explain(t, prices)
			costA, costB := sumRows(tc.a...)/float64(len(tc.a)), sumRows(tc.b...)/float64(len(tc.b))
			checkIdentity(t, e.PerRun, costB-costA)
			if got := mustFloat(t, "A cost per run", e.A.CostPerRun); !near(got, costA) {
				t.Errorf("A cost per run = %g, want %g", got, costA)
			}
			sa, sb := count(tc.pass[0]), count(tc.pass[1])
			cpsA, cpsB := sumRows(tc.a...)/float64(sa), sumRows(tc.b...)/float64(sb)
			checkIdentity(t, e.PerSuccess, cpsB-cpsA)
			if got := mustFloat(t, "B cost per success", e.B.CostPerSuccess); !near(got, cpsB) {
				t.Errorf("B cost per success = %g, want %g", got, cpsB)
			}
		})
	}
}

func count(xs []bool) int {
	n := 0
	for _, x := range xs {
		if x {
			n++
		}
	}
	return n
}

func TestOnlyThePriceMovesWhenOnlyTheModelChanges(t *testing.T) {
	prices := testPrices()
	f := newFixture()
	f.add("A", "t1", true, []schemas.CostRow{row("small", 1000, 3000, 0, 200, prices)}, nil)
	f.add("B", "t1", true, []schemas.CostRow{row("large", 1000, 3000, 0, 200, prices)}, nil)
	e := f.explain(t, prices)
	for _, p := range e.PerRun.Parts {
		x := mustFloat(t, p.ID, p.USD)
		if p.ID == PartPrice {
			if !near(x, mustFloat(t, "total", e.PerRun.Total)) {
				t.Errorf("price part %g, want the whole difference", x)
			}
			continue
		}
		if x != 0 {
			t.Errorf("part %s = %g, want 0 when only prices differ", p.ID, x)
		}
	}
}

func TestCacheShareDropIsItsOwnPart(t *testing.T) {
	prices := testPrices()
	f := newFixture()
	// Same model, same tokens per turn and turns; B reads none from cache.
	f.add("A", "t1", true, []schemas.CostRow{row("small", 1000, 9000, 0, 100, prices)}, nil)
	f.add("B", "t1", true, []schemas.CostRow{row("small", 10000, 0, 0, 100, prices)}, nil)
	f.m.Arms["B"] = f.m.Arms["A"]
	e := f.explain(t, prices)
	want := 9000 * (1 - 0.1) / 1e6
	if got := mustFloat(t, "cache read part", partUSD(t, e.PerRun, PartCacheRead)); !near(got, want) {
		t.Errorf("cache read part = %g, want %g", got, want)
	}
	if got := mustFloat(t, "B cache-neutral", e.B.CacheNeutralPerRun); !near(got, mustFloat(t, "A cache-neutral", e.A.CacheNeutralPerRun)) {
		t.Errorf("cache-neutral costs differ (%g): the same tokens at fresh price must cost the same", got)
	}
}

func TestUnknownCostStaysUnknownWithBounds(t *testing.T) {
	prices := testPrices()
	f := newFixture()
	f.add("A", "t1", true, []schemas.CostRow{row("small", 1000, 0, 0, 100, prices)}, nil)
	unpriced := schemas.CostRow{RequestID: "u", Attempt: 1, Model: "mystery", Tokens: schemas.Tokens{Fresh: 100},
		USD: schemas.UnknownWithin("no price", schemas.EvidenceGateway, 0, 0.5)}
	f.add("B", "t1", true, []schemas.CostRow{row("small", 1000, 0, 0, 100, prices), unpriced}, nil)
	e := f.explain(t, prices)
	if e.PerRun.Total.IsObserved() || e.B.CostPerRun.IsObserved() {
		t.Fatalf("total %+v and B cost %+v must be unknown", e.PerRun.Total, e.B.CostPerRun)
	}
	b := e.PerRun.Total.Bounds
	if b == nil || !near(b[0], e.PerRun.Known) || !near(b[1], e.PerRun.Known+0.5) {
		t.Errorf("total bounds = %v, want [known, known+0.5] with known %g", b, e.PerRun.Known)
	}
	if p := partUSD(t, e.PerRun, PartUnpriced); p.IsObserved() || p.Bounds == nil || p.Bounds[1] != 0.5 {
		t.Errorf("unpriced part = %+v, want unknown within [0, 0.5]", p)
	}
	// A failed attempt without a status has no bounds: the total has none.
	f2 := newFixture()
	f2.add("A", "t1", true, []schemas.CostRow{row("small", 1000, 0, 0, 100, prices)}, nil)
	f2.add("B", "t1", true, []schemas.CostRow{{RequestID: "u", Attempt: 1, Model: "small", USD: schemas.Unknown("attempt 1 failed without a status", schemas.EvidenceGateway)}}, nil)
	e2 := f2.explain(t, prices)
	if e2.PerRun.Total.State != schemas.ValueUnknown || e2.PerRun.Total.Bounds != nil {
		t.Errorf("total = %+v, want unknown without bounds", e2.PerRun.Total)
	}
	// A run without cost rows and without cost_usd is unknown, never zero.
	f3 := newFixture()
	f3.add("A", "t1", true, []schemas.CostRow{row("small", 1000, 0, 0, 100, prices)}, nil)
	f3.add("B", "t1", true, nil, nil)
	e3 := f3.explain(t, prices)
	if e3.B.CostPerRun.IsObserved() {
		t.Errorf("B cost per run = %+v, want unknown for a run without cost rows", e3.B.CostPerRun)
	}
}

func TestCostPerSuccessUndefinedWithoutSuccess(t *testing.T) {
	prices := testPrices()
	f := newFixture()
	f.add("A", "t1", false, []schemas.CostRow{row("small", 1000, 0, 0, 100, prices)}, nil)
	f.add("B", "t1", true, []schemas.CostRow{row("small", 1000, 0, 0, 100, prices)}, nil)
	e := f.explain(t, prices)
	if e.PerSuccess.Reason == "" || e.PerSuccess.Total.State != schemas.ValueNotApplicable {
		t.Errorf("per success = %+v, want not applicable with a reason", e.PerSuccess)
	}
	if e.A.CostPerSuccess.State != schemas.ValueNotApplicable || !strings.Contains(e.A.CostPerSuccess.Reason, "0 of 1") {
		t.Errorf("A cost per success = %+v, want undefined (0 of 1)", e.A.CostPerSuccess)
	}
}

// rawRow is a gateway row with a raw detail carrying a system prompt.
func rawRow(prompt, tools, key string, refusal bool) map[string]any {
	out := map[string]any{"role": "assistant", "refusal": nil}
	if refusal {
		out["refusal"] = "I can't help with that."
	}
	return map[string]any{"id": "g", "status": "success", "kind": "message", "latency_ms": 100,
		"raw": map[string]any{
			"input_history":     []map[string]any{{"role": "system", "content": prompt}, {"role": "user", "content": "go"}},
			"tools":             []map[string]any{{"name": tools}},
			"selected_key_name": key,
			"output_message":    out,
		}}
}

func TestCacheBreakNamesTheCause(t *testing.T) {
	prices := testPrices()
	t.Run("system prompt changed between arms", func(t *testing.T) {
		f := newFixture()
		f.m.Arms["B"] = f.m.Arms["A"]
		f.add("A", "t1", true, []schemas.CostRow{row("small", 1000, 9000, 0, 100, prices)}, []map[string]any{rawRow("You are helpful.", "bash", "k1", false)})
		f.add("B", "t1", true, []schemas.CostRow{row("small", 9000, 1000, 0, 100, prices)}, []map[string]any{rawRow("You are helpful. Today is 2026-11-18.", "bash", "k1", true)})
		e := f.explain(t, prices)
		if len(e.Cache) != 1 || e.Cache[0].Kind != FindingBetweenArms || e.Cache[0].Arm != "B" {
			t.Fatalf("cache findings = %+v, want one between-arms drop in B", e.Cache)
		}
		if !strings.HasPrefix(e.Cache[0].Cause, "system prompt changed") {
			t.Errorf("cause = %q, want the system prompt", e.Cache[0].Cause)
		}
		if e.B.RefusalsPerRun != 1 || e.A.RefusalsPerRun != 0 {
			t.Errorf("refusals per run = %g / %g, want 0 / 1", e.A.RefusalsPerRun, e.B.RefusalsPerRun)
		}
	})
	t.Run("same prefix: the evidence does not show the cause", func(t *testing.T) {
		f := newFixture()
		f.m.Arms["B"] = f.m.Arms["A"]
		f.add("A", "t1", true, []schemas.CostRow{row("small", 1000, 9000, 0, 100, prices)}, []map[string]any{rawRow("p", "bash", "k1", false)})
		f.add("B", "t1", true, []schemas.CostRow{row("small", 9000, 1000, 0, 100, prices)}, []map[string]any{rawRow("p", "bash", "k1", false)})
		e := f.explain(t, prices)
		if len(e.Cache) != 1 || !strings.HasPrefix(e.Cache[0].Cause, "not shown by the evidence") {
			t.Fatalf("cache findings = %+v, want a drop whose cause the evidence does not show", e.Cache)
		}
	})
	t.Run("over time with the same prefix", func(t *testing.T) {
		f := newFixture()
		for i := 0; i < 4; i++ {
			read, fresh := int64(9000), int64(1000)
			if i >= 2 {
				read, fresh = 1000, 9000
			}
			f.add("A", fmt.Sprintf("t%d", i), true, []schemas.CostRow{row("small", fresh, read, 0, 100, prices)}, []map[string]any{rawRow("p", "bash", "k1", false)})
			f.add("B", fmt.Sprintf("t%d", i), true, []schemas.CostRow{row("small", fresh, read, 0, 100, prices)}, []map[string]any{rawRow("p", "bash", "k1", false)})
		}
		e := f.explain(t, prices)
		var kinds []string
		for _, c := range e.Cache {
			kinds = append(kinds, c.Kind+":"+c.Arm)
			if !strings.HasPrefix(c.Cause, "same system prompt and tool list throughout") {
				t.Errorf("cause = %q, want the same-prefix cause", c.Cause)
			}
		}
		if strings.Join(kinds, ",") != "over_time:A,over_time:B" {
			t.Errorf("findings = %v, want over-time drops in A and B", kinds)
		}
	})
	t.Run("no drop, no finding", func(t *testing.T) {
		f := newFixture()
		f.add("A", "t1", true, []schemas.CostRow{row("small", 1000, 9000, 0, 100, prices)}, nil)
		f.add("B", "t1", true, []schemas.CostRow{row("large", 1500, 8500, 0, 100, prices)}, nil)
		if e := f.explain(t, prices); len(e.Cache) != 0 {
			t.Errorf("cache findings = %+v, want none for a 5 point move", e.Cache)
		}
	})
}

func TestQueryRefusesUnknownArms(t *testing.T) {
	f := newFixture()
	for _, q := range []Query{{A: "A", B: "A"}, {A: "Z"}} {
		if _, err := Explain(context.Background(), Input{Manifest: f.m, Read: f.read}, q); err == nil {
			t.Errorf("query %+v: want an error", q)
		}
	}
}

func point(id string, x, y float64) ParetoPoint {
	return ParetoPoint{ID: id, X: schemas.Observed(x, schemas.EvidenceGateway), Y: schemas.Observed(y, schemas.EvidenceOutcome)}
}

func TestFrontier(t *testing.T) {
	pts := []ParetoPoint{
		point("cheap-weak", 1, 0.5),
		point("dear-strong", 5, 0.9),
		point("dominated", 6, 0.8), // dearer and weaker than dear-strong
		point("middle", 3, 0.7),
		point("tie", 3, 0.7), // identical to middle: neither dominates
		point("same-cost-weaker", 3, 0.6),
		{ID: "unknown-cost", X: schemas.Unknown("cost unknown in 1 of 3 runs", schemas.EvidenceGateway), Y: schemas.Observed(1, schemas.EvidenceOutcome)},
		{ID: "no-success", X: schemas.NotApplicable(), Y: schemas.Observed(0, schemas.EvidenceOutcome)},
	}
	out := Frontier(pts)
	got := map[string]ParetoPoint{}
	var order []string
	for _, p := range out {
		got[p.ID] = p
		order = append(order, p.ID)
	}
	for id, want := range map[string]bool{"cheap-weak": true, "dear-strong": true, "middle": true, "tie": true, "dominated": false, "same-cost-weaker": false} {
		if got[id].Frontier != want || !got[id].Placed {
			t.Errorf("%s: frontier %v placed %v, want frontier %v", id, got[id].Frontier, got[id].Placed, want)
		}
	}
	if d := strings.Join(got["dominated"].DominatedBy, ","); d != "dear-strong" {
		t.Errorf("dominated by %q, want dear-strong", d)
	}
	if d := strings.Join(got["same-cost-weaker"].DominatedBy, ","); d != "middle,tie" {
		t.Errorf("same-cost-weaker dominated by %q, want middle,tie", d)
	}
	for _, id := range []string{"unknown-cost", "no-success"} {
		if got[id].Placed || got[id].Frontier || got[id].Reason == "" {
			t.Errorf("%s = %+v, want not placed with a reason", id, got[id])
		}
	}
	if want := "cheap-weak,middle,tie,same-cost-weaker,dear-strong,dominated,no-success,unknown-cost"; strings.Join(order, ",") != want {
		t.Errorf("order = %v, want %s", order, want)
	}
}

func TestParetoFromMatrix(t *testing.T) {
	entry := func(succ, cps float64) schemas.LeaderboardEntry {
		return schemas.LeaderboardEntry{Success: schemas.Observed(succ, schemas.EvidenceOutcome), Metrics: map[string]schemas.Value{
			entryCostPerSuccess: schemas.Observed(cps, schemas.EvidenceGateway), entryCostUSD: schemas.Observed(cps*succ, schemas.EvidenceGateway)}}
	}
	mx := &schemas.ModelMatrix{Rows: []schemas.MatrixRow{{Arm: "A", LeaderboardEntry: entry(0.8, 0.10)}, {Arm: "B", LeaderboardEntry: entry(0.6, 0.20)}}}
	p, err := ParetoFromMatrix(mx, "")
	if err != nil {
		t.Fatal(err)
	}
	if p.X != XCostPerSuccess || strings.Join(p.Frontier, ",") != "A" {
		t.Errorf("pareto = %+v, want x cost_per_success and frontier A", p)
	}
	if _, err := ParetoFromMatrix(mx, "tokens"); err == nil {
		t.Error("x tokens: want an error")
	}
}

func TestShapleyIsExactAndSymmetric(t *testing.T) {
	a := [nFactors]float64{3, 1000, 100, 0.5, 0.1, 1e-6, 1e-7, 1.25e-6, 4e-6}
	b := [nFactors]float64{5, 3000, 50, 0.2, 0, 5e-6, 5e-7, 6.25e-6, 2e-5}
	phi := shapley(a, b)
	sum := 0.0
	for _, x := range phi {
		sum += x
	}
	if !near(sum, modelCost(b)-modelCost(a)) {
		t.Errorf("shares sum to %g, want %g", sum, modelCost(b)-modelCost(a))
	}
	back := shapley(b, a)
	for i := range phi {
		if !near(phi[i], -back[i]) {
			t.Errorf("factor %d: %g forward, %g backward; want opposite", i, phi[i], back[i])
		}
	}
}

// TestCacheKeyEvidenceCountsEachRowOnce pins the counts the prompt_cache_key
// cause cites: two of three rows in A carry a key and none of two in B.
func TestCacheKeyEvidenceCountsEachRowOnce(t *testing.T) {
	prices := testPrices()
	f := newFixture()
	f.m.Arms["B"] = f.m.Arms["A"]
	keyed := func(on bool) map[string]any {
		r := rawRow("p", "bash", "k1", false)
		if on {
			r["raw"].(map[string]any)["params"] = map[string]any{"prompt_cache_key": "pk"}
		}
		return r
	}
	f.add("A", "t1", true, []schemas.CostRow{row("small", 1000, 9000, 0, 100, prices)}, []map[string]any{keyed(true), keyed(true), keyed(false)})
	f.add("B", "t1", true, []schemas.CostRow{row("small", 9000, 1000, 0, 100, prices)}, []map[string]any{keyed(false), keyed(false)})
	e := f.explain(t, prices)
	var found bool
	for _, c := range e.Cache {
		if strings.Contains(c.Cause, "prompt_cache_key") {
			found = true
			if !strings.Contains(c.Cause, "on 2 of 3 rows and arm B on 0 of 2") {
				t.Errorf("cause = %q, want counts 2 of 3 and 0 of 2", c.Cause)
			}
		}
	}
	if !found {
		t.Fatalf("cache findings = %+v, want the prompt_cache_key cause", e.Cache)
	}
}

// TestDefaultArmsSkipTheAACopy pins that the default comparison is the
// baseline against the candidate, never the baseline against its A/A copy.
func TestDefaultArmsSkipTheAACopy(t *testing.T) {
	f := newFixture()
	f.m.Arms["A2"] = f.m.Arms["A"]
	q, err := resolveQuery(f.m, Query{})
	if err != nil || q.A != "A" || q.B != "B" {
		t.Fatalf("default query = %+v, %v; want A vs B", q, err)
	}
	if q, err := resolveQuery(f.m, Query{A: "A", B: "A2"}); err != nil || q.B != "A2" {
		t.Errorf("explicit A2 = %+v, %v; want it kept", q, err)
	}
	if q, _ := resolveQuery(f.m, Query{A: "B"}); q.B != "A" {
		t.Errorf("default b with a=B is %q, want A", q.B)
	}
}
