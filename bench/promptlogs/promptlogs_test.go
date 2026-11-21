package promptlogs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) GetSetting(_ context.Context, org, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[org+"/"+key]
	return v, ok, nil
}

func (s *memSettings) SetSetting(_ context.Context, org, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[org+"/"+key] = value
	return nil
}

type fakeRow struct {
	ID   string
	Org  string
	At   time.Time
	Text bool
}

// fakeGateway answers GET and DELETE /api/logs like Bifrost, honouring the
// metadata filter, end_time, order, limit and offset. ignoreFilter makes it
// return every row, like a gateway that dropped the filter.
type fakeGateway struct {
	mu           sync.Mutex
	rows         []fakeRow
	ignoreFilter bool
	token        string
	sawNoFilter  bool
}

func (f *fakeGateway) handler(t *testing.T) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(setupTokenHeader) != f.token {
			w.WriteHeader(401)
			return
		}
		q := r.URL.Query()
		org := q.Get("metadata_" + schemas.PromptLogOrgMetadataKey)
		if org == "" {
			f.sawNoFilter = true
		}
		var end time.Time
		if s := q.Get("end_time"); s != "" {
			end, _ = time.Parse(time.RFC3339, s)
		}
		f.mu.Lock()
		var sel []fakeRow
		for _, row := range f.rows {
			if !f.ignoreFilter && row.Org != org {
				continue
			}
			if !end.IsZero() && !row.At.Before(end) {
				continue
			}
			sel = append(sel, row)
		}
		f.mu.Unlock()
		sort.Slice(sel, func(i, j int) bool {
			if q.Get("order") == "asc" {
				return sel[i].At.Before(sel[j].At)
			}
			return sel[i].At.After(sel[j].At)
		})
		total := len(sel)
		off, _ := strconv.Atoi(q.Get("offset"))
		lim, _ := strconv.Atoi(q.Get("limit"))
		if off > len(sel) {
			off = len(sel)
		}
		sel = sel[off:]
		if lim > 0 && lim < len(sel) {
			sel = sel[:lim]
		}
		logs := []map[string]any{}
		for _, row := range sel {
			logs = append(logs, map[string]any{"id": row.ID, "timestamp": row.At, "provider": "anthropic", "model": "claude",
				"status": "success", "cost": 0.01, "latency": 120.0, "content_hidden": !row.Text,
				"token_usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5},
				"metadata":    map[string]any{schemas.PromptLogOrgMetadataKey: row.Org, schemas.PromptLogRunMetadataKey: "r_" + row.ID}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"logs": logs, "pagination": map[string]any{"total_count": total}})
	})
	mux.HandleFunc("DELETE /api/logs", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			IDs []string `json:"ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || len(in.IDs) == 0 {
			w.WriteHeader(400)
			return
		}
		del := map[string]bool{}
		for _, id := range in.IDs {
			del[id] = true
		}
		f.mu.Lock()
		kept := f.rows[:0]
		for _, row := range f.rows {
			if !del[row.ID] {
				kept = append(kept, row)
			}
		}
		f.rows = kept
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"message": "ok"})
	})
	return mux
}

func (f *fakeGateway) ids(org string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.rows {
		if r.Org == org {
			out = append(out, r.ID)
		}
	}
	sort.Strings(out)
	return out
}

func newFake(t *testing.T, rows []fakeRow) (*fakeGateway, *Client) {
	f := &fakeGateway{rows: rows, token: "setup-tok"}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL+"/", "setup-tok")
	c.PageSize = 2
	return f, c
}

var now = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func days(n int) time.Time { return now.Add(-time.Duration(n) * 24 * time.Hour) }

func TestPolicyDefaultsAndSave(t *testing.T) {
	st := &memSettings{}
	ctx := context.Background()
	p, err := LoadPolicy(ctx, st, "org_a")
	if err != nil || p != schemas.DefaultPromptLogPolicy() {
		t.Fatalf("default = %+v, %v", p, err)
	}
	if err := SavePolicy(ctx, st, "org_a", schemas.PromptLogPolicy{KeepContent: false, RetentionDays: 0}); err == nil {
		t.Error("retention 0 saved")
	}
	if err := SavePolicy(ctx, st, "org_a", schemas.PromptLogPolicy{RetentionDays: 31}); err == nil {
		t.Error("retention 31 saved")
	}
	if err := SavePolicy(ctx, st, "org_a", schemas.PromptLogPolicy{KeepContent: false, RetentionDays: 3}); err != nil {
		t.Fatal(err)
	}
	p, _ = LoadPolicy(ctx, st, "org_a")
	if p.KeepContent || p.RetentionDays != 3 {
		t.Errorf("saved = %+v", p)
	}
	if q, _ := LoadPolicy(ctx, st, "org_b"); q != schemas.DefaultPromptLogPolicy() {
		t.Errorf("another org sees org_a's policy: %+v", q)
	}
	if NewClient("  ", "x") != nil {
		t.Error("empty url gives a client")
	}
}

func TestListOnlyOwnOrg(t *testing.T) {
	f, c := newFake(t, []fakeRow{{"a1", "org_a", days(1), true}, {"b1", "org_b", days(1), true}, {"a2", "org_a", days(2), false}})
	page, err := c.List(context.Background(), "org_a", Query{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Rows) != 2 || page.Rows[0].ID != "a1" || page.Rows[1].ID != "a2" || page.Total != 2 {
		t.Fatalf("page = %+v", page)
	}
	r := page.Rows[0]
	if r.RunID != "r_a1" || r.PromptTokens != 10 || r.CompletionTokens != 5 || r.CostUSD != 0.01 || !r.ContentStored || page.Rows[1].ContentStored {
		t.Errorf("row = %+v", page.Rows)
	}
	if _, err := c.List(context.Background(), "", Query{}); err == nil {
		t.Error("list without an org")
	}
	if f.sawNoFilter {
		t.Error("a list went out without the org filter")
	}
	// A gateway that ignores the filter still never leaks org_b's rows.
	f.ignoreFilter = true
	page, _ = c.List(context.Background(), "org_a", Query{Limit: 10})
	for _, row := range page.Rows {
		if row.ID == "b1" {
			t.Errorf("leaked another org's row: %+v", page.Rows)
		}
	}
}

func TestPurgeOnlyOwnOrg(t *testing.T) {
	rows := []fakeRow{{"b1", "org_b", days(1), true}}
	for i := 0; i < 5; i++ {
		rows = append(rows, fakeRow{"a" + strconv.Itoa(i), "org_a", days(i + 1), true})
	}
	f, c := newFake(t, rows)
	n, err := c.Purge(context.Background(), "org_a", time.Time{})
	if err != nil || n != 5 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if got := f.ids("org_a"); len(got) != 0 {
		t.Errorf("org_a left = %v", got)
	}
	if got := f.ids("org_b"); len(got) != 1 {
		t.Errorf("org_b touched: %v", got)
	}
}

func TestSweepAppliesEachRetention(t *testing.T) {
	f, c := newFake(t, []fakeRow{
		{"a_new", "org_a", days(1), true}, {"a_old", "org_a", days(5), true},
		{"b_new", "org_b", days(1), true}, {"b_mid", "org_b", days(5), true}, {"b_old", "org_b", days(10), true},
	})
	st := &memSettings{}
	ctx := context.Background()
	if err := SavePolicy(ctx, st, "org_a", schemas.PromptLogPolicy{KeepContent: true, RetentionDays: 3}); err != nil {
		t.Fatal(err)
	}
	// org_b keeps the default (7 days).
	n, err := Sweep(ctx, st, c, []string{"org_a", "org_b"}, now)
	if err != nil || n != 2 {
		t.Fatalf("sweep = %d, %v", n, err)
	}
	if got := f.ids("org_a"); len(got) != 1 || got[0] != "a_new" {
		t.Errorf("org_a = %v", got)
	}
	if got := f.ids("org_b"); len(got) != 2 {
		t.Errorf("org_b = %v", got)
	}
	if n, err := Sweep(ctx, st, nil, []string{"org_a"}, now); n != 0 || err != nil {
		t.Errorf("sweep without a gateway = %d, %v", n, err)
	}
}
