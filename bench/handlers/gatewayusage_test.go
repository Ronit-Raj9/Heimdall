package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func newGatewayUsageTest(secret string) *gatewayUsageHandler {
	return &gatewayUsageHandler{
		env:    OrgEnv{Mode: schemas.ModeLive},
		getenv: func(k string) string { return map[string]string{GatewayUsageSecretEnv: secret}[k] },
		store:  &gatewayUsageStore{runs: map[string][]GatewayUsageRecord{}, dropped: map[string]int{}},
	}
}

func gatewayUsagePost(h *gatewayUsageHandler, auth, body string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI(GatewayUsagePath)
	if auth != "" {
		ctx.Request.Header.Set("Authorization", auth)
	}
	ctx.Request.SetBodyString(body)
	h.ingest(ctx)
	return ctx
}

func gatewayUsageGet(h *gatewayUsageHandler, runID string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(GatewayUsagePath + "?run_id=" + runID)
	h.read(ctx)
	return ctx
}

func TestGatewayUsageIngest(t *testing.T) {
	good := `{"records":[` +
		`{"run_id":"r1","org_id":"o1","provider":"openai","model":"gpt-4o-mini","input_tokens":10,"output_tokens":5,"total_tokens":15,"cost_usd":0.01,"latency_ms":100,"cache_hit":true},` +
		`{"run_id":"r1","org_id":"o1","provider":"anthropic","model":"claude","total_tokens":20,"cost_usd":0.02,"latency_ms":300,"fallback_used":true,"fault_applied":"rate_limit","status_code":429},` +
		`{"run_id":"r1","org_id":"o2","provider":"openai","model":"x","total_tokens":99}]}`
	cases := []struct {
		name, secret, auth, body string
		want                     int
	}{
		{"off without secret", "", "Bearer s", good, fasthttp.StatusServiceUnavailable},
		{"no auth", "s", "", good, fasthttp.StatusUnauthorized},
		{"wrong secret", "s", "Bearer t", good, fasthttp.StatusUnauthorized},
		{"missing run id", "s", "Bearer s", `{"records":[{"provider":"openai"}]}`, fasthttp.StatusBadRequest},
		{"unknown field", "s", "Bearer s", `{"records":[],"x":1}`, fasthttp.StatusBadRequest},
		{"accepted", "s", "Bearer s", good, fasthttp.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newGatewayUsageTest(c.secret)
			ctx := gatewayUsagePost(h, c.auth, c.body)
			if got := ctx.Response.StatusCode(); got != c.want {
				t.Fatalf("status %d, want %d: %s", got, c.want, ctx.Response.Body())
			}
		})
	}
	t.Run("too many records", func(t *testing.T) {
		h := newGatewayUsageTest("s")
		recs := strings.Repeat(`{"run_id":"r"},`, maxGatewayUsageBatch+1)
		ctx := gatewayUsagePost(h, "Bearer s", `{"records":[`+strings.TrimSuffix(recs, ",")+`]}`)
		if ctx.Response.StatusCode() != fasthttp.StatusRequestEntityTooLarge {
			t.Fatalf("status %d", ctx.Response.StatusCode())
		}
	})
	t.Run("read back with totals", func(t *testing.T) {
		h := newGatewayUsageTest("s")
		gatewayUsagePost(h, "Bearer s", good)
		ctx := gatewayUsageGet(h, "r1")
		if ctx.Response.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
		}
		var out GatewayUsageRun
		if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
			t.Fatal(err)
		}
		if out.Summary.Calls != 3 || out.Summary.CacheHits != 1 || out.Summary.Fallbacks != 1 || out.Summary.Faults != 1 || out.Summary.Errors != 1 {
			t.Fatalf("summary %+v", out.Summary)
		}
		if ctx := gatewayUsageGet(h, "nope"); ctx.Response.StatusCode() != fasthttp.StatusNotFound {
			t.Fatalf("unknown run status %d", ctx.Response.StatusCode())
		}
	})
	t.Run("org filter", func(t *testing.T) {
		h := newGatewayUsageTest("s")
		gatewayUsagePost(h, "Bearer s", good)
		out, ok := h.store.get("r1", "o1")
		if !ok || out.Summary.Calls != 2 || out.Summary.TotalTokens != 35 || out.Summary.MeanLatencyMS != 200 {
			t.Fatalf("o1 view %+v", out.Summary)
		}
		if _, ok := h.store.get("r1", "o3"); ok {
			t.Fatal("other org sees rows")
		}
	})
}

func TestGatewayUsageStoreBounds(t *testing.T) {
	s := &gatewayUsageStore{runs: map[string][]GatewayUsageRecord{}, dropped: map[string]int{}}
	recs := make([]GatewayUsageRecord, maxGatewayUsagePerRun+3)
	for i := range recs {
		recs[i].RunID = "big"
	}
	if got := s.add(recs); got.Accepted != maxGatewayUsagePerRun || got.Dropped != 3 {
		t.Fatalf("add %+v", got)
	}
	for i := 0; i < maxGatewayUsageRuns; i++ {
		s.add([]GatewayUsageRecord{{RunID: "r" + string(rune('a'+i%26)) + strings.Repeat("x", i/26)}})
	}
	if _, ok := s.runs["big"]; ok {
		t.Fatal("oldest run not evicted")
	}
	if len(s.runs) != maxGatewayUsageRuns {
		t.Fatalf("runs %d", len(s.runs))
	}
}

func TestSealedGatewayUsageKeepsRunOrgOnly(t *testing.T) {
	prev := gatewayUsage
	gatewayUsage = &gatewayUsageStore{runs: map[string][]GatewayUsageRecord{}, dropped: map[string]int{}}
	defer func() { gatewayUsage = prev }()
	gatewayUsage.add([]GatewayUsageRecord{
		{RunID: "r1", OrgID: "o1", RequestID: "mine", Provider: "openai", Model: "m", CostUSD: 0.5, FaultApplied: "rate_limit"},
		{RunID: "r1", OrgID: "o2", RequestID: "theirs"},
		{RunID: "r1", RequestID: "untagged"},
	})
	data, ok := SealedGatewayUsage("r1", "o1")
	if !ok {
		t.Fatal("no rows sealed")
	}
	var got []PluginUsage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].RequestID != "mine" || got[0].Fault != "rate_limit" || got[0].CostUSD == nil || *got[0].CostUSD != 0.5 {
		t.Fatalf("sealed rows: %+v", got)
	}
	if data, ok := SealedGatewayUsage("r1", ""); !ok || !strings.Contains(string(data), "untagged") || strings.Contains(string(data), "mine") {
		t.Fatalf("local seal keeps only untagged rows: %s", data)
	}
	if _, ok := SealedGatewayUsage("r1", "o3"); ok {
		t.Fatal("org with no rows sealed something")
	}
	if _, ok := SealedGatewayUsage("nope", "o1"); ok {
		t.Fatal("unknown run sealed something")
	}
	// The in-memory read for the panel is org-checked too.
	if run, ok := GatewayUsageForRun("r1", "o2"); !ok || len(run.Records) != 1 || run.Records[0].RequestID != "theirs" {
		t.Fatalf("org read: %+v", run)
	}
}
