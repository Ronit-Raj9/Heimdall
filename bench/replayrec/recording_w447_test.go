package replayrec

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func strp(s string) *string { return &s }

var t0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// toolSession is two calls: the first answers with a tool call, the second
// request carries the tool result.
func toolSession(result string) []Row {
	req0 := `{"model":"openai/gpt-4o","messages":[{"role":"system","content":"You help."},{"role":"user","content":"Weather in Paris?"}],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`
	resp0 := `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"unit\":\"c\",\"city\":\"Paris\"}"}}]},"finish_reason":"tool_calls"}]}`
	res, _ := json.Marshal(result)
	req1 := `{"model":"openai/gpt-4o","messages":[{"role":"system","content":"You help."},{"role":"user","content":"Weather in Paris?"},` +
		`{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{\"unit\":\"c\",\"city\":\"Paris\"}"}}]},` +
		`{"role":"tool","tool_call_id":"c1","content":` + string(res) + `}],"tools":[{"type":"function","function":{"name":"get_weather"}}]}`
	resp1 := `{"choices":[{"message":{"role":"assistant","content":"It is 12 C in Paris."},"finish_reason":"stop"}]}`
	return []Row{
		{ID: "r2", Timestamp: t0.Add(time.Second), Status: "success", Provider: "openai", Model: "gpt-4o", SessionID: strp("s1"), RawRequest: req1, RawResponse: resp1},
		{ID: "r1", Timestamp: t0, Status: "success", Provider: "openai", Model: "gpt-4o", SessionID: strp("s1"), RawRequest: req0, RawResponse: resp0},
	}
}

