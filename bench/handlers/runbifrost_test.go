package handlers

import (
	"encoding/json"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func TestBuildRunBifrost(t *testing.T) {
	usd := 0.002
	log := &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{
		{ID: "p", Kind: "message", Provider: "anthropic", ModelRequested: "claude-sonnet-4-5", ModelServed: "claude-sonnet-4-5", Status: "error", ErrorStatus: 503, LatencyMS: 5},
		{ID: "f", ParentID: "p", Kind: "message", Provider: "openai", ModelRequested: "gpt-5", ModelServed: "gpt-5", FallbackIndex: 1, Status: "success", LatencyMS: 15,
			Tokens: &schemas.Tokens{Fresh: 100, Out: 20}, BifrostUSD: &usd},
		{ID: "c", Kind: "count_tokens", Provider: "anthropic", Status: "success", LatencyMS: 1},
	}}
	ledger := []schemas.LedgerEntry{{Call: 1, Action: "inject", Status: 503}, {Call: 2, Action: "pass", Status: 200}}
	usage := []PluginUsage{{RequestID: "f", CacheHit: true}, {RequestID: "x", Provider: "anthropic", Model: "claude-haiku-4-5", InputTokens: 10, OutputTokens: 2, Fault: "rate_limit", LatencyMS: 10}}
	out := BuildRunBifrost("r1", log, ledger, usage)
	if !out.Available || !out.Complete || out.Source != "both" || out.Calls != 3 {
		t.Fatalf("out = %+v", out)
	}
	if out.Fallbacks != 1 || out.CacheHits != 1 || out.Faults != 2 || out.Errors != 1 {
		t.Errorf("counts: fallbacks %d cache %d faults %d errors %d", out.Fallbacks, out.CacheHits, out.Faults, out.Errors)
	}
	if out.CallList[0].Fault != "inject 503" || out.CallList[1].Fault != "" || out.CallList[2].Fault != "rate_limit" {
		t.Errorf("faults per call: %+v", out.CallList)
	}
	if out.Tokens.Fresh != 110 || out.Tokens.Out != 22 || out.CostUSD == nil || *out.CostUSD != usd || out.CostKnown != 1 {
		t.Errorf("tokens %+v cost %v known %d", out.Tokens, out.CostUSD, out.CostKnown)
	}
	if len(out.Providers) != 2 || len(out.Models) != 3 || out.LatencyMS != 30 || out.AvgLatency != 10 {
		t.Errorf("providers %v models %v latency %v avg %v", out.Providers, out.Models, out.LatencyMS, out.AvgLatency)
	}
}

func TestBuildRunBifrostEmpty(t *testing.T) {
	out := BuildRunBifrost("r0", nil, nil, nil)
	if out.Available || out.Calls != 0 || out.CostUSD != nil || out.CallList == nil {
		t.Fatalf("out = %+v", out)
	}
	if out := BuildRunBifrost("r2", nil, nil, []PluginUsage{{Provider: "openai", Model: "gpt-5", FallbackUsed: true}}); out.Source != "plugin_usage" || !out.Complete || out.Fallbacks != 1 {
		t.Fatalf("plugin only = %+v", out)
	}
}

func TestRunBifrostRoute(t *testing.T) {
	idx := routingIndex()
	idx.files["r_b"][FileGatewayUsage] = []byte(`[{"request_id":"f","cache_hit":true,"fault":""}]`)
	h := NewRunBifrostHandler(testMode, idx)
	ctx := &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "r_b")
	h.getRunBifrost(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var out RunBifrost
	if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Calls != 2 || out.Fallbacks != 1 || out.CacheHits != 1 || out.Errors != 1 || out.Source != "both" {
		t.Fatalf("out = %+v", out)
	}
	ctx = &fasthttp.RequestCtx{}
	ctx.SetUserValue("id", "nope")
	h.getRunBifrost(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("missing run status %d", ctx.Response.StatusCode())
	}
}

func TestPluginUsageOf(t *testing.T) {
	got := pluginUsageOf([]GatewayUsageRecord{{RequestID: "f", Provider: "openai", Model: "gpt-5", InputTokens: 3, OutputTokens: 4, CostUSD: 0.1, LatencyMS: 9, CacheHit: true, FaultApplied: "rate_limit"}})
	if len(got) != 1 || got[0].Fault != "rate_limit" || *got[0].CostUSD != 0.1 || got[0].InputTokens != 3 || !got[0].CacheHit {
		t.Fatalf("got %+v", got)
	}
}
