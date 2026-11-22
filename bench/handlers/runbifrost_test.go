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

func TestLoadRunBifrostPrefersLiveUsage(t *testing.T) {
	idx := routingIndex()
	idx.files["r_b"][FileGatewayUsage] = []byte(`[{"request_id":"file-only","cache_hit":true}]`)
	if out, err := LoadRunBifrost(idx, "r_b", ""); err != nil || out.CacheHits != 1 {
		t.Fatalf("file fallback: %+v %v", out, err)
	}
	prev := gatewayUsage
	gatewayUsage = &gatewayUsageStore{runs: map[string][]GatewayUsageRecord{
		"r_b": {{RequestID: "live", RunID: "r_b", Provider: "openai", Model: "gpt-5", FaultApplied: "rate_limit"}},
	}, dropped: map[string]int{}}
	defer func() { gatewayUsage = prev }()
	out, err := LoadRunBifrost(idx, "r_b", "")
	if err != nil {
		t.Fatal(err)
	}
	if out.CacheHits != 0 || out.Faults < 1 {
		t.Fatalf("live rows should win over the run file: %+v", out)
	}
}

func TestLoadRunBifrostOrgChecksLiveUsage(t *testing.T) {
	idx := routingIndex()
	idx.files["r_b"][FileGatewayUsage] = []byte(`[{"request_id":"sealed","cache_hit":true}]`)
	prev := gatewayUsage
	gatewayUsage = &gatewayUsageStore{runs: map[string][]GatewayUsageRecord{
		"r_b": {{RequestID: "other-org", RunID: "r_b", OrgID: "o2", FaultApplied: "rate_limit"}},
	}, dropped: map[string]int{}}
	defer func() { gatewayUsage = prev }()
	out, err := LoadRunBifrost(idx, "r_b", "o1")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range out.CallList {
		if c.ID == "other-org" {
			t.Fatalf("another org's live row shown: %+v", out.CallList)
		}
	}
	if out.CacheHits != 1 {
		t.Fatalf("sealed file should be used: %+v", out)
	}
}

func TestLoadRunBifrostFaultPath(t *testing.T) {
	idx := routingIndex()
	// No fault applied: no path.
	if out, err := LoadRunBifrost(idx, "r_b", ""); err != nil || out.FaultPath != "" {
		t.Fatalf("clean run fault path = %q (%v), want empty", out.FaultPath, err)
	}
	// A run file that holds the plugin's fault row: the run's recorded path wins.
	idx.files["r_b"][FileGatewayUsage] = []byte(`[{"request_id":"f","fault":"inject 503"}]`)
	for i := range idx.runs {
		if idx.runs[i].Run.RunID == "r_b" {
			idx.runs[i].Run.Gateway.FaultPath = "plugin"
		}
	}
	out, err := LoadRunBifrost(idx, "r_b", "")
	if err != nil || out.Faults != 1 || out.FaultPath != "plugin" {
		t.Fatalf("plugin run = %+v (%v), want fault path plugin", out, err)
	}
	// Faults with no recorded path: the default proxy mode applied them.
	for i := range idx.runs {
		if idx.runs[i].Run.RunID == "r_b" {
			idx.runs[i].Run.Gateway.FaultPath = ""
		}
	}
	out, err = LoadRunBifrost(idx, "r_b", "")
	if err != nil || out.FaultPath != "proxy" {
		t.Fatalf("proxy-mode run fault path = %q (%v), want proxy", out.FaultPath, err)
	}
}
