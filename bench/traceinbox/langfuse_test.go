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

// fakeLangfuse speaks the part of Langfuse's public API the source reads:
// v3 scores and v2 observations, both with cursor paging, basic auth, and
// one 429 before the first scores page.
type fakeLangfuse struct {
	mu       sync.Mutex
	queries  []string
	limited  bool
	scorePgs int
}

func lfObs(traceID, id, typ, name, level, status string, start time.Time, input, output any) map[string]any {
	enc := func(v any) any {
		if v == nil {
			return nil
		}
		b, _ := json.Marshal(v)
		return string(b) // the v2 API returns input and output as raw strings
	}
	return map[string]any{"id": id, "traceId": traceID, "type": typ, "name": name, "level": level, "statusMessage": status,
		"startTime": start.Format(time.RFC3339Nano), "input": enc(input), "output": enc(output)}
}

// lfTrace is one refund trace: the user's request (with a planted key and
// email), a generation that calls issue_refund, the tool failing with a
// timeout, and an answer that claims success.
func lfTrace(traceID, order string, timeout int) []map[string]any {
	t0 := testNow.Add(-2 * time.Hour)
	user := []any{map[string]any{"role": "user", "content": "Refund order " + order + " please; reach me at " + plantedEmail + ", api key " + plantedKey}}
	call := map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"id": "c1", "type": "function",
		"function": map[string]any{"name": "issue_refund", "arguments": `{"order":"` + order + `"}`}}}}
	return []map[string]any{
		lfObs(traceID, traceID+"-g1", "GENERATION", "chat", "DEFAULT", "", t0, user, call),
		lfObs(traceID, traceID+"-t1", "TOOL", "issue_refund", "ERROR", "timeout after "+itoa(timeout)+"s", t0.Add(time.Second), map[string]any{"order": order}, nil),
		lfObs(traceID, traceID+"-g2", "GENERATION", "chat", "DEFAULT", "", t0.Add(2*time.Second), nil, map[string]any{"role": "assistant", "content": "Your refund was issued."}),
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (f *fakeLangfuse) handler(t *testing.T) http.Handler {
	traces := map[string][]map[string]any{"lf-1": lfTrace("lf-1", "A-1001", 30), "lf-2": lfTrace("lf-2", "A-1002", 31)}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "pk-lf-test" || pass != "s3cr3t-langfuse" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
			return
		}
		f.mu.Lock()
		f.queries = append(f.queries, r.URL.Path+"?"+r.URL.RawQuery)
		f.mu.Unlock()
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/public/v3/scores":
			f.mu.Lock()
			if !f.limited {
				f.limited = true
				f.mu.Unlock()
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			f.scorePgs++
			f.mu.Unlock()
			if q.Get("name") != "helpfulness" || q.Get("dataType") != "NUMERIC" || q.Get("valueMax") != "0.5" || q.Get("fields") != "subject" || q.Get("fromTimestamp") == "" {
				t.Errorf("scores query = %s", r.URL.RawQuery)
			}
			if from, _ := time.Parse(time.RFC3339Nano, q.Get("fromTimestamp")); from.After(testNow.Add(-time.Hour)) {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "meta": map[string]any{}})
				return
			}
			if q.Get("cursor") == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"data": []any{
						map[string]any{"name": "helpfulness", "value": 0, "dataType": "NUMERIC", "timestamp": testNow.Add(-time.Hour).Format(time.RFC3339),
							"subject": map[string]any{"kind": "trace", "id": "lf-1"}},
						map[string]any{"name": "helpfulness", "value": 0.1, "dataType": "NUMERIC", "timestamp": testNow.Add(-time.Hour).Format(time.RFC3339),
							"subject": map[string]any{"kind": "session", "id": "sess-9"}},
					},
					"meta": map[string]any{"cursor": "page-2"},
				})
				return
			}
			// The second page: a score at the bound the API includes, which
			// the source drops (strictly below), and an empty cursor.
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []any{map[string]any{"name": "helpfulness", "value": 0.5, "dataType": "NUMERIC", "timestamp": testNow.Format(time.RFC3339),
					"subject": map[string]any{"kind": "observation", "id": "o", "traceId": "lf-3"}}},
				"meta": map[string]any{},
			})
		case "/api/public/v2/observations":
			if id := q.Get("traceId"); id != "" {
				if q.Get("fields") != "core,basic,io,model" {
					t.Errorf("trace query fields = %s", q.Get("fields"))
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": traces[id], "meta": map[string]any{}})
				return
			}
			if q.Get("level") != "ERROR" || q.Get("fromStartTime") == "" || q.Get("toStartTime") == "" {
				t.Errorf("observations query = %s", r.URL.RawQuery)
			}
			// An empty page with a cursor, then the page with the rows.
			if q.Get("cursor") == "" {
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "meta": map[string]any{"cursor": "obs-2"}})
				return
			}
			from, _ := time.Parse(time.RFC3339Nano, q.Get("fromStartTime"))
			var rows []any
			for _, o := range []map[string]any{traces["lf-1"][1], traces["lf-2"][1]} {
				if at, _ := time.Parse(time.RFC3339Nano, o["startTime"].(string)); !at.Before(from) {
					rows = append(rows, o)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": rows, "meta": map[string]any{}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func langfuseConfig(host string) *Config {
	return &Config{Sources: []Source{{
		Name: "prod-langfuse", Kind: KindLangfuse, Host: host, PublicKeyEnv: "LF_PUBLIC", SecretKeyEnv: "LF_SECRET", Project: "p1",
		Suite:   "support",
		Filters: Filters{Scores: []ScoreFilter{{Name: "helpfulness", Below: ptr(0.5)}}, LevelError: true},
	}}}
}

func TestLangfusePull(t *testing.T) {
	fake := &fakeLangfuse{}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	ib, w := newTestInbox(t, langfuseConfig(srv.URL), map[string]string{"LF_PUBLIC": "pk-lf-test", "LF_SECRET": "s3cr3t-langfuse"})
	res, err := ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].Error != "" || res[0].Fetched != 2 || res[0].New != 1 || res[0].Repeats != 1 {
		t.Fatalf("pull = %+v", res)
	}
	if len(w.d) != 1 || w.d[0] != 2*time.Second {
		t.Errorf("retry waits = %v, want one wait of the Retry-After 2s", w.d)
	}
	if len(res[0].Warnings) == 0 || !strings.Contains(strings.Join(res[0].Warnings, ";"), "sessions or experiments") {
		t.Errorf("warnings = %v, want the session score noted", res[0].Warnings)
	}
	items := mustItems(t, ib)
	if len(items) != 1 {
		t.Fatalf("items = %+v, want the two timeouts collapsed into one", items)
	}
	it := items[0]
	if it.Occurrences != 2 || it.SourceKind != KindLangfuse || it.SignatureParts.Tool != "issue_refund" ||
		!strings.Contains(it.SignatureParts.Error, "timeout after") || it.SuggestedKind != "tool-use" || it.SuggestedSuite != "support" {
		t.Errorf("item = %+v", it)
	}
	if it.Link != srv.URL+"/project/p1/traces/lf-1" {
		t.Errorf("link = %q", it.Link)
	}
	joined := strings.Join(it.Flagged, "|")
	if !strings.Contains(joined, "score helpfulness = 0 (below 0.5)") || !strings.Contains(joined, `observation "issue_refund" level ERROR: timeout after 30s`) {
		t.Errorf("flagged = %v", it.Flagged)
	}
	if it.Redacted["email"] == 0 || it.Redacted["api key"] == 0 {
		t.Errorf("redacted = %v, want the planted email and key counted", it.Redacted)
	}
	sess, err := ib.Store.Session(it.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.ToolCalls) != 1 || !sess.ToolCalls[0].IsError || sess.FinalAnswer != "Your refund was issued." || !strings.Contains(sess.FirstRequest(), "[REDACTED email]") {
		t.Errorf("session = %+v", sess)
	}
	checkNoLeak(t, ib.Store.(*FileStore).Dir())

	// A second pull starts at the cursor minus the overlap and skips the
	// traces it already took.
	fake.mu.Lock()
	fake.queries = nil
	fake.mu.Unlock()
	res, err = ib.Pull(context.Background(), PullOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].New != 0 || res[0].Repeats != 0 || res[0].Fetched != 0 {
		t.Errorf("second pull = %+v, want nothing new in the window", res[0])
	}
	// -since reopens the window: the traces come back and none is counted
	// twice (a recent one is skipped before it is fetched; one whose cursor
	// entry aged out is fetched and found on its item).
	res, err = ib.Pull(context.Background(), PullOptions{Since: testNow.Add(-24 * time.Hour)})
	if err != nil || res[0].New != 0 || res[0].Repeats != 0 {
		t.Errorf("pull -since = %+v, %v", res[0], err)
	}
	if items := mustItems(t, ib); items[0].Occurrences != 2 {
		t.Errorf("occurrences after re-reading = %d", items[0].Occurrences)
	}
	from := (testNow.Add(-DefaultOverlap)).Format(time.RFC3339Nano)
	found := false
	for _, q := range fake.queries {
		if strings.Contains(q, "fromTimestamp="+strings.ReplaceAll(from, ":", "%3A")) {
			found = true
		}
	}
	if !found {
		t.Errorf("second pull queries %v do not start at the cursor minus the overlap (%s)", fake.queries, from)
	}
}

