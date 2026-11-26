package investigator

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

type fakeGateway struct {
	mu      sync.Mutex
	minted  []*schemas.RunContext
	revoked int
}

func (g *fakeGateway) GetName() string    { return "fake" }
func (g *fakeGateway) GetVersion() string { return "1" }
func (g *fakeGateway) Preflight(context.Context) (*schemas.GatewayManifest, error) {
	return &schemas.GatewayManifest{}, nil
}
func (g *fakeGateway) MintKey(_ context.Context, rc *schemas.RunContext) (*schemas.RunKey, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.minted = append(g.minted, rc)
	return &schemas.RunKey{ID: "vk1", Value: "sk-investigator", Provider: rc.Agent.Gateway.Provider, Model: rc.Agent.Model}, nil
}
func (g *fakeGateway) RevokeKey(context.Context, *schemas.RunKey) error {
	g.mu.Lock()
	g.revoked++
	g.mu.Unlock()
	return nil
}
func (g *fakeGateway) RevokeStale(context.Context, map[string]bool) (int, error) { return 0, nil }
func (g *fakeGateway) FetchRequests(context.Context, *schemas.RunKey, schemas.FetchExpectation) (*schemas.RequestLog, error) {
	return nil, nil
}

// testPrice: $1 per million input tokens, $2 per million output tokens.
func testPrice(model string, tok schemas.Tokens) (float64, bool) {
	if model != "gpt-test" {
		return 0, false
	}
	return float64(tok.Fresh+tok.CacheRead)/1e6 + float64(tok.Out)*2/1e6, true
}

// replyServer answers each call with the next reply and keeps every body.
type replyServer struct {
	mu      sync.Mutex
	replies []string
	bodies  []string
}

func (s *replyServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-bf-vk") != "sk-investigator" {
			t.Errorf("call without the job key")
		}
		raw, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.bodies = append(s.bodies, string(raw))
		reply := `{"ok":true}`
		if len(s.replies) > 0 {
			reply = s.replies[0]
			s.replies = s.replies[1:]
		}
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{
			"model":   "gpt-test",
			"choices": []any{map[string]any{"message": map[string]any{"content": reply}}},
			"usage":   map[string]any{"prompt_tokens": 1000, "completion_tokens": 200},
		})
	}
}

func newTestRunner(t *testing.T, h http.Handler, budget float64, maxCalls int) (*Runner, *fakeGateway) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	gw := &fakeGateway{}
	r, err := New(Config{Gateway: gw, URL: srv.URL, Provider: "openai", Model: "gpt-test", Price: testPrice, BudgetUSD: budget, MaxCalls: maxCalls, JobID: "j1"})
	if err != nil {
		t.Fatal(err)
	}
	return r, gw
}

func TestRunnerRefusesUnpricedModel(t *testing.T) {
	_, err := New(Config{Gateway: &fakeGateway{}, URL: "http://x", Provider: "openai", Model: "unpriced", Price: testPrice, BudgetUSD: 1})
	if err == nil || !strings.Contains(err.Error(), "no price") {
		t.Fatalf("want a refusal for an unpriced model, got %v", err)
	}
	if _, err := New(Config{Gateway: &fakeGateway{}, URL: "http://x", Provider: "openai", Model: "gpt-test", Price: testPrice}); err == nil {
		t.Fatal("want a refusal for a zero budget")
	}
}

func TestRunnerReservesWorstCase(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("the gateway was called although the call could pass the cap")
	})
	r, _ := newTestRunner(t, h, 0.01, 0)
	_, err := r.Call(context.Background(), Request{Purpose: "findings", Messages: []Message{{Role: "user", Content: strings.Repeat("a", 50000)}}, MaxTokens: 5000})
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("want ErrBudget, got %v", err)
	}
	if r.Spent() != 0 {
		t.Fatalf("spent %v, want 0", r.Spent())
	}
}

func TestRunnerRedactsBeforeSend(t *testing.T) {
	s := &replyServer{}
	r, _ := newTestRunner(t, s.handler(t), 1, 0)
	secret := "sk-abcdefghijklmnopqrstuvwxyz123456"
	_, err := r.Call(context.Background(), Request{Purpose: "findings", Messages: []Message{{Role: "user", Content: "key " + secret + " mail jane.doe@example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	body := s.bodies[0]
	if strings.Contains(body, secret) || strings.Contains(body, "jane.doe@example.com") {
		t.Fatalf("the body still holds a secret: %s", body)
	}
	if !strings.Contains(body, "[REDACTED api key]") || !strings.Contains(body, "[REDACTED email]") {
		t.Fatalf("the body has no redaction placeholders: %s", body)
	}
	// Raw bodies are redacted too, and keep only the pinned model.
	_, err = r.Call(context.Background(), Request{Purpose: "replay_step", Raw: []byte(`{"model":"other/x","stream":true,"messages":[{"role":"user","content":"` + secret + `"}]}`)})
	if err != nil {
		t.Fatal(err)
	}
	raw := s.bodies[1]
	if strings.Contains(raw, secret) || !strings.Contains(raw, `"openai/gpt-test"`) || !strings.Contains(raw, `"stream":false`) {
		t.Fatalf("raw body not redacted or not pinned: %s", raw)
	}
}

func TestRunnerSchemaRetry(t *testing.T) {
	s := &replyServer{replies: []string{"not json", `{"items":"wrong"}`, `{"items":[]}`}}
	r, _ := newTestRunner(t, s.handler(t), 1, 0)
	schema := `{"type":"object","required":["items"],"properties":{"items":{"type":"array"}}}`
	rep, err := r.Call(context.Background(), Request{Purpose: "findings", Schema: schema, Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Text != `{"items":[]}` || rep.Attempts != 3 {
		t.Fatalf("reply %+v", rep)
	}
	rows := r.Calls()
	if len(rows) != 3 {
		t.Fatalf("ledger rows %d, want 3", len(rows))
	}
	sum := 0.0
	for _, row := range rows {
		sum += row.CostUSD
	}
	if sum != r.Spent() || sum <= 0 || rep.CostUSD != sum {
		t.Fatalf("spent %v, ledger sum %v, reply cost %v", r.Spent(), sum, rep.CostUSD)
	}
	if !strings.Contains(s.bodies[1], "not valid") {
		t.Fatalf("the retry does not carry the parse error: %s", s.bodies[1])
	}
}

func TestRunnerMaxCalls(t *testing.T) {
	s := &replyServer{}
	r, _ := newTestRunner(t, s.handler(t), 1, 2)
	for i := 0; i < 2; i++ {
		if _, err := r.Call(context.Background(), Request{Purpose: "findings", Messages: []Message{{Role: "user", Content: "hi"}}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Call(context.Background(), Request{Purpose: "findings", Messages: []Message{{Role: "user", Content: "hi"}}}); !errors.Is(err, ErrBudget) {
		t.Fatalf("third call: want ErrBudget, got %v", err)
	}
	if len(s.bodies) != 2 {
		t.Fatalf("gateway calls %d, want 2", len(s.bodies))
	}
}

func TestRunnerCloseRevokesOnce(t *testing.T) {
	s := &replyServer{}
	r, gw := newTestRunner(t, s.handler(t), 1, 0)
	if err := r.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gw.revoked != 1 {
		t.Fatalf("revoked %d times, want 1", gw.revoked)
	}
	if len(gw.minted) != 1 || gw.minted[0].Agent.ID != "investigator" || gw.minted[0].Run.RunID != "r_iv_j1" {
		t.Fatalf("minted %+v", gw.minted)
	}
}
