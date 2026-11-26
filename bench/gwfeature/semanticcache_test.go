package gwfeature

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func scReq() DraftRequest {
	return DraftRequest{Feature: schemas.GatewayFeatureSemanticCache, Suite: "acme-support@1.0.0", Base: "agents/mcploop.yaml", Model: "claude-haiku-4-5", Trials: 2, BudgetUSD: 2}
}

func scEnv(plugins []string, err error) DraftEnv {
	return DraftEnv{Root: "", GatewayGet: func(ctx context.Context, path string, out any) error {
		if path != "/api/plugins/loaded" {
			return fmt.Errorf("unexpected path %s", path)
		}
		if err != nil {
			return err
		}
		b, _ := json.Marshal(map[string]any{"plugins": plugins})
		return json.Unmarshal(b, out)
	}}
}

func TestCacheDraftRefusesWithoutPlugin(t *testing.T) {
	f, ok := Lookup(schemas.GatewayFeatureSemanticCache)
	if !ok || f.Name != "Semantic cache" {
		t.Fatalf("feature not registered: %+v", f)
	}
	want := "Bifrost's semantic cache is not loaded on the gateway these runs use. Load it with a vector store and an embedding provider first."
	for name, env := range map[string]DraftEnv{
		"absent":    scEnv([]string{"telemetry"}, nil),
		"error":     scEnv(nil, errors.New("down")),
		"nogateway": {},
	} {
		if _, _, err := f.Draft(context.Background(), env, scReq()); err == nil || (name == "absent" && err.Error() != want) {
			t.Errorf("%s: err %v", name, err)
		}
	}
	r := scReq()
	r.Options = map[string]string{"grid": "0.8"}
	if _, _, err := f.Draft(context.Background(), scEnv([]string{"semantic_cache"}, nil), r); err == nil {
		t.Error("grid of one threshold accepted")
	}
}

func TestCacheDraftArmsLowestThreshold(t *testing.T) {
	f, _ := Lookup(schemas.GatewayFeatureSemanticCache)
	r := scReq()
	r.Options = map[string]string{"grid": "0.95,0.85,0.90"}
	exp, notes, err := f.Draft(context.Background(), scEnv([]string{"semantic_cache"}, nil), r)
	if err != nil {
		t.Fatal(err)
	}
	if exp.GatewayTest == nil || exp.GatewayTest.Feature != schemas.GatewayFeatureSemanticCache || exp.GatewayTest.Control != "A" || strings.Join(exp.GatewayTest.Variants, ",") != "B" {
		t.Fatalf("gateway test %+v", exp.GatewayTest)
	}
	if _, ok := exp.Arms["A"].Set["gateway.bifrost"]; ok || exp.Arms["A"].Set["model"] != "claude-haiku-4-5" || exp.Arms["A"].Label != "cache off" {
		t.Errorf("A: %+v", exp.Arms["A"])
	}
	b := exp.Arms["B"]
	gb, _ := b.Set["gateway.bifrost"].(map[string]any)
	sc, _ := gb["semantic_cache"].(map[string]any)
	if b.Label != "cache on at 0.85" || b.Set["model"] != "claude-haiku-4-5" || sc["mode"] != "semantic" || sc["scope"] != "arm" || sc["ttl"] != "1h" || sc["threshold"] != 0.85 {
		t.Errorf("B: %+v", b)
	}
	if exp.Conditions[0].Trials != 2 || len(notes) == 0 {
		t.Errorf("trials %d notes %v", exp.Conditions[0].Trials, notes)
	}
}

func scInput(runs []Run) Input {
	return Input{ExpID: "e_cache", Test: schemas.GatewayTest{Feature: schemas.GatewayFeatureSemanticCache, Control: "A", Variants: []string{"B"},
		Options: map[string]string{"grid": "0.80,0.90,0.95", "target": "0.02"}}, Runs: runs}
}

