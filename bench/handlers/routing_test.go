package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// routingIndex is a two-arm experiment (W9): A fixed sonnet, B a sonnet to
// haiku fallback chain; one sealed model-outage run per arm, B's answered
// its third turn on the haiku fallback.
func routingIndex() *fakeIndex {
	chain := &schemas.RoutingPolicy{Strategy: schemas.RoutingFallbackChain,
		Targets: []schemas.RouteTarget{{Model: "claude-sonnet-4-5"}}, Fallbacks: []schemas.RouteTarget{{Model: "claude-haiku-4-5"}}}
	cfg := func(p *schemas.RoutingPolicy) schemas.AgentConfig {
		return schemas.AgentConfig{ID: "toy", Model: "claude-sonnet-4-5", Gateway: schemas.GatewayPolicy{Provider: "anthropic", Routing: p}}
	}
	m := &schemas.Manifest{ExpID: "e_rt", Arms: map[string]schemas.ArmManifest{
		"A": {Label: "fixed sonnet", Config: cfg(&schemas.RoutingPolicy{Strategy: schemas.RoutingFixed, Targets: []schemas.RouteTarget{{Model: "claude-sonnet-4-5"}}})},
		"B": {Label: "fallback chain", Config: cfg(chain)},
	}, Routing: map[string]string{"A": "hash-a", "B": "hash-b"}}
	obs := func(v float64) string { return fmt.Sprintf(`{"state":"observed","v":%g,"evidence":"gateway"}`, v) }
	run := func(id, arm string) RunView {
		return RunView{Slot: schemas.Slot{SlotID: "s_" + id, ExpID: "e_rt", Arm: arm, Condition: "model-outage", Counted: true},
			Run: schemas.Run{RunID: id, ExpID: "e_rt", Attempt: 1}, Sealed: true}
	}
	files := map[string]map[string][]byte{
		"r_a": {FileMetrics: []byte(`{"fallback_used":` + obs(0) + `,"served_model_switches":` + obs(0) +
			`,"routed_target_anthropic_claude_sonnet_4_5":` + obs(1) + `,"routed_cost_usd_anthropic_claude_sonnet_4_5":` + obs(0.004) + `}`),
			FileEvidence: []byte(`{"requests":{"complete":true,"row_ids":[],"requests":[]}}`), FileGates: []byte(`[]`)},
		"r_b": {FileMetrics: []byte(`{"fallback_used":` + obs(1) + `,"served_model_switches":` + obs(1) +
			`,"routed_target_anthropic_claude_sonnet_4_5":` + obs(2.0/3) + `,"routed_target_anthropic_claude_haiku_4_5":` + obs(1.0/3) +
			`,"routed_cost_usd_anthropic_claude_haiku_4_5":` + obs(0.0008) + `,"routed_cost_usd_anthropic_claude_sonnet_4_5":{"state":"unknown","reason":"x","evidence":"gateway"}` +
			`,"routed_latency_ms_anthropic_claude_haiku_4_5":` + obs(7.5) + `}`),
			FileEvidence: []byte(`{"requests":{"complete":true,"row_ids":["p","f"],"requests":[` +
				`{"id":"p","kind":"message","provider":"anthropic","model_requested":"claude-sonnet-4-5","model_served":"claude-sonnet-4-5","fallback_index":0,"status":"error","error_status":503,"latency_ms":5,"stream":false,"cache_hit":false,"timestamp":"2026-11-18T00:00:00Z","routing_rule_name":"heimdall-route-r_b","routing_engines":["routing-rule"]},` +
				`{"id":"f","parent_id":"p","kind":"message","provider":"anthropic","model_requested":"claude-haiku-4-5","model_served":"claude-haiku-4-5","fallback_index":1,"status":"success","latency_ms":7.5,"stream":false,"cache_hit":false,"timestamp":"2026-11-18T00:00:01Z","routing_rule_name":"heimdall-route-r_b","routing_engines":["routing-rule","core"]},` +
				`{"id":"c","kind":"count_tokens","provider":"anthropic","model_requested":"x","model_served":"x","fallback_index":0,"status":"success","latency_ms":1,"stream":false,"cache_hit":false,"timestamp":"2026-11-18T00:00:02Z"}]}}`),
			FileGates: []byte(`[{"gate":"model_fidelity","version":"1.0.0","effect":"invalidate","fired":false,"detail":"routing policy fallback_chain: 1 request(s) served by another policy model than claude-sonnet-4-5 (expected switch)"}]`)},
	}
	return &fakeIndex{manifests: map[string]*schemas.Manifest{"e_rt": m}, runs: []RunView{run("r_a", "A"), run("r_b", "B")}, files: files}
}

