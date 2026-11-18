package traceinbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// bifrostFixture is the gateway's log rows as GET /api/logs lists them and
// GET /api/logs/{id} details them (fields from framework/logstore/tables.go).
func bifrostFixture() (list []map[string]any, detail map[string]map[string]any) {
	ts := func(m int) string { return testNow.Add(-time.Duration(m) * time.Minute).Format(time.RFC3339Nano) }
	sess := "sess-7"
	list = []map[string]any{
		{"id": "r-ok", "timestamp": ts(50), "status": "success", "provider": "openai", "model": "gpt-x", "number_of_retries": 0, "fallback_index": 0},
		{"id": "r-err1", "timestamp": ts(40), "status": "error", "provider": "openai", "model": "gpt-x", "number_of_retries": 0, "fallback_index": 0, "session_id": sess},
		{"id": "r-err2", "timestamp": ts(39), "status": "error", "provider": "openai", "model": "gpt-x", "number_of_retries": 0, "fallback_index": 0, "session_id": sess},
		{"id": "r-retry", "timestamp": ts(30), "status": "success", "provider": "anthropic", "model": "claude-x", "number_of_retries": 3, "fallback_index": 0},
		{"id": "r-fb", "timestamp": ts(20), "status": "success", "provider": "groq", "model": "llama-x", "number_of_retries": 0, "fallback_index": 1},
		{"id": "r-budget", "timestamp": ts(10), "status": "error", "provider": "openai", "model": "gpt-x", "number_of_retries": 0, "fallback_index": 0},
	}
	msg := func(role, content string) map[string]any { return map[string]any{"role": role, "content": content} }
	errDetails := func(typ string, status int, message string) map[string]any {
		return map[string]any{"type": typ, "status_code": status, "is_bifrost_error": false, "error": map[string]any{"message": message}}
	}
	detail = map[string]map[string]any{
		"r-err2": {"id": "r-err2", "status": "error", "provider": "openai", "model": "gpt-x", "session_id": sess,
			"input_history": []any{msg("user", "Summarize ticket 4411 for "+plantedEmail), msg("assistant", "Working on it."), msg("user", "And the attachments?")},
			"error_details": errDetails("server_error", 500, "upstream connect error")},
		"r-retry": {"id": "r-retry", "status": "success", "provider": "anthropic", "model": "claude-x",
			"input_history": []any{msg("user", "Translate this contract clause")}, "output_message": msg("assistant", "Voici la clause.")},
		"r-fb": {"id": "r-fb", "status": "success", "provider": "groq", "model": "llama-x",
			"input_history": []any{msg("user", "Plan my trip to Lisbon")}, "output_message": msg("assistant", "Day 1: Alfama.")},
		"r-budget": {"id": "r-budget", "status": "error", "provider": "openai", "model": "gpt-x",
			"input_history": []any{msg("user", "Draft the quarterly report")},
			"error_details": errDetails("budget_exceeded", 402, "Budget exceeded: virtual key budget exceeded: 10.0000 >= 10.0000 dollars")},
	}
	return list, detail
}

func fakeBifrost(t *testing.T) *httptest.Server {
	list, detail := bifrostFixture()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(bifrostTokenHdr) != "bf-token-9" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/logs" {
			q := r.URL.Query()
			if q.Get("sort_by") != "timestamp" || q.Get("order") != "asc" || q.Get("start_time") == "" || q.Get("virtual_key_ids") != "vk-prod" {
				t.Errorf("list query = %s", r.URL.RawQuery)
			}
			offset, _ := strconv.Atoi(q.Get("offset"))
			limit, _ := strconv.Atoi(q.Get("limit"))
			var rows []map[string]any
			for _, row := range list {
				if s := q.Get("status"); s != "" && row["status"] != s {
					continue
				}
				rows = append(rows, row)
			}
			end := min(len(rows), offset+limit)
			page := []map[string]any{}
			if offset < len(rows) {
				page = rows[offset:end]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"logs": page})
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/api/logs/")
		d, ok := detail[id]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(d)
	}))
}

