package bifrost

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeAgentGateway serves the agent gateway's management API the way
// Bifrost v2.5.1 answered it (docs/a2a-agents.md), including a history
// filter it ignores.
type fakeAgentGateway struct {
	mu        sync.Mutex
	created   []map[string]any
	deleted   []string
	tokens    []string
	historyQs []string
}

func (f *fakeAgentGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.tokens = append(f.tokens, r.Header.Get(setupTokenHeader))
	f.mu.Unlock()
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/api/agents":
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.created = append(f.created, body)
		f.mu.Unlock()
		writeJSONT(w, http.StatusCreated, body)
	case r.Method == http.MethodDelete && r.URL.Path == "/api/agents/gone":
		writeJSONT(w, http.StatusNotFound, map[string]string{"error": "not found"})
	case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/agents/"):
		f.mu.Lock()
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/api/agents/"))
		f.mu.Unlock()
		writeJSONT(w, http.StatusOK, map[string]string{"message": "deleted"})
	case r.URL.Path == "/api/agents/history":
		f.mu.Lock()
		f.historyQs = append(f.historyQs, r.URL.RawQuery)
		f.mu.Unlock()
		writeJSONT(w, http.StatusOK, map[string]any{"logs": []map[string]any{
			{"id": "op1", "record_kind": "request", "operation": "SendMessage", "status": "success", "agent_name": "hb-r-1-lead", "task_id": "t1", "latency": 12.5},
			{"id": "ev1", "record_kind": "event", "operation": "SendStreamingMessage", "status": "success", "agent_name": "hb-r-1-lead", "task_id": "t1", "task_state": "TASK_STATE_WORKING"},
			{"id": "op9", "record_kind": "request", "operation": "SendMessage", "status": "error", "agent_name": "someone-else", "error_details": map[string]any{"error": map[string]string{"message": "nope"}}},
		}})
	case r.URL.Path == "/api/agents/history/op1":
		writeJSONT(w, http.StatusOK, map[string]any{"id": "op1", "request_body": `{"message":{}}`, "response_body": `{"id":"t1","status":{"state":"TASK_STATE_COMPLETED"}}`})
	case r.URL.Path == "/api/logs" || r.URL.Path == "/api/mcp-logs":
		task := r.URL.Query().Get("agent_correlation_id")
		writeJSONT(w, http.StatusOK, map[string]any{"logs": []map[string]any{
			{"id": r.URL.Path + "-row", "agent_correlation_id": task, "virtual_key_id": "vk-1"},
			{"id": "unrelated", "agent_correlation_id": "other-task", "virtual_key_id": "vk-1"},
		}})
	default:
		writeJSONT(w, http.StatusNotFound, map[string]string{"error": r.URL.Path})
	}
}

func TestA2AManagement(t *testing.T) {
	f := &fakeAgentGateway{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	g, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := g.RegisterA2A(ctx, schemas.A2ARegistrationSpec{Name: "hb-r-1-lead", CardURL: "http://a/card", VirtualKeyIDs: []string{"vk-1"}, Forward: true}); err != nil {
		t.Fatal(err)
	}
	if err := g.RegisterA2A(ctx, schemas.A2ARegistrationSpec{Name: "hb-r-1-writer", CardURL: "http://w/card", VirtualKeyIDs: []string{"vk-1"}, Forward: true, RuntimeKey: "member-secret"}); err != nil {
		t.Fatal(err)
	}
	lead, writer := f.created[0], f.created[1]
	if lead["forward_accepted_credential"] != true || lead["allow_by_default"] != false || lead["runtime_auth"] != nil {
		t.Fatalf("lead registration %v", lead)
	}
	auth, _ := writer["runtime_auth"].(map[string]any)
	headers, _ := auth["headers"].(map[string]any)
	if writer["forward_accepted_credential"] != false || headers["x-bf-vk"] != "member-secret" {
		t.Fatalf("a member with a runtime key gets it as x-bf-vk and no forwarding: %v", writer)
	}
	if err := g.DeleteA2A(ctx, "hb-r-1-lead"); err != nil || g.DeleteA2A(ctx, "gone") != nil || len(f.deleted) != 1 {
		t.Fatalf("delete: %v %v", err, f.deleted)
	}
	since := time.Date(2026, 11, 18, 8, 0, 0, 0, time.UTC)
	rows, err := g.A2AHistory(ctx, []string{"hb-r-1-lead"}, since)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ResponseBody == "" || rows[1].ResponseBody != "" {
		t.Fatalf("rows of other registrations are dropped and only request rows are read again: %+v", rows)
	}
	if !strings.Contains(f.historyQs[0], "agent_name=hb-r-1-lead") || !strings.Contains(f.historyQs[0], "order=asc") {
		t.Fatalf("history query %q", f.historyQs[0])
	}
	linked, err := g.A2ACorrelated(ctx, []string{"t1"}, since)
	if err != nil || len(linked) != 2 || linked[0].Kind != "llm" || linked[1].Kind != "mcp" {
		t.Fatalf("rows whose correlation id is not the task are dropped: %+v %v", linked, err)
	}
	for _, tok := range f.tokens {
		if tok != testToken {
			t.Fatalf("every management call carries the setup token, got %q", tok)
		}
	}
}