func TestExperimentRouting(t *testing.T) {
	idx := routingIndex()
	out, err := ExperimentRouting(idx, idx.manifests["e_rt"])
	if err != nil {
		t.Fatal(err)
	}
	if !out.Routed || len(out.Arms) != 2 || out.Arms[1].PolicyHash != "hash-b" {
		t.Fatalf("summary = %+v", out)
	}
	if got := out.Arms[1].Summary; got != "fallback_chain: anthropic/claude-sonnet-4-5; fallbacks anthropic/claude-haiku-4-5" {
		t.Errorf("summary line = %q", got)
	}
	want := []string{"strategy: A fixed; B fallback_chain", "fallbacks: A none; B claude-haiku-4-5"}
	if strings.Join(out.Differences, "|") != strings.Join(want, "|") {
		t.Errorf("differences = %q, want %q", out.Differences, want)
	}
	b := out.Arms[1].Conditions[0]
	if b.Runs != 1 || b.FallbackRuns != 1 || b.FallbackKnown != 1 || b.Switches != 1 || len(b.Targets) != 2 {
		t.Fatalf("arm B outage = %+v", b)
	}
	sonnet, haiku := b.Targets[0], b.Targets[1]
	if sonnet.Target != "anthropic/claude-sonnet-4-5" || sonnet.CostUSD != nil || haiku.Target != "anthropic/claude-haiku-4-5" ||
		haiku.CostUSD == nil || *haiku.CostUSD != 0.0008 || haiku.LatencyMS == nil || *haiku.LatencyMS != 7.5 {
		t.Errorf("targets = %+v %+v", sonnet, haiku)
	}
}

func TestRoutingRoutes(t *testing.T) {
	h := NewRoutingHandler(testMode, routingIndex())
	call := func(handler fasthttp.RequestHandler, id string) (int, map[string]any) {
		var ctx fasthttp.RequestCtx
		ctx.SetUserValue("id", id)
		handler(&ctx)
		var body map[string]any
		if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
			t.Fatalf("decode %s: %v", ctx.Response.Body(), err)
		}
		return ctx.Response.StatusCode(), body
	}
	status, body := call(h.getExperimentRouting, "e_rt")
	if status != 200 {
		t.Fatalf("experiment routing status %d: %v", status, body)
	}
	checkKeys(t, "RoutingSummary", body, loadKeys(t, "RoutingSummary"), true)
	for path, typ := range map[string]string{"arms[]": "RoutingArm", "arms[].conditions[]": "RoutingCondition", "arms[].conditions[].targets[]": "RoutingTargetUse",
		"arms[].policy": "RoutingPolicy", "arms[].policy.targets[]": "RouteTarget"} {
		for i, obj := range objectsAt(t, body, path) {
			checkKeys(t, fmt.Sprintf("%s #%d", path, i), obj, loadKeys(t, typ), false)
		}
	}
	status, body = call(h.getRunRouting, "r_b")
	if status != 200 {
		t.Fatalf("run routing status %d: %v", status, body)
	}
	checkKeys(t, "RunRouting", body, loadKeys(t, "RunRouting"), true)
	rows := objectsAt(t, body, "rows[]")
	for i, obj := range rows {
		checkKeys(t, fmt.Sprintf("rows #%d", i), obj, loadKeys(t, "RunRoutingRow"), false)
	}
	if len(rows) != 2 || rows[1]["fallback_index"] != float64(1) || rows[1]["model_served"] != "claude-haiku-4-5" || body["fallback_used"] != true {
		t.Errorf("run routing = %v", body)
	}
	if f, _ := body["fidelity"].(map[string]any); f == nil || !strings.Contains(f["detail"].(string), "expected switch") {
		t.Errorf("fidelity = %v", body["fidelity"])
	}
	if status, _ := call(h.getExperimentRouting, "e_missing"); status != 404 {
		t.Errorf("missing experiment status %d", status)
	}
	if status, _ := call(h.getRunRouting, "r_missing"); status != 404 {
		t.Errorf("missing run status %d", status)
	}
}