func scRun(id, task, arm string, success bool, cost float64, rows ...Row) Run {
	s, c := success, cost
	for i := range rows {
		if rows[i].ID == "" {
			rows[i].ID = fmt.Sprintf("%s-%d", id, i)
		}
		rows[i].Kind, rows[i].Status = "message", "success"
	}
	return Run{RunID: id, Task: task, Arm: arm, Condition: "clean", Success: &s, CostUSD: &c, Rows: rows}
}

func scMiss(cacheID string, cost float64) Row {
	c := cost
	return Row{CacheID: cacheID, CostUSD: &c, LatencyMS: 900}
}

func scHit(cacheID string, sim float64) Row {
	s, th := sim, 0.80
	return Row{CacheHit: true, CacheID: cacheID, RawKnown: true, CacheSimilarity: &s, CacheThreshold: &th, LatencyMS: 10}
}

func scDetails(t *testing.T, e Effect) cacheDetails {
	t.Helper()
	var d cacheDetails
	if err := json.Unmarshal(e.Details, &d); err != nil {
		t.Fatalf("details %v (%s)", err, e.Details)
	}
	return d
}

func TestCacheNotAppliedWithoutStoredEntries(t *testing.T) {
	runs := []Run{scRun("a1", "t1", "A", true, 1, Row{}), scRun("b1", "t1", "B", true, 1, Row{})}
	e := computeCache(context.Background(), scInput(runs))
	if e.State != StateNotApplied || !strings.HasPrefix(e.Reason, "Bifrost stored nothing for the cache setup") || e.Result != "failed" {
		t.Fatalf("effect %+v", e)
	}
}

func TestCacheNotAppliedWrongThreshold(t *testing.T) {
	h := scHit("k1", 0.9)
	th := 0.85
	h.CacheThreshold = &th
	runs := []Run{scRun("a1", "t1", "A", true, 1, Row{}), scRun("b1", "t1", "B", true, 1, scMiss("k1", 1)), scRun("b2", "t2", "B", true, 0, h)}
	e := computeCache(context.Background(), scInput(runs))
	if e.State != StateNotApplied || e.Reason != "Bifrost used threshold 0.85, not the setup's 0.80" {
		t.Fatalf("effect %+v", e)
	}
}

func TestCacheCurveFromSimilarity(t *testing.T) {
	runs := []Run{
		scRun("a1", "t1", "A", true, 1, Row{}),
		scRun("b1", "t1", "B", true, 1, scMiss("k1", 1)),
		scRun("b2", "t2", "B", true, 0, scHit("k1", 0.82)),
		scRun("b3", "t3", "B", true, 0, scHit("k1", 0.91)),
		scRun("b4", "t4", "B", true, 0, scHit("k1", 0.97)),
	}
	d := scDetails(t, computeCache(context.Background(), scInput(runs)))
	var got []int
	for _, th := range d.Thresholds {
		got = append(got, th.Hits)
	}
	if fmt.Sprint(got) != "[3 2 1]" || !d.Estimated {
		t.Fatalf("hits per threshold %v (%+v)", got, d)
	}
	if d.Thresholds[0].SavedUSD != 3 || d.Thresholds[0].SavedShare == nil || *d.Thresholds[0].SavedShare != 0.75 {
		t.Errorf("saving %+v", d.Thresholds[0])
	}
}

// scCross is a cross task hit run on single call task task, with A runs.
func scCross(i int, aPass, hitPass bool) []Run {
	task := fmt.Sprintf("t%d", i)
	return []Run{
		scRun("a"+task, task, "A", aPass, 1, Row{}),
		scRun("b"+task, task, "B", hitPass, 0, scHit("k0", 0.97)),
	}
}

func TestCacheWrongHitNeedsControlPass(t *testing.T) {
	runs := []Run{scRun("a0", "t0", "A", true, 1, Row{}), scRun("b0", "t0", "B", true, 1, scMiss("k0", 1))}
	runs = append(runs, scCross(1, true, false)...)  // wrong
	runs = append(runs, scCross(2, false, false)...) // undecided
	runs = append(runs, scCross(3, true, true)...)   // right
	d := scDetails(t, computeCache(context.Background(), scInput(runs)))
	th := d.Thresholds[0]
	if th.Hits != 3 || th.Decided != 2 || th.Wrong != 1 || d.Undecided != 1 {
		t.Fatalf("threshold %+v details %+v", th, d)
	}
	if th.WrongShare == nil || *th.WrongShare != 0.5 || th.WrongHigh == nil || *th.WrongHigh <= 0.5 {
		t.Errorf("share %+v", th)
	}
}

