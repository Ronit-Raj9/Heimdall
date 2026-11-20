package faultproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// realSink is a fake real upstream that counts every request it receives.
func realSink(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_real","type":"message","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	t.Cleanup(sink.Close)
	return sink, &hits
}

func realConfig(sinkURL string) Config {
	var providers []ProviderSpec
	for _, p := range DefaultProviders() {
		p.BaseURL = sinkURL
		providers = append(providers, p)
	}
	return Config{
		Providers: providers,
		Getenv:    func(k string) string { return map[string]string{"ANTHROPIC_API_KEY": "sk-ant-real"}[k] },
		Slots:     []SlotConfig{{Name: "slot-a", Auth: "auth-a"}},
	}
}

// TestScriptedFaultPlanNeverForwards is 3-C-01: a fault plan with Scripted
// set (a probe on a smoke or scripted_upstream run) answers the calls it does
// not fault from the scripted model and never reaches a keyed upstream.
func TestScriptedFaultPlanNeverForwards(t *testing.T) {
	sink, hits := realSink(t)
	env := newTestEnvConfig(t, realConfig(sink.URL))
	env.setPlan(t, "slot-a", schemas.FaultPlan{Mode: ModePersistent, FromCall: 3, Status: 503, RunID: "r_smoke", Nonce: "n1", Scripted: true})
	for i := 1; i <= 3; i++ {
		resp, body := env.call(t, http.MethodPost, "/v1/messages", "auth-a", msgBody)
		want := http.StatusOK
		if i == 3 {
			want = http.StatusServiceUnavailable
		}
		if resp.StatusCode != want || strings.Contains(string(body), "msg_real") {
			t.Fatalf("call %d: status %d body %s", i, resp.StatusCode, body)
		}
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("the real upstream got %d request(s) under a scripted fault plan", n)
	}
	es := env.entries(t)
	if es[0].Upstream != schemas.LedgerUpstreamScripted || es[2].Action != ActionInject {
		t.Fatalf("ledger %+v", es)
	}
	h, err := env.client.Capabilities(context.Background())
	if err != nil || !strings.Contains(strings.Join(h.PlanFields, ","), "scripted") {
		t.Fatalf("health plan fields %+v %v", h, err)
	}
}

// TestPlansSurviveRestart is 3-B-01: a scripted plan bound before a proxy
// restart is still bound after it, so the run's next call is answered by
// the scripted model and tagged with its run, not forwarded upstream.
func TestPlansSurviveRestart(t *testing.T) {
	sink, hits := realSink(t)
	cfg := realConfig(sink.URL)
	cfg.LedgerPath = filepath.Join(t.TempDir(), "ledger.jsonl")
	cfg.ControlToken = "tok"
	start := func() (*Server, *httptest.Server, *httptest.Server) {
		srv, err := NewServer(cfg)
		if err != nil {
			t.Fatalf("NewServer: %v", err)
		}
		return srv, httptest.NewServer(srv.Handler()), httptest.NewServer(srv.ControlHandler())
	}
	srv, data, ctrl := start()
	c := NewClient(ctrl.URL, WithToken("tok"))
	ctx := context.Background()
	if err := c.SetPlan(ctx, "slot-a", schemas.FaultPlan{Mode: ModeScripted, RunID: "r_check"}); err != nil {
		t.Fatal(err)
	}
	data.Close()
	ctrl.Close()
	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}

	srv, data, ctrl = start() // the restart
	defer func() { data.Close(); ctrl.Close(); _ = srv.Close() }()
	if p, ok := srv.plans.get("slot-a"); !ok || p.Mode != ModeScripted || p.RunID != "r_check" {
		t.Fatalf("plan after restart = %+v (%v)", p, ok)
	}
	req, _ := http.NewRequest(http.MethodPost, data.URL+"/v1/messages", strings.NewReader(msgBody))
	req.Header.Set("x-api-key", "auth-a")
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || hits.Load() != 0 {
		t.Fatalf("after restart: status %d, real upstream hits %d", resp.StatusCode, hits.Load())
	}
	es, err := NewClient(ctrl.URL, WithToken("tok")).Ledger(ctx, "r_check")
	if err != nil || len(es) != 1 || es[0].Upstream != schemas.LedgerUpstreamScripted {
		t.Fatalf("ledger for r_check after restart: %+v %v", es, err)
	}
}

// TestCorruptPlanFileRefusesStart: a plan file that cannot be read stops the
// proxy from starting rather than starting with every plan forgotten.
func TestCorruptPlanFileRefusesStart(t *testing.T) {
	cfg := realConfig("http://127.0.0.1:1")
	cfg.LedgerPath = filepath.Join(t.TempDir(), "ledger.jsonl")
	if err := writePlans(PlansPath(cfg.LedgerPath), map[string]schemas.FaultPlan{"slot-a": {Mode: "bogus"}}); err != nil {
		t.Fatal(err)
	}
	if srv, err := NewServer(cfg); err == nil {
		_ = srv.Close()
		t.Fatal("NewServer started over an invalid plan file")
	}
}
