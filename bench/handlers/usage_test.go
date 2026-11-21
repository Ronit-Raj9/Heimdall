package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// usageIndex is testIndex with seal times on 2026-11-10 (t01) and
// 2026-11-12 (t02), models per arm and cost/tokens metrics:
// A runs 0.10 USD / 100 tokens, B runs 0.25 USD / 300 tokens; r_t02_B has
// an unknown cost.
func usageIndex() *fakeIndex {
	idx := testIndex()
	m := idx.manifests[testExp]
	a, b := m.Arms["A"], m.Arms["B"]
	a.Config.Model, a.Config.ID = "gpt-5-mini", "cfg-a"
	b.Config.Model, b.Config.ID = "claude-sonnet", "cfg-b"
	m.Arms["A"], m.Arms["B"] = a, b
	for i := range idx.runs {
		rv := &idx.runs[i]
		day := 10
		if rv.Slot.Task == "t02" {
			day = 12
		}
		sealed := time.Date(2026, 11, day, 9, 0, 0, 0, time.UTC)
		rv.Run.Timing.Started = sealed.Add(-time.Minute)
		rv.Run.Timing.Sealed = &sealed
		metrics := `{"cost_usd":{"state":"observed","v":0.10},"tokens":{"state":"observed","v":100}}`
		if rv.Slot.Arm == "B" {
			metrics = `{"cost_usd":{"state":"observed","v":0.25},"tokens":{"state":"observed","v":300}}`
		}
		if rv.Run.RunID == "r_t02_B" {
			metrics = `{"cost_usd":{"state":"unknown","v":null,"reason":"cost_not_measured"},"tokens":{"state":"observed","v":300}}`
		}
		idx.files[rv.Run.RunID][FileMetrics] = []byte(metrics)
	}
	return idx
}

func usageClient(t *testing.T, svc Services) *testClient {
	t.Helper()
	r := router.New()
	env := OrgEnv{Mode: testMode, Services: svc, Common: []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)}}
	for _, area := range OrgAreas() {
		area(env, r)
	}
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func getUsage(t *testing.T, c *testClient, query string, want int) UsageReport {
	t.Helper()
	resp := c.do(t, "GET", APIPrefix+"/usage"+query, nil, nil)
	if resp.StatusCode() != want {
		t.Fatalf("GET /usage%s = %d, want %d: %s", query, resp.StatusCode(), want, resp.Body())
	}
	var out UsageReport
	if want == fasthttp.StatusOK {
		if err := json.Unmarshal(resp.Body(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return out
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestUsageTotalsMatchRunCosts(t *testing.T) {
	c := usageClient(t, Services{OrgID: "o1", Index: usageIndex()})
	out := getUsage(t, c, "?from=2026-11-09&to=2026-11-12", fasthttp.StatusOK)
	if !near(out.TotalCostUSD, 0.10+0.25+0.10) || out.Runs != 4 || out.UnknownCostRuns != 1 || !near(out.TotalTokens, 800) {
		t.Fatalf("totals %+v", out)
	}
	if out.Experiments != 1 || len(out.Buckets) != 4 {
		t.Fatalf("buckets %+v", out.Buckets)
	}
	if out.Buckets[0].Key != "2026-11-09" || out.Buckets[0].Runs != 0 || !near(out.Buckets[1].CostUSD, 0.35) || !near(out.Buckets[3].CostUSD, 0.10) {
		t.Fatalf("day buckets %+v", out.Buckets)
	}

	byModel := getUsage(t, c, "?from=2026-11-09&to=2026-11-12&group_by=model", fasthttp.StatusOK)
	if len(byModel.Buckets) != 2 || byModel.Buckets[0].Key != "claude-sonnet" || !near(byModel.Buckets[0].CostUSD, 0.25) ||
		byModel.Buckets[0].UnknownCostRuns != 1 || !near(byModel.Buckets[1].CostUSD, 0.20) {
		t.Fatalf("model buckets %+v", byModel.Buckets)
	}

	only10 := getUsage(t, c, "?from=2026-11-10&to=2026-11-10&group_by=agent", fasthttp.StatusOK)
	if only10.Runs != 2 || !near(only10.TotalCostUSD, 0.35) {
		t.Fatalf("range filter %+v", only10)
	}
}

func TestUsageKeysAndErrors(t *testing.T) {
	master := make([]byte, 32)
	ks, err := keystore.Open(nil, master)
	if err != nil {
		t.Fatal(err)
	}
	k, err := ks.Create(context.Background(), "o1", keystore.KindProvider, keystore.Put{Provider: "openai", Label: "main", Value: "sk-secret-1234", BudgetUSD: 50}, "u")
	if err != nil {
		t.Fatal(err)
	}
	if err := ks.AddSpend(context.Background(), "o1", k.ID, 1.5); err != nil {
		t.Fatal(err)
	}
	c := usageClient(t, Services{OrgID: "o1", Index: usageIndex(), Keys: ks})
	resp := c.do(t, "GET", APIPrefix+"/usage?group_by=key", nil, nil)
	if resp.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode(), resp.Body())
	}
	if bytes.Contains(resp.Body(), []byte("sk-secret")) {
		t.Fatal("usage echoed a key secret")
	}
	var out UsageReport
	_ = json.Unmarshal(resp.Body(), &out)
	if len(out.Keys) != 1 || !near(out.KeysMonthSpent, 1.5) || len(out.Buckets) != 1 || out.Buckets[0].Label != "main" {
		t.Fatalf("keys %+v buckets %+v", out.Keys, out.Buckets)
	}

	getUsage(t, c, "?group_by=bogus", fasthttp.StatusBadRequest)
	getUsage(t, c, "?from=2026-11-12&to=2026-11-01", fasthttp.StatusBadRequest)
	getUsage(t, c, "?from=2024-01-01&to=2026-11-01", fasthttp.StatusBadRequest)
	getUsage(t, c, "?from=nope", fasthttp.StatusBadRequest)

	empty := usageClient(t, Services{OrgID: "o1"})
	getUsage(t, empty, "", fasthttp.StatusServiceUnavailable)
}

func TestAdminUsageNeedsServerAdmin(t *testing.T) {
	r := router.New()
	NewAdminUsageHandler(testMode, nil).RegisterRoutes(r)
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(APIPrefix + "/admin/usage")
	ctx.Request.Header.SetMethod("GET")
	r.Handler(ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusForbidden {
		t.Fatalf("anonymous admin usage = %d, want 403", ctx.Response.StatusCode())
	}
}
