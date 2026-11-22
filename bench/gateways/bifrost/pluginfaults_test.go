package bifrost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/faultproxy"
	"github.com/maximhq/bifrost/bench/schemas"
)

type fakeProxyCtl struct {
	mu      sync.Mutex
	plans   map[string]schemas.FaultPlan
	cleared []string
}

func (f *fakeProxyCtl) SetPlan(_ context.Context, slot string, p schemas.FaultPlan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.plans == nil {
		f.plans = map[string]schemas.FaultPlan{}
	}
	f.plans[slot] = p
	return nil
}
func (f *fakeProxyCtl) ClearPlan(_ context.Context, slot string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleared = append(f.cleared, slot)
	delete(f.plans, slot)
	return nil
}
func (f *fakeProxyCtl) Ledger(context.Context, string) ([]schemas.LedgerEntry, error) {
	return []schemas.LedgerEntry{{}}, nil
}

// fakePluginGateway stands in for Bifrost with the heimdall plugin: it
// decodes the plan header and fails calls of a persistent plan from
// from_call on, answering 200 otherwise.
func fakePluginGateway(t *testing.T) (*httptest.Server, *[]http.Header) {
	var mu sync.Mutex
	var seen []http.Header
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		n := calls
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		if v := r.Header.Get(HeaderFaultPlan); v != "" {
			p, err := DecodePluginPlan(v)
			if err != nil {
				t.Errorf("decode: %v", err)
			}
			if (p.Mode == PluginModeRateLimit || p.Mode == PluginModeError) && n >= p.FromCall {
				if p.RetryAfterS > 0 {
					w.Header().Set("Retry-After", strconv.Itoa(p.RetryAfterS))
				}
				w.WriteHeader(p.Status)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestPluginFaultsInjectsThroughGateway(t *testing.T) {
	ctx := context.Background()
	proxy := &fakeProxyCtl{}
	ctl, err := SelectFaultController("plugin", proxy)
	if err != nil {
		t.Fatal(err)
	}
	pf := ctl.(*PluginFaults)
	plan := schemas.FaultPlan{Mode: faultproxy.ModePersistent, FromCall: 2, Status: 429, RetryAfterS: 7, RunID: "run-1", Nonce: "n1"}
	if err := pf.SetPlan(ctx, "slot-a", plan); err != nil {
		t.Fatal(err)
	}
	if got := proxy.plans["slot-a"]; got.Mode != faultproxy.ModeNone || got.RunID != "run-1" {
		t.Fatalf("proxy should get a clean pass-through plan, got %+v", got)
	}
	pf.Tag("slot-a", RunTags{EvaluationID: "ev-9", Org: "org-1", Arm: "b"})

	srv, seen := fakePluginGateway(t)
	hc := &http.Client{Transport: pf.Transport("slot-a", nil)}
	var codes []int
	for range 3 {
		resp, err := hc.Post(srv.URL+"/v1/chat/completions", "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		codes = append(codes, resp.StatusCode)
		if resp.StatusCode == 429 && resp.Header.Get("Retry-After") != "7" {
			t.Fatalf("Retry-After = %q", resp.Header.Get("Retry-After"))
		}
	}
	if codes[0] != 200 || codes[1] != 429 || codes[2] != 429 {
		t.Fatalf("codes = %v, want [200 429 429]", codes)
	}
	h := (*seen)[0]
	for k, want := range map[string]string{HeaderRunID: "run-1", HeaderEvaluationID: "ev-9", HeaderOrg: "org-1", HeaderArm: "b", HeaderFaultPlanID: "run-1:persistent"} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	p, _ := DecodePluginPlan(h.Get(HeaderFaultPlan))
	if p.Mode != PluginModeRateLimit || p.Seed != "" || p.Status != 429 || p.FromCall != 2 {
		t.Fatalf("plan = %+v", p)
	}
	if !strings.Contains(pf.HeaderLines("slot-a"), "x-heimdall-run-id: run-1\n") {
		t.Fatalf("header lines: %q", pf.HeaderLines("slot-a"))
	}

	if err := pf.ClearPlan(ctx, "slot-a"); err != nil {
		t.Fatal(err)
	}
	if pf.Headers("slot-a") != nil || len(proxy.cleared) != 1 {
		t.Fatal("clear should drop headers and clear the proxy slot")
	}
}

func TestPluginFaultsFallsBackToProxy(t *testing.T) {
	ctx := context.Background()
	proxy := &fakeProxyCtl{}
	pf := NewPluginFaults(proxy)
	plan := schemas.FaultPlan{Mode: faultproxy.ModeScripted, RunID: "r2"}
	if err := pf.SetPlan(ctx, "s", plan); err != nil {
		t.Fatal(err)
	}
	if proxy.plans["s"].Mode != faultproxy.ModeScripted {
		t.Fatalf("scripted plan must go to the proxy, got %+v", proxy.plans["s"])
	}
	h := pf.Headers("s")
	if h.Get(HeaderFaultPlan) != "" || h.Get(HeaderRunID) != "r2" {
		t.Fatalf("fallback plan: no plan header, run tag kept; got %v", h)
	}
	if es, _ := pf.Ledger(ctx, "r2"); len(es) != 1 {
		t.Fatal("ledger should come from the proxy")
	}
	// No proxy: a plan only the proxy runs is refused, a plugin plan is fine.
	solo := NewPluginFaults(nil)
	if err := solo.SetPlan(ctx, "s", plan); err == nil {
		t.Fatal("want error without a proxy")
	}
	if err := solo.SetPlan(ctx, "s", schemas.FaultPlan{Mode: faultproxy.ModeFlaky, FailProb: 0.3, Status: 503}); err != nil {
		t.Fatal(err)
	}
	if solo.Headers("s").Get(HeaderFaultPlan) == "" {
		t.Fatal("flaky plan should ride as a header")
	}
	// Invalid plans are rejected before anything is bound.
	if err := pf.SetPlan(ctx, "s", schemas.FaultPlan{Mode: faultproxy.ModePersistent}); err == nil {
		t.Fatal("want validation error")
	}
}

func TestSelectFaultController(t *testing.T) {
	proxy := &fakeProxyCtl{}
	for _, m := range []string{"", "proxy", " PROXY "} {
		c, err := SelectFaultController(m, proxy)
		if err != nil || c != schemas.FaultController(proxy) {
			t.Fatalf("mode %q: %v %T", m, err, c)
		}
	}
	if _, err := SelectFaultController("bogus", proxy); err == nil {
		t.Fatal("want error")
	}
	t.Setenv(FaultModeEnv, "plugin")
	c, err := FaultControllerFromEnv(proxy)
	if _, ok := c.(*PluginFaults); err != nil || !ok {
		t.Fatalf("env plugin: %v %T", err, c)
	}
}

func TestToPluginPlanModes(t *testing.T) {
	cases := []struct {
		in   schemas.FaultPlan
		want PluginPlan
	}{
		{schemas.FaultPlan{Mode: "persistent", FromCall: 2, Calls: 3, Status: 503}, PluginPlan{Mode: "error", FromCall: 2, Calls: 3, Status: 503}},
		{schemas.FaultPlan{Mode: "once", Call: 4, Status: 500}, PluginPlan{Mode: "error", FromCall: 4, Calls: 1, Status: 500}},
		{schemas.FaultPlan{Mode: "slow", DelayMS: 100, FirstByteDelayMS: 8000, UntilS: 30}, PluginPlan{Mode: "latency", DelayMS: 8100, UntilS: 30}},
		{schemas.FaultPlan{Mode: "flaky", FailProb: 0.3, Status: 503, Nonce: "x"}, PluginPlan{Mode: "flaky", FailProb: 0.3, Status: 503, Seed: "x"}},
		{schemas.FaultPlan{}, PluginPlan{Mode: "none"}},
	}
	for _, c := range cases {
		got := ToPluginPlan("s", c.in)
		got.ID = ""
		if got != c.want {
			t.Errorf("%+v: got %+v, want %+v", c.in, got, c.want)
		}
		back, err := DecodePluginPlan(EncodePluginPlan(got))
		if err != nil || back != got {
			t.Errorf("round trip: %+v %v", back, err)
		}
	}
	if p, err := DecodePluginPlan(`{"mode":"error","status":500}`); err != nil || p.Status != 500 {
		t.Fatalf("plain JSON: %+v %v", p, err)
	}
}
