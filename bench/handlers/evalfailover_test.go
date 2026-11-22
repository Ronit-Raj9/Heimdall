package handlers

import (
	"strings"
	"testing"

	"github.com/valyala/fasthttp"

	"github.com/maximhq/bifrost/bench/failover"
	"github.com/maximhq/bifrost/bench/schemas"
)

// Lane C07: the failover option adds a fallback arm with a Bifrost
// fallback_chain policy and the primary outage condition.
func TestEvaluationFailoverArmsAndOutage(t *testing.T) {
	l := &evalLauncher{}
	r, _ := evalServer(t, Services{Launcher: l, Suites: evalSuites()})
	ctx := evalCall(r, "POST", APIPrefix+"/evaluations",
		`{"arms":[{"label":"A","agent":"scripted","model":"openai/gpt-4o"}],"benchmarks":["longmemeval"],"tasks_per_benchmark":2,"trials":1,
		  "failover":{"enabled":true,"fallbacks":[{"model":"openai/gpt-4o-mini"}]}}`)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	ev := decodeEval(t, ctx)
	if ev.Spec.Failover == nil || !contains(ev.Spec.Faults, "primary-outage") {
		t.Fatalf("spec %+v", ev.Spec)
	}
	if ev.Spec.Failover.Fallbacks[0].Provider != "openai" || ev.Spec.Failover.Fallbacks[0].Model != "gpt-4o-mini" {
		t.Fatalf("fallbacks not split: %+v", ev.Spec.Failover.Fallbacks)
	}
	exp := l.planned[ev.Children[0].ExperimentID]
	if exp == nil {
		t.Fatalf("no experiment planned: %+v", ev.Children)
	}
	fo, ok := exp.Arms["B"] // the failover arm is arm B (withVariantArms)
	if len(exp.Arms) != 2 || !ok || !strings.HasSuffix(fo.Label, failover.ArmLabelSuffix) {
		t.Fatalf("arms %+v", exp.Arms)
	}
	pol, _ := fo.Set["gateway.routing"].(map[string]any)
	if pol["strategy"] != schemas.RoutingFallbackChain || fo.Set["gateway.provider"] != "openai" || fo.Set["model"] != "gpt-4o" {
		t.Fatalf("failover arm set %+v", fo.Set)
	}
	if _, ok := exp.Arms["A"].Set["gateway.routing"]; ok {
		t.Fatal("plain arm must have no fallbacks")
	}
	var conds []string
	for _, c := range exp.Conditions {
		conds = append(conds, c.ID)
	}
	if !contains(conds, schemas.ConditionClean) || !contains(conds, schemas.GenericProbePrimaryOutage) {
		t.Fatalf("conditions %v", conds)
	}
	if p := schemas.GenericProbe(schemas.GenericProbePrimaryOutage); p == nil || p.Apply.Fault.FromCall != 1 || p.Apply.Fault.Model != "" {
		t.Fatalf("probe %+v", p)
	}

	for _, body := range []string{
		`{"arms":[{"agent":"scripted","model":"openai/gpt-4o"}],"benchmarks":["longmemeval"],"failover":{"enabled":true}}`,
		`{"arms":[{"agent":"scripted","model":"openai/gpt-4o"}],"benchmarks":["longmemeval"],"failover":{"enabled":true,"fallbacks":[{"model":"anthropic/claude-haiku-4-5"}]}}`,
		`{"arms":[{"agent":"scripted","model":"gpt-4o"}],"benchmarks":["longmemeval"],"failover":{"enabled":true,"fallbacks":[{"model":"gpt-4o-mini"}]}}`,
		`{"arms":[{"label":"A","agent":"scripted","model":"openai/gpt-4o"},{"label":"B","agent":"other","model":"openai/gpt-4o"}],"benchmarks":["longmemeval"],"failover":{"enabled":true,"fallbacks":[{"model":"gpt-4o-mini"}]}}`,
	} {
		if got := evalCall(r, "POST", APIPrefix+"/evaluations", body); got.Response.StatusCode() != fasthttp.StatusBadRequest {
			t.Errorf("%s: status %d %s", body, got.Response.StatusCode(), got.Response.Body())
		}
	}
	// Off: dropped, nothing added.
	off := decodeEval(t, evalCall(r, "POST", APIPrefix+"/evaluations",
		`{"arms":[{"agent":"scripted","model":"openai/gpt-4o"}],"benchmarks":["longmemeval"],"failover":{"enabled":false}}`))
	if off.Spec.Failover != nil || contains(off.Spec.Faults, "primary-outage") {
		t.Fatalf("off spec %+v", off.Spec)
	}
}
