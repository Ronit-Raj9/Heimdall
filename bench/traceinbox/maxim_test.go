package traceinbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeMaxim speaks POST /v1/log-repositories/logs/search (page paging) and
// GET /v1/log-repositories/logs/traces with the x-maxim-api-key header.
type fakeMaxim struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (f *fakeMaxim) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Maxim-Api-Key") != "mx-key-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		at := testNow.Add(-2 * time.Hour).Format(time.RFC3339)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/log-repositories/logs/search":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
			}
			f.mu.Lock()
			f.bodies = append(f.bodies, body)
			f.mu.Unlock()
			if body["workspaceId"] != "ws1" || body["id"] != "repo1" || body["type"] != "trace" {
				t.Errorf("search body %v", body)
			}
			var logs []any
			if _, ok := body["traceFilters"]; ok {
				logs = []any{map[string]any{"id": "mx-1", "name": "support", "startTimestamp": at}}
			} else {
				ef, _ := body["evaluatorFilters"].(map[string]any)
				fs, _ := ef["filters"].([]any)
				if len(fs) != 1 || fs[0].(map[string]any)["value"] != "fail" || fs[0].(map[string]any)["evaluatorName"] != "Task Success" {
					t.Errorf("evaluator filter %v", ef)
				}
				logs = []any{map[string]any{"id": "mx-1", "startTimestamp": at}, map[string]any{"id": "mx-2", "startTimestamp": at,
					"evaluationData": []any{map[string]any{"evaluatorName": "Task Success", "pass": false, "score": 0, "reasoning": "refund not issued"}}}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"logs": logs}, "pagination": map[string]any{"total": len(logs), "page": 0, "limit": 50}})
		case r.Method == http.MethodGet && r.URL.Path == "/v1/log-repositories/logs/traces":
			id := r.URL.Query().Get("id")
			trace := map[string]any{"id": id, "name": "support", "input": "Refund order A1 for " + plantedEmail,
				"timeline": []any{
					map[string]any{"id": "g1", "type": "generation", "model": "gpt-5.4-mini", "messages": []any{map[string]any{"role": "user", "content": "Refund order A1 please"}}},
					map[string]any{"id": "t1", "type": "tool_call", "name": "refund_order", "args": map[string]any{"order": "A1"}, "status": "error", "error": "refund service down"},
				},
				"output": "Sorry, I could not refund it."}
			if id == "mx-2" {
				trace["timeline"] = []any{}
				trace["input"] = "Where is my package?"
				trace["output"] = "It shipped."
				trace["evaluationData"] = []any{map[string]any{"evaluatorName": "Task Success", "pass": false}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": trace})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestMaximPull(t *testing.T) {
	fake := &fakeMaxim{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	cfg := &Config{Sources: []Source{{Name: "maxim-prod", Kind: KindMaxim, Host: srv.URL, APIKeyEnv: "MX_KEY", Workspace: "ws1", Project: "repo1",
		Filters: Filters{Errors: true, Evaluators: []string{"Task Success"}}}}}
	ib, _ := newTestInbox(t, cfg, map[string]string{"MX_KEY": "mx-key-1"})
	res, err := ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Error != "" || res[0].Fetched != 2 || res[0].New != 2 {
		t.Fatalf("pull = %+v", res[0])
	}
	if len(fake.bodies) != 2 {
		t.Fatalf("searches = %d", len(fake.bodies))
	}
	items := mustItems(t, ib)
	var refund, pkg *Item
	for i := range items {
		switch {
		case strings.Contains(items[i].Title, "Refund order"):
			refund = &items[i]
		case strings.Contains(items[i].Title, "package"):
			pkg = &items[i]
		}
	}
	if refund == nil || pkg == nil {
		t.Fatalf("items = %+v", items)
	}
	if refund.SignatureParts.Tool != "refund_order" || refund.Outcome != "error" ||
		refund.Link != "https://app.getmaxim.ai/workspace/ws1/log-repository/repo1/logs?traceId=mx-1" {
		t.Errorf("refund item = %+v", refund)
	}
	if !strings.Contains(strings.Join(pkg.Flagged, "|"), "Maxim evaluator Task Success failed: refund not issued") {
		t.Errorf("evaluator item flags = %v", pkg.Flagged)
	}
	checkNoLeak(t, ib.ProjectRoot)
	// A second pull skips traces it already took.
	res, err = ib.Pull(context.Background(), PullOptions{})
	if err != nil || res[0].New != 0 {
		t.Fatalf("second pull = %+v %v", res, err)
	}
}

func TestMaximSourceValidate(t *testing.T) {
	for name, s := range map[string]Source{
		"no key":       {Name: "m", Kind: KindMaxim, Workspace: "w", Project: "r", Filters: Filters{Errors: true}},
		"no workspace": {Name: "m", Kind: KindMaxim, APIKeyEnv: "K", Project: "r", Filters: Filters{Errors: true}},
		"no filter":    {Name: "m", Kind: KindMaxim, APIKeyEnv: "K", Workspace: "w", Project: "r"},
		"empty eval":   {Name: "m", Kind: KindMaxim, APIKeyEnv: "K", Workspace: "w", Project: "r", Filters: Filters{Evaluators: []string{" "}}},
		"foreign":      {Name: "m", Kind: KindMaxim, APIKeyEnv: "K", Workspace: "w", Project: "r", Filters: Filters{Errors: true, LevelError: true}},
		"evals on ls":  {Name: "m", Kind: KindLangSmith, Host: "https://x.example", APIKeyEnv: "K", Project: "p", Filters: Filters{Errors: true, Evaluators: []string{"x"}}},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	ok := Source{Name: "m", Kind: KindMaxim, APIKeyEnv: "K", Workspace: "w", Project: "r", Filters: Filters{Evaluators: []string{"Task Success"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
}