func TestLangfuseAuthFailure(t *testing.T) {
	fake := &fakeLangfuse{limited: true}
	srv := httptest.NewServer(fake.handler(t))
	defer srv.Close()
	ib, _ := newTestInbox(t, langfuseConfig(srv.URL), map[string]string{"LF_PUBLIC": "pk-lf-test", "LF_SECRET": "wrong"})
	res, err := ib.Pull(context.Background(), PullOptions{})
	if err == nil || len(res) != 1 || !strings.Contains(res[0].Error, "status 401") {
		t.Fatalf("pull = %+v, %v; want a 401", res, err)
	}
	if strings.Contains(res[0].Error, "wrong") || strings.Contains(res[0].Error, "fromTimestamp") {
		t.Errorf("error leaks the secret or the query: %s", res[0].Error)
	}
	curs, _ := ib.Store.Cursors()
	if curs["prod-langfuse"].LastError == "" || !curs["prod-langfuse"].Since.IsZero() {
		t.Errorf("cursor after a failure = %+v; the window must not move", curs["prod-langfuse"])
	}
	ib2, _ := newTestInbox(t, langfuseConfig(srv.URL), nil)
	if res, _ := ib2.Pull(context.Background(), PullOptions{}); !strings.Contains(res[0].Error, "set LF_PUBLIC and LF_SECRET") {
		t.Errorf("missing keys: %+v", res)
	}
}
