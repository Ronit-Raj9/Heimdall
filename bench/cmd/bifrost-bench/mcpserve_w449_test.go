package main

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

// w449Fake answers the API routes the investigation tools read and records
// the query of each request.
type w449Fake struct {
	mu      sync.Mutex
	queries map[string]string
	routes  map[string]string
}

func (f *w449Fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.queries[r.URL.Path] = r.URL.RawQuery
	body, ok := f.routes[r.URL.Path]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `{"error":{"message":"route not found","type":"not_found"}}`)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func w449Run(t *testing.T, routes map[string]string, tool, args string) (string, *w449Fake) {
	t.Helper()
	f := &w449Fake{queries: map[string]string{}, routes: routes}
	hs := httptest.NewServer(f)
	defer hs.Close()
	srv := &mcpServer{api: &heimdallAPI{base: hs.URL, client: &http.Client{Timeout: 5 * time.Second}}}
	v, err := mcpToolTable[tool].run(context.Background(), srv, json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	return v.(string), f
}

func TestMCPListStepsCapsLimit(t *testing.T) {
	long := strings.Repeat("s", 1000)
	out, f := w449Run(t, map[string]string{"/api/v1/runs/r_1/steps/page": `{"run_id":"r_1","steps":[{"seq":1,"kind":"command","summary":"` + long + `"}],"next_after_seq":null,"total":1}`},
		"list_steps", `{"run_id":"r_1","limit":5000,"kind":"command"}`)
	if q := f.queries["/api/v1/runs/r_1/steps/page"]; !strings.Contains(q, "limit=200") || !strings.Contains(q, "kind=command") {
		t.Fatalf("query %q", q)
	}
	if strings.Contains(out, long) || !strings.Contains(out, strings.Repeat("s", 300)+"...") {
		t.Fatal("summary not cut at 300")
	}
	if !mcpToolTable["list_steps"].readOnly {
		t.Fatal("list_steps not read only")
	}
}

func TestMCPCompareCommandsFirstDifference(t *testing.T) {
	a := []schemas.StepRecord{{Seq: 1, Kind: "command", Summary: "ls  -la"}, {Seq: 2, Kind: "command", Summary: "pytest -k 12"}, {Seq: 3, Kind: "command", Summary: "git diff"}}
	b := []schemas.StepRecord{{Seq: 5, Kind: "command", Summary: "LS -la"}, {Seq: 6, Kind: "tool_request", ToolName: "bash", Summary: "cat setup.py"}, {Seq: 7, Kind: "command", Summary: "pytest -k 99"},
		{Seq: 8, Kind: "tool_request", ToolName: "read_file", Summary: "not a command"}}
	got := mcpCompareCommands(a, b, 100)
	rows := got["rows"].([]mcpCompareRow)
	if len(rows) != 4 || rows[0].Side != "both" || rows[1].Side != "only_b" || rows[1].Command != "cat setup.py" || rows[2].Command != "pytest -k #" || rows[3].Side != "only_a" {
		b, _ := json.Marshal(rows)
		t.Fatalf("rows %s", b)
	}
	if fd := got["first_difference"].(*int); fd == nil || *fd != 1 {
		t.Fatalf("first_difference %v", got["first_difference"])
	}
	if same := mcpCompareCommands(a, a, 100); same["first_difference"].(*int) != nil {
		t.Fatal("identical runs differ")
	}
}

func TestMCPGetConversationNotKept(t *testing.T) {
	out, _ := w449Run(t, map[string]string{
		"/api/v1/runs/r_1/calls":    `{"run_id":"r_1","calls":[{"call":1,"request_id":"q1","status":200},{"call":2,"request_id":"q2","status":200}]}`,
		"/api/v1/runs/r_1/calls/q1": `{"run_id":"r_1","request_id":"q1","model":"m","status":200,"request":"","response":"","available":false}`,
		"/api/v1/runs/r_1/calls/q2": `{"run_id":"r_1","request_id":"q2","model":"m","status":200,"available":true,` +
			`"request":"{\"messages\":[{\"role\":\"system\",\"content\":\"x\"},{\"role\":\"user\",\"content\":\"fix the bug\"}]}",` +
			`"response":"{\"choices\":[{\"message\":{\"content\":\"done\",\"tool_calls\":[{\"function\":{\"name\":\"bash\",\"arguments\":\"{\\\"cmd\\\":\\\"ls\\\"}\"}}]}}]}"}`,
	}, "get_conversation", `{"run_id":"r_1"}`)
	var got struct {
		Calls []mcpConversationCall `json:"calls"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Calls) != 2 || got.Calls[0].Kept || got.Calls[0].Note != "The gateway kept no text for this call." {
		t.Fatalf("calls %+v", got.Calls)
	}
	if c := got.Calls[1]; !c.Kept || c.LastMessage != "user: fix the bug" || !strings.Contains(c.Response, "done") || !strings.Contains(c.Response, "tool call bash") {
		t.Fatalf("kept call %+v", c)
	}
}

func TestMCPTodaysFailuresWindow(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	old, recent := now.Add(-30*time.Hour), now.Add(-2*time.Hour)
	items := []schemas.InboxItem{
		{ID: "a", State: "new", LastSeen: recent, Title: "recent new"},
		{ID: "b", State: "new", LastSeen: old},
		{ID: "c", State: "accepted", LastSeen: recent, LastRecurrence: &recent, Task: &schemas.InboxTaskLink{Suite: "s", ID: "t"}},
		{ID: "d", State: "accepted", LastSeen: recent},
		{ID: "e", State: "rejected", LastSeen: recent},
	}
	got := mcpFailuresSince(items, now.Add(-24*time.Hour), 20)
	l := got["items"].([]mcpFailure)
	if len(l) != 2 || l[0].ID != "a" || l[1].ID != "c" || l[1].Task != "s/t" {
		t.Fatalf("items %+v", l)
	}
	if got := mcpFailuresSince(items, now.Add(-48*time.Hour), 1); len(got["items"].([]mcpFailure)) != 1 || got["total"] != 3 {
		t.Fatalf("limit %+v", got)
	}
}

func TestMCPToolsCut64K(t *testing.T) {
	big := strings.Repeat("p", 200<<10)
	out, _ := w449Run(t, map[string]string{"/api/v1/runs/r_1/step/3": `{"step":{"seq":3,"summary":"` + big + `"}}`}, "get_step", `{"run_id":"r_1","seq":3}`)
	if len(out) > mcpInvestigateCap+100 || !strings.HasSuffix(out, "bytes; ask for a smaller page") || !strings.Contains(out, "cut: 65536 of ") {
		t.Fatalf("not cut: %d bytes, tail %q", len(out), out[len(out)-80:])
	}
	for _, name := range []string{"list_steps", "get_step", "list_agents_in_run", "get_conversation", "get_retries", "compare_commands", "cost_per_task", "todays_failures", "get_inbox_item"} {
		if tool, ok := mcpToolTable[name]; !ok || !tool.readOnly || tool.description == "" {
			t.Fatalf("tool %s missing or not read only", name)
		}
	}
}
