package handlers

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// "Bifrost cache on vs off": one agent, and the server adds a cache-on copy
// identical except for the semantic cache headers.
func TestEvaluationBifrostCacheArm(t *testing.T) {
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations",
		`{"arms":[{"label":"A","agent":"scripted","model":"m1"}],"benchmarks":["longmemeval"],"bifrost_cache":true}`)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	if !ev.Spec.BifrostCache {
		t.Fatalf("spec lost bifrost_cache: %+v", ev.Spec)
	}
	exp := l.planned[ev.Children[0].ExperimentID]
	off, on := exp.Arms["A"], exp.Arms[schemas.EvaluationCacheArm]
	if len(exp.Arms) != 2 || on.Label != off.Label+schemas.EvaluationCacheSuffix {
		t.Fatalf("arms %+v", exp.Arms)
	}
	if _, set := off.Set["gateway.bifrost"]; set {
		t.Fatalf("cache-off arm sets gateway.bifrost: %+v", off.Set)
	}
	gb, _ := on.Set["gateway.bifrost"].(map[string]any)
	sc, _ := gb["semantic_cache"].(map[string]any)
	if sc["mode"] != schemas.SemanticCacheDirect || sc["scope"] != schemas.SemanticCacheScopeTask || sc["ttl"] != "1h" {
		t.Fatalf("cache-on set %+v", on.Set)
	}
	for k, v := range off.Set {
		if on.Set[k] != v {
			t.Fatalf("arms differ beyond the cache at %s", k)
		}
	}
	est := evalCall(r, "POST", APIPrefix+"/evaluations/estimate",
		`{"arms":[{"label":"A","agent":"scripted","model":"m1"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":4,"trials":2,"bifrost_cache":true}`)
	var e EvalEstimate
	if err := json.Unmarshal(est.Response.Body(), &e); err != nil || len(e.PerBenchmark) != 1 {
		t.Fatalf("estimate %s %v", est.Response.Body(), err)
	}
	if b := e.PerBenchmark[0]; b.Skipped != "" || b.Runs != 2*2*b.Tasks {
		t.Fatalf("estimate runs %d for %d tasks (%s), want both cache arms", b.Runs, b.Tasks, b.Skipped)
	}
	for _, body := range []string{
		`{"arms":[{"label":"A","agent":"scripted","model":"m1"},{"label":"B","agent":"scripted","model":"m2"}],"benchmarks":["longmemeval"],"bifrost_cache":true}`,
		`{"arms":[{"agent":"scripted","model":"m"}],"benchmarks":["longmemeval"],"bifrost_cache":true,"stream":{"memory":"carry"}}`,
	} {
		if got := evalCall(r, "POST", APIPrefix+"/evaluations", body).Response.StatusCode(); got != fasthttp.StatusBadRequest {
			t.Errorf("%s: status %d", body, got)
		}
	}
}