func TestBifrostPull(t *testing.T) {
	srv := fakeBifrost(t)
	defer srv.Close()
	cfg := &Config{Sources: []Source{{Name: "gateway", Kind: KindBifrost, URL: srv.URL, TokenEnv: "BF_TOKEN", VirtualKeys: []string{"vk-prod"},
		Filters: Filters{Errors: true, MinRetries: 2, Fallbacks: true, Budget: true}}}}
	ib, _ := newTestInbox(t, cfg, map[string]string{"BF_TOKEN": "bf-token-9"})
	// Shrink the page so paging is exercised.
	res, err := ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Error != "" || res[0].Fetched != 4 || res[0].New != 4 {
		t.Fatalf("pull = %+v", res[0])
	}
	byTitle := map[string]Item{}
	for _, it := range mustItems(t, ib) {
		byTitle[it.Title] = it
	}
	sess := byTitle["Summarize ticket <n> for [REDACTED email]"]
	if sess.ID == "" {
		// The title keeps the redacted text, not the normalized one.
		for title, it := range byTitle {
			if strings.HasPrefix(title, "Summarize ticket") {
				sess = it
			}
		}
	}
	if sess.TraceID != "sess-7" || !strings.Contains(sess.Link, "/workspace/logs?selected_log=r-err2") ||
		!strings.Contains(strings.Join(sess.Flagged, "|"), "failed (status 500): upstream connect error") {
		t.Errorf("session item = %+v", sess)
	}
	kinds := map[string]bool{}
	for title, it := range byTitle {
		switch {
		case strings.HasPrefix(title, "Translate"):
			kinds["retries"] = strings.Contains(strings.Join(it.Flagged, "|"), "retried 3 times") && it.SignatureParts.Error == "flag:retries"
		case strings.HasPrefix(title, "Plan my trip"):
			kinds["fallback"] = strings.Contains(strings.Join(it.Flagged, "|"), "served by fallback 1 (groq/llama-x)")
		case strings.HasPrefix(title, "Draft the quarterly"):
			kinds["budget"] = strings.Contains(strings.Join(it.Flagged, "|"), "budget exceeded") && it.Outcome == "error"
		}
	}
	if !kinds["retries"] || !kinds["fallback"] || !kinds["budget"] {
		t.Errorf("flags = %v, items %+v", kinds, byTitle)
	}
	checkNoLeak(t, ib.Store.(*FileStore).Dir())
}

func TestBifrostBudgetOnly(t *testing.T) {
	srv := fakeBifrost(t)
	defer srv.Close()
	cfg := &Config{Sources: []Source{{Name: "gateway", Kind: KindBifrost, URL: srv.URL, TokenEnv: "BF_TOKEN", VirtualKeys: []string{"vk-prod"},
		Filters: Filters{Budget: true}}}}
	ib, _ := newTestInbox(t, cfg, map[string]string{"BF_TOKEN": "bf-token-9"})
	res, err := ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	items := mustItems(t, ib)
	if res[0].New != 1 || len(items) != 1 || !strings.HasPrefix(items[0].Title, "Draft the quarterly") {
		t.Fatalf("budget only: pull %+v, items %+v", res[0], items)
	}
}

// TestCappedPullProgresses proves a pull stopped by max_per_pull holds the
// cursor and the next pull takes the next sessions, never the same ones.
func TestCappedPullProgresses(t *testing.T) {
	srv := fakeBifrost(t)
	defer srv.Close()
	cfg := &Config{Sources: []Source{{Name: "gateway", Kind: KindBifrost, URL: srv.URL, TokenEnv: "BF_TOKEN", VirtualKeys: []string{"vk-prod"}, MaxPerPull: 1,
		Filters: Filters{Errors: true, MinRetries: 2, Fallbacks: true, Budget: true}}}}
	ib, _ := newTestInbox(t, cfg, map[string]string{"BF_TOKEN": "bf-token-9"})
	var truncated []bool
	for i := 0; i < 5; i++ {
		res, err := ib.Pull(context.Background(), PullOptions{})
		if err != nil {
			t.Fatal(err)
		}
		truncated = append(truncated, res[0].Truncated)
		curs, _ := ib.Store.Cursors()
		if from := curs["gateway"].Since.Add(-DefaultOverlap); res[0].Truncated && from.After(testNow.Add(-10*time.Minute)) {
			t.Errorf("pull %d was capped but the next window starts at %s, after the rows not taken", i, from)
		}
	}
	if n := len(mustItems(t, ib)); n != 4 {
		t.Errorf("items after capped pulls = %d, want 4 (truncated %v)", n, truncated)
	}
	if !truncated[0] || truncated[4] {
		t.Errorf("truncated = %v", truncated)
	}
}