func TestCacheMultiCallTaskUndecided(t *testing.T) {
	runs := []Run{
		scRun("a0", "t0", "A", true, 1, Row{}),
		scRun("b0", "t0", "B", true, 1, scMiss("k0", 1)),
		scRun("a1", "t1", "A", true, 1, Row{}, Row{}),
		scRun("b1", "t1", "B", false, 0, scHit("k0", 0.97), Row{}),
	}
	d := scDetails(t, computeCache(context.Background(), scInput(runs)))
	if d.Thresholds[0].Hits != 1 || d.Thresholds[0].Decided != 0 || d.Undecided != 1 || d.SingleCallTask != 1 {
		t.Fatalf("details %+v", d)
	}
}

// scMany gives n cross task hits at each similarity in sims; wrong marks
// which similarities serve wrong answers.
func scMany(n int, sims []float64, wrong map[float64]bool) []Run {
	runs := []Run{scRun("a0", "t0", "A", true, 1, Row{}), scRun("b0", "t0", "B", true, 1, scMiss("k0", 1))}
	i := 1
	for _, s := range sims {
		for j := 0; j < n; j++ {
			task := fmt.Sprintf("t%03d", i)
			runs = append(runs, scRun("a"+task, task, "A", true, 1, Row{}), scRun("b"+task, task, "B", !wrong[s], 0, scHit("k0", s)))
			i++
		}
	}
	return runs
}

func TestCacheRecommendsLowestSafeThreshold(t *testing.T) {
	in := scInput(scMany(200, []float64{0.85, 0.97}, map[float64]bool{0.85: true}))
	in.Test.Options["target"] = "0.05"
	e := computeCache(context.Background(), in)
	d := scDetails(t, e)
	if d.Recommended == nil || *d.Recommended != 0.90 || e.State != StateMeasured {
		t.Fatalf("recommended %v effect %+v", d.Recommended, e)
	}
	if e.Setting == nil || e.Setting.Kind != "plugin_config" {
		t.Fatalf("setting %+v", e.Setting)
	}
	var body struct {
		Name   string             `json:"name"`
		Config map[string]float64 `json:"config"`
	}
	if err := json.Unmarshal([]byte(e.Setting.Body), &body); err != nil || body.Name != "semantic_cache" || body.Config["threshold"] != 0.90 {
		t.Errorf("body %s (%v)", e.Setting.Body, err)
	}
	if !strings.HasPrefix(e.Headline, "Set the threshold to 0.90: 0 wrong in 200 judged hits") {
		t.Errorf("headline %q", e.Headline)
	}
	all := computeCache(context.Background(), scInput(scMany(20, []float64{0.97}, map[float64]bool{0.97: true})))
	if all.Headline != "Keep the cache to exact repeats: every threshold served wrong answers." || scDetails(t, all).Recommended != nil {
		t.Errorf("all wrong: %+v", all)
	}
}

func TestCacheNotEnoughJudgedHits(t *testing.T) {
	e := computeCache(context.Background(), scInput(scMany(3, []float64{0.97}, nil)))
	if e.State != StateNotEnoughData || e.Reason != "3 cache hits could be judged; 10 are needed at a threshold" {
		t.Fatalf("effect %+v", e)
	}
}

func TestCacheNoHitsMentionsIdentityScoping(t *testing.T) {
	runs := []Run{scRun("a1", "t1", "A", true, 1, Row{}), scRun("b1", "t1", "B", true, 1, scMiss("k1", 1))}
	e := computeCache(context.Background(), scInput(runs))
	if e.State != StateNotEnoughData || !strings.Contains(e.Reason, "disable_identity_scoping") {
		t.Fatalf("effect %+v", e)
	}
}