func TestBuildPairsToolResultsByID(t *testing.T) {
	rec, err := Build("prod", toolSession("12 C, cloudy"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Calls) != 2 || rec.Calls[0].RowID != "r1" {
		t.Fatalf("calls not in time order: %+v", rec.Calls)
	}
	if len(rec.Tools) != 1 {
		t.Fatalf("tools = %+v", rec.Tools)
	}
	tr := rec.Tools[0]
	if tr.CallID != "c1" || tr.Name != "get_weather" || tr.AskedAt != 0 || tr.SeenAt != 1 || tr.Content != "12 C, cloudy" {
		t.Fatalf("tool result = %+v", tr)
	}
	tc := rec.Calls[0].Answer.ToolCalls[0]
	if tc.Arguments != `{"city":"Paris","unit":"c"}` || tc.ArgsHash != tr.ArgsHash || len(tc.ArgsHash) != 16 {
		t.Fatalf("tool call = %+v", tc)
	}
	if !rec.Calls[0].ToolsDefined || rec.Calls[0].RequestFrom != "raw_request" {
		t.Fatalf("call 0 = %+v", rec.Calls[0])
	}
	if strings.Contains(string(rec.Calls[0].Request), `"model"`) {
		t.Fatalf("model field kept: %s", rec.Calls[0].Request)
	}
	if rec.Calls[1].Answer.Text != "It is 12 C in Paris." || rec.Calls[1].Answer.Finish != "stop" {
		t.Fatalf("answer 1 = %+v", rec.Calls[1].Answer)
	}
	if !strings.HasPrefix(rec.ID, "rec_") || len(rec.ID) != 16 || rec.SessionID != "s1" || rec.Version != FormatVersion {
		t.Fatalf("recording head = %s %s %d", rec.ID, rec.SessionID, rec.Version)
	}
}

func TestBuildDropsFallbackRows(t *testing.T) {
	rows := toolSession("ok")
	fb := rows[1]
	fb.ID, fb.FallbackIndex, fb.Provider = "r1b", 1, "anthropic"
	rows = append(rows, fb)
	rec, err := Build("prod", rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Calls) != 2 {
		t.Fatalf("fallback row kept: %d calls", len(rec.Calls))
	}
	for _, c := range rec.Calls {
		if c.RowID == "r1b" {
			t.Fatal("fallback row kept")
		}
	}
}

func TestBuildMarksMissingToolDefinitions(t *testing.T) {
	hist := []json.RawMessage{json.RawMessage(`{"role":"user","content":"Weather?"}`)}
	out := json.RawMessage(`{"role":"assistant","content":"","tool_calls":[{"id":"c1","type":"function","function":{"name":"get_weather","arguments":"{}"}}]}`)
	rec, err := Build("prod", []Row{{ID: "r1", Timestamp: t0, Status: "success", SessionID: strp("s1"), InputHistory: hist, OutputMessage: out}})
	if err != nil {
		t.Fatal(err)
	}
	c := rec.Calls[0]
	if c.ToolsDefined || c.RequestFrom != "input_history" || len(c.Answer.ToolCalls) != 1 {
		t.Fatalf("call = %+v", c)
	}
	if len(rec.Gaps) == 0 || !strings.Contains(rec.Gaps[0], "no tool definitions") {
		t.Fatalf("gaps = %v", rec.Gaps)
	}
}

func TestBuildRedacts(t *testing.T) {
	rec, err := Build("prod", toolSession("key sk-abcdefghijklmnopqrstuvwxyz123456 owner jane.doe@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	got := rec.Tools[0].Content
	if strings.Contains(got, "sk-abc") || strings.Contains(got, "jane.doe") || !strings.Contains(got, "[REDACTED ") {
		t.Fatalf("tool result not redacted: %q", got)
	}
	total := 0
	for _, n := range rec.Redacted {
		total += n
	}
	if total != 2 {
		t.Fatalf("redacted counts = %v", rec.Redacted)
	}
	if rec.Calls[0].HasRedaction || !rec.Calls[1].HasRedaction {
		t.Fatalf("HasRedaction = %v, %v", rec.Calls[0].HasRedaction, rec.Calls[1].HasRedaction)
	}
	if strings.Contains(string(rec.Calls[1].Request), "jane.doe") {
		t.Fatal("request not redacted")
	}
}

func TestTapeStrict(t *testing.T) {
	rec, err := Build("prod", toolSession("12 C"))
	if err != nil {
		t.Fatal(err)
	}
	args, err := Canonical(`{"unit": "c", "city": "Paris"}`)
	if err != nil {
		t.Fatal(err)
	}
	tape := NewTape(rec)
	if _, ok := tape.Answer("get_weather", mustCanon(t, `{"city":"Rome","unit":"c"}`)); ok {
		t.Fatal("a changed value was answered")
	}
	if _, ok := tape.Answer("get_forecast", args); ok {
		t.Fatal("another tool name was answered")
	}
	res, ok := tape.Answer("get_weather", args)
	if !ok || res.Content != "12 C" {
		t.Fatalf("same call with other key order: %+v %v", res, ok)
	}
	if _, ok := tape.Answer("get_weather", args); ok {
		t.Fatal("a recorded result was used twice")
	}
	if _, err := Canonical("{not json"); err == nil {
		t.Fatal("invalid JSON accepted")
	}
}

func mustCanon(t *testing.T, s string) string {
	t.Helper()
	c, err := Canonical(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestWriteToolWords(t *testing.T) {
	for name, want := range map[string]bool{
		"create_ticket": true, "sendEmail": true, "delete-user": true, "issue.refund": true, "RefundOrder": true,
		"get_weather": false, "listUsers": false, "search": false, "read_file": false, "": false, "executeQuery": true,
		"settings_get": false, "set_flag": true,
	} {
		if got := WriteTool(name); got != want {
			t.Errorf("WriteTool(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSessionsSampling(t *testing.T) {
	var logs []map[string]any
	for i := 0; i < 1000; i++ {
		logs = append(logs, map[string]any{"id": fmt.Sprintf("row%d", i), "timestamp": t0.Add(time.Duration(i) * time.Second), "status": "success",
			"model": "gpt-4o", "session_id": fmt.Sprintf("sess-%d", i)})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Bifrost-Setup-Token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var off, lim int
		fmt.Sscan(r.URL.Query().Get("offset"), &off)
		fmt.Sscan(r.URL.Query().Get("limit"), &lim)
		end := min(off+lim, len(logs))
		page := []map[string]any{}
		if off < len(logs) {
			page = logs[off:end]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": page})
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, Token: "tok", HTTP: srv.Client()}
	q := Query{Since: t0, Until: t0.Add(time.Hour), Max: 200000, SampleRate: 0.5}
	a, err := c.Sessions(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) < 450 || len(a) > 550 {
		t.Fatalf("kept %d of 1000", len(a))
	}
	b, err := c.Sessions(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != len(b) {
		t.Fatalf("not stable: %d then %d", len(a), len(b))
	}
	for i := range a {
		if a[i].SessionID != b[i].SessionID {
			t.Fatalf("not stable at %d", i)
		}
	}
}

func TestClientSessionBuilds(t *testing.T) {
	rows := toolSession("12 C")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/logs":
			_ = json.NewEncoder(w).Encode(map[string]any{"logs": []map[string]any{
				{"id": "r1", "timestamp": t0, "status": "success", "session_id": "s1"},
				{"id": "x9", "timestamp": t0, "status": "success", "session_id": "other"},
				{"id": "r2", "timestamp": t0.Add(time.Second), "status": "success", "session_id": "s1"},
			}})
		case r.URL.Path == "/api/logs/r1":
			_ = json.NewEncoder(w).Encode(rows[1])
		case r.URL.Path == "/api/logs/r2":
			_ = json.NewEncoder(w).Encode(rows[0])
		default:
			t.Errorf("unexpected fetch %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	rec, err := c.Session(context.Background(), "prod", "s1", t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Calls) != 2 || len(rec.Tools) != 1 {
		t.Fatalf("recording = %d calls, %d tools", len(rec.Calls), len(rec.Tools))
	}
}
