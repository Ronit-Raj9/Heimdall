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

// fakeLangSmith speaks POST /api/v2/runs/query (cursor paging, newest
// first) and GET /api/v2/traces/{id}/runs, with the X-API-Key header, and
// answers 503 once to the trace read.
type fakeLangSmith struct {
	mu      sync.Mutex
	bodies  []map[string]any
	unavail bool
}

func lsRuns(traceID, request, toolErr string) []map[string]any {
	t0 := testNow.Add(-3 * time.Hour).Format(time.RFC3339Nano)
	root := map[string]any{"id": traceID, "trace_id": traceID, "name": "support-agent", "run_type": "CHAIN", "status": "ERROR", "is_root": true,
		"start_time": t0, "error": "agent gave up", "inputs": map[string]any{"input": request}, "outputs": map[string]any{}}
	llm := map[string]any{"id": traceID + "-llm", "trace_id": traceID, "name": "ChatOpenAI", "run_type": "LLM", "status": "SUCCESS",
		"start_time": t0, "parent_run_ids": []string{traceID},
		"inputs": map[string]any{"messages": []any{[]any{map[string]any{"lc": 1, "type": "constructor", "id": []any{"langchain", "schema", "messages", "HumanMessage"},
			"kwargs": map[string]any{"content": request}}}}}}
	tool := map[string]any{"id": traceID + "-tool", "trace_id": traceID, "name": "cancel_subscription", "run_type": "TOOL", "status": "ERROR",
		"start_time": t0, "parent_run_ids": []string{traceID}, "error": toolErr, "inputs": map[string]any{"input": map[string]any{"plan": "pro"}}}
	return []map[string]any{root, llm, tool}
}

func (f *fakeLangSmith) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "ls-key-123" || r.Header.Get("X-Tenant-Id") != "ws-1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/runs/query":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			f.mu.Lock()
			f.bodies = append(f.bodies, body)
			f.mu.Unlock()
			if ids, _ := body["project_ids"].([]any); len(ids) != 1 || ids[0] != "proj-1" || body["is_root"] != true {
				t.Errorf("query body = %v", body)
			}
			if body["has_error"] == true {
				if body["cursor"] == nil {
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": "ls-1", "trace_id": "ls-1", "status": "ERROR",
						"error": "403 forbidden", "start_time": testNow.Add(-3 * time.Hour).Format(time.RFC3339Nano)}}, "next_cursor": "c2"})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": "ls-2", "trace_id": "ls-2", "status": "ERROR",
					"error": "403 forbidden", "start_time": testNow.Add(-3 * time.Hour).Format(time.RFC3339Nano)}}, "next_cursor": nil})
				return
			}
			filter, _ := body["filter"].(string)
			if filter != `and(eq(feedback_key, "user_score"), lt(feedback_score, 0.5))` || body["min_feedback_modified_at"] == nil {
				t.Errorf("feedback query = %v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"id": "ls-3", "trace_id": "ls-3", "status": "SUCCESS",
				"start_time": testNow.Add(-time.Hour).Format(time.RFC3339Nano)}}})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/v2/traces/"):
			f.mu.Lock()
			first := !f.unavail
			f.unavail = true
			f.mu.Unlock()
			if first {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if sel := r.URL.Query()["selects"]; len(sel) < 5 || r.URL.Query().Get("project_id") != "proj-1" {
				t.Errorf("trace query = %s", r.URL.RawQuery)
			}
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v2/traces/"), "/runs")
			switch id {
			case "ls-1", "ls-2":
				_ = json.NewEncoder(w).Encode(map[string]any{"items": lsRuns(id, "Cancel my pro plan, I am "+plantedEmail, "403 forbidden: plan locked")})
			default:
				runs := lsRuns(id, "What is your refund policy?", "")
				runs = runs[:2]
				runs[0]["status"], runs[0]["error"] = "SUCCESS", ""
				runs[0]["outputs"] = map[string]any{"output": "Refunds take 90 days."}
				_ = json.NewEncoder(w).Encode(map[string]any{"items": runs})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func TestLangSmithPull(t *testing.T) {
	fake := &fakeLangSmith{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	cfg := &Config{Sources: []Source{{Name: "smith", Kind: KindLangSmith, Host: srv.URL, APIKeyEnv: "LS_KEY", Project: "proj-1", Workspace: "ws-1",
		Link:    "https://smith.example/o/org/projects/p/{project}/r/{trace_id}",
		Filters: Filters{Errors: true, Feedback: &FeedbackFilter{Name: "user_score", Below: ptr(0.5)}}}}}
	ib, w := newTestInbox(t, cfg, map[string]string{"LS_KEY": "ls-key-123"})
	res, err := ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Error != "" || res[0].Fetched != 3 || res[0].New != 2 || res[0].Repeats != 1 {
		t.Fatalf("pull = %+v", res[0])
	}
	if len(w.d) != 1 {
		t.Errorf("waits = %v, want one retry after the 503", w.d)
	}
	items := mustItems(t, ib)
	var cancel, policy *Item
	for i := range items {
		switch {
		case strings.Contains(items[i].Title, "Cancel my pro plan"):
			cancel = &items[i]
		case strings.Contains(items[i].Title, "refund policy"):
			policy = &items[i]
		}
	}
	if cancel == nil || policy == nil {
		t.Fatalf("items = %+v", items)
	}
	if cancel.Occurrences != 2 || cancel.SignatureParts.Tool != "cancel_subscription" || cancel.Outcome != "error" ||
		cancel.Link != "https://smith.example/o/org/projects/p/proj-1/r/ls-1" || cancel.SuggestedKind != "tool-use" {
		t.Errorf("cancel item = %+v", cancel)
	}
	if !strings.HasPrefix(policy.SignatureParts.Error, "flag:feedback") || policy.SuggestedKind != "qa" ||
		!strings.Contains(strings.Join(policy.Flagged, "|"), "negative user feedback: user_score below 0.5") {
		t.Errorf("feedback item = %+v", policy)
	}
	sess, err := ib.Store.Session(policy.ID)
	if err != nil || sess.FinalAnswer != "Refunds take 90 days." {
		t.Errorf("feedback session = %+v, %v", sess, err)
	}
	checkNoLeak(t, ib.Store.(*FileStore).Dir())
	if len(fake.bodies) != 3 {
		t.Errorf("queries = %d, want 2 error pages and 1 feedback page", len(fake.bodies))
	}
}
