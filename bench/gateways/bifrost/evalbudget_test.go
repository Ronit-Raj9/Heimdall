package bifrost

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGovernance is a Bifrost governance customer API: create, read (with
// a settable budget usage) and delete.
type fakeGovernance struct {
	mu      sync.Mutex
	created []map[string]any
	usage   float64
	limit   float64
	deleted []string
	exists  bool
}

func (f *fakeGovernance) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/governance/customers", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode: %v", err)
		}
		f.mu.Lock()
		f.created = append(f.created, body)
		f.limit = body["budgets"].([]any)[0].(map[string]any)["max_limit"].(float64)
		f.exists = true
		f.mu.Unlock()
		writeJSONT(w, 200, map[string]any{"message": "Customer created successfully", "customer": map[string]any{"id": "cust-1", "name": body["name"]}})
	})
	mux.HandleFunc("GET /api/governance/customers/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if !f.exists {
			writeJSONT(w, 404, map[string]any{"error": map[string]any{"message": "Customer not found"}})
			return
		}
		writeJSONT(w, 200, map[string]any{"customer": map[string]any{"id": r.PathValue("id"),
			"budgets": []any{map[string]any{"max_limit": f.limit, "current_usage": f.usage, "reset_duration": "1w"}}}})
	})
	mux.HandleFunc("DELETE /api/governance/customers/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.deleted = append(f.deleted, r.PathValue("id"))
		if !f.exists {
			writeJSONT(w, 404, map[string]any{"error": map[string]any{"message": "Customer not found"}})
			return
		}
		f.exists = false
		writeJSONT(w, 200, map[string]any{"message": "Customer deleted successfully"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestEvaluationBudgetLifecycle(t *testing.T) {
	fg := &fakeGovernance{}
	srv := fg.server(t)
	g, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := g.OpenEvaluationBudget(ctx, "e_demo", 0, 0); err == nil {
		t.Fatal("a zero budget was accepted")
	}
	id, err := g.OpenEvaluationBudget(ctx, "e_demo", 2.5, 30)
	if err != nil || id != "cust-1" {
		t.Fatalf("open: %q %v", id, err)
	}
	if again, err := g.OpenEvaluationBudget(ctx, "e_demo", 2.5, 30); err != nil || again != id || len(fg.created) != 1 {
		t.Fatalf("reopen made a second customer: %q %v %d", again, err, len(fg.created))
	}
	body := fg.created[0]
	if body["name"] != "heimdall-eval-e_demo" {
		t.Errorf("customer name %v", body["name"])
	}
	b := body["budgets"].([]any)[0].(map[string]any)
	if b["max_limit"] != 2.5 || b["reset_duration"] != budgetReset {
		t.Errorf("budget %v", b)
	}
	rl := body["rate_limit"].(map[string]any)
	if rl["request_max_limit"] != float64(30) || rl["request_reset_duration"] != "1m" {
		t.Errorf("rate limit %v", rl)
	}

	fg.usage = 1.0
	spent, limit, exhausted, err := g.EvaluationBudgetSpent(ctx, "e_demo")
	if err != nil || exhausted || spent != 1.0 || limit != 2.5 {
		t.Fatalf("spent %v %v %v %v", spent, limit, exhausted, err)
	}
	fg.usage = 2.5
	if _, _, exhausted, _ = g.EvaluationBudgetSpent(ctx, "e_demo"); !exhausted {
		t.Fatal("a used up budget was not reported")
	}
	if err := g.CloseEvaluationBudget(ctx, "e_demo"); err != nil {
		t.Fatal(err)
	}
	if len(fg.deleted) != 1 || fg.deleted[0] != "cust-1" {
		t.Fatalf("deleted %v", fg.deleted)
	}
	// Closed: nothing open, nothing more deleted, reads report zero.
	if err := g.CloseEvaluationBudget(ctx, "e_demo"); err != nil || len(fg.deleted) != 1 {
		t.Fatalf("second close: %v %v", err, fg.deleted)
	}
	if _, _, exhausted, err := g.EvaluationBudgetSpent(ctx, "e_demo"); err != nil || exhausted {
		t.Fatalf("closed budget read: %v %v", exhausted, err)
	}
}

func TestEvaluationBudgetCloseGoneCustomer(t *testing.T) {
	fg := &fakeGovernance{}
	g, err := New(testConfig(fg.server(t).URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := g.OpenEvaluationBudget(ctx, "e_gone", 1, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := fg.created[0]["rate_limit"]; ok {
		t.Error("rate limit sent without requests_per_minute")
	}
	fg.exists = false // deleted by hand in the Bifrost UI
	if err := g.CloseEvaluationBudget(ctx, "e_gone"); err != nil {
		t.Fatalf("a customer already gone must count as closed: %v", err)
	}
}

// The run key of an evaluation with an open budget is minted under the
// evaluation's customer; other runs are not.
func TestMintKeyUnderEvaluationCustomer(t *testing.T) {
	f := newFakeBifrost(t)
	g, err := New(multiConfig(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	g.mu.Lock()
	g.evalCust = map[string]string{"e_capped": "cust-9"}
	g.mu.Unlock()
	rc := runContext("r_evalb0001", "gpt-4o-mini")
	rc.Agent.Gateway.Provider = "openai"
	rc.Run.ExpID = "e_capped"
	if _, err := g.MintKey(context.Background(), rc); err != nil {
		t.Fatalf("mint: %v", err)
	}
	if got := f.lastBody("POST /api/governance/virtual-keys")["customer_id"]; got != "cust-9" {
		t.Fatalf("customer_id %v", got)
	}
	other := runContext("r_evalb0002", "gpt-4o-mini")
	other.Agent.Gateway.Provider = "openai"
	other.Run.ExpID = "e_free"
	if _, err := g.MintKey(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if got, ok := f.lastBody("POST /api/governance/virtual-keys")["customer_id"]; ok {
		t.Fatalf("a run of another evaluation got customer_id %v", got)
	}
}

func TestIsBifrostBudgetStop(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   bool
	}{
		{402, "", true},
		{429, `{"type":"budget_exceeded","error":{"message":"x"}}`, true},
		{400, `{"error":{"message":"customer budget exceeded: 2.5000 >= 2.5000 dollars"}}`, true},
		{429, `{"type":"rate_limited"}`, false},
		{500, "upstream down", false},
	}
	for _, c := range cases {
		if got := IsBifrostBudgetStop(c.status, c.body); got != c.want {
			t.Errorf("IsBifrostBudgetStop(%d, %q) = %v", c.status, c.body, got)
		}
	}
	if !strings.Contains(BifrostBudgetStopMessage, "Bifrost budget") {
		t.Error(BifrostBudgetStopMessage)
	}
}
