package bifrost

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

func fakeWithRows(t *testing.T, files ...string) (*fakeBifrost, *Gateway) {
	t.Helper()
	f := newFakeBifrost(t)
	for _, name := range files {
		f.rows = append(f.rows, json.RawMessage(readFixture(t, name)))
	}
	g := newTestGateway(t, f)
	g.pageSize = 2 // force paging over the fixtures
	g.settle = 50 * time.Millisecond
	return f, g
}

var fixtureKey = &schemas.RunKey{ID: "8b842280-1301-43e1-8b41-f1b0b479a146", KeySlot: "slot-b"}

func TestFetchRequests(t *testing.T) {
	ledger := loadLedger(t)
	tests := []struct {
		name         string
		rows         []string
		hide         int
		ledger       []schemas.LedgerEntry
		timeout      time.Duration
		complete     bool
		wantRows     int
		wantReason   string
		maxElapsed   time.Duration
		processingID string
	}{
		{name: "complete", rows: fixtureRows, ledger: ledger, complete: true, wantRows: 9},
		{name: "rows arrive late", rows: fixtureRows, hide: 3, ledger: ledger, complete: true, wantRows: 9},
		{name: "no requests at all", complete: true},
		{
			name: "ledger entry never gets a row", rows: without(fixtureRows, "retry-ok.json"), ledger: ledger,
			timeout: 300 * time.Millisecond, wantRows: 8, wantReason: "timeout after 300ms: 2 of 10 ledger entries have no row",
		},
		{
			name: "row without ledger entry returns before the timeout", rows: fixtureRows,
			ledger:  dropWhere(ledger, func(e schemas.LedgerEntry) bool { return e.Kind == "count_tokens" }),
			timeout: time.Minute, maxElapsed: 5 * time.Second, wantRows: 9, wantReason: "has no ledger entry",
		},
		{
			name: "processing row is refetched until done", rows: fixtureRows, ledger: ledger, complete: true, wantRows: 9,
			processingID: "f89e9e14-eb90-44e1-9528-08c083cf6fca",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, g := fakeWithRows(t, tt.rows...)
			f.hideLists = tt.hide
			if tt.processingID != "" {
				go finishLater(t, f, tt.processingID)
			}
			start := time.Now()
			log, err := g.FetchRequests(context.Background(), fixtureKey, schemas.FetchExpectation{
				LedgerEntries: tt.ledger, Timeout: tt.timeout, PollEvery: 10 * time.Millisecond,
			})
			if err != nil {
				t.Fatalf("FetchRequests: %v", err)
			}
			if log.Complete != tt.complete || len(log.Requests) != tt.wantRows || len(log.RowIDs) != tt.wantRows {
				t.Fatalf("complete %t rows %d reason %q", log.Complete, len(log.Requests), log.Reason)
			}
			if !strings.Contains(log.Reason, tt.wantReason) || (tt.complete && log.Reason != "") {
				t.Errorf("reason = %q, want %q", log.Reason, tt.wantReason)
			}
			if tt.maxElapsed > 0 && time.Since(start) > tt.maxElapsed {
				t.Errorf("took %s", time.Since(start))
			}
			if log.Dropped == nil || *log.Dropped != 0 {
				t.Errorf("dropped = %v", log.Dropped)
			}
			details := f.count("GET /api/logs/{id}")
			want := len(tt.rows)
			if tt.processingID != "" {
				want += 2 // fetched while processing, then again once it finished
			}
			if tt.processingID == "" && details != want {
				t.Errorf("detail fetches = %d, want %d (one per row)", details, want)
			}
			for i, id := range log.RowIDs {
				if log.Requests[i].ID != id {
					t.Errorf("row %d id %s != %s", i, log.Requests[i].ID, id)
				}
			}
		})
	}
}

// finishLater marks a row processing, then success after a few polls.
func finishLater(t *testing.T, f *fakeBifrost, id string) {
	f.mu.Lock()
	idx := -1
	var orig json.RawMessage
	for i, r := range f.rows {
		if strings.Contains(string(r), `"id": "`+id+`"`) {
			idx, orig = i, r
			f.rows[i] = json.RawMessage(strings.Replace(string(r), `"status": "success"`, `"status": "processing"`, 1))
		}
	}
	f.mu.Unlock()
	if idx < 0 {
		t.Errorf("row %s not found", id)
		return
	}
	time.Sleep(100 * time.Millisecond)
	f.mu.Lock()
	f.rows[idx] = orig
	f.mu.Unlock()
}

func TestFetchRequestsRefusesKeyWithoutID(t *testing.T) {
	_, g := fakeWithRows(t)
	if _, err := g.FetchRequests(context.Background(), &schemas.RunKey{}, schemas.FetchExpectation{}); err == nil {
		t.Fatal("want error")
	}
}

func TestFetchRequestsCancelled(t *testing.T) {
	_, g := fakeWithRows(t, fixtureRows...)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.FetchRequests(ctx, fixtureKey, schemas.FetchExpectation{LedgerEntries: loadLedger(t)}); err == nil {
		t.Fatal("want context error")
	}
}

// TestFetchRequestsDirect: a direct provider's run has no ledger. Its rows
// are complete when nothing is processing and two polls match, after the
// settle time, even though no ledger entry backs them.
func TestFetchRequestsDirect(t *testing.T) {
	f := newFakeBifrost(t)
	for _, name := range []string{"chat-json.json", "responses-stream-tools.json", "retry-fail.json"} {
		f.rows = append(f.rows, json.RawMessage(readFixture(t, "openai/"+name)))
	}
	f.hideLists = 2
	g, err := New(multiConfig(f.srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	g.settle = 50 * time.Millisecond
	key := &schemas.RunKey{ID: "vk-direct", KeySlot: "oa-slot-1", Provider: "openai"}
	log, err := g.FetchRequests(context.Background(), key, schemas.FetchExpectation{Timeout: 5 * time.Second, PollEvery: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if !log.Complete || log.Reason != "" || len(log.Requests) != 3 {
		t.Fatalf("direct log complete %t reason %q rows %d", log.Complete, log.Reason, len(log.Requests))
	}
	// The same rows on a proxied key with an empty ledger do not join.
	pkey := &schemas.RunKey{ID: "vk-proxied", KeySlot: "fp-slot-1", Provider: "anthropic"}
	plog, err := g.FetchRequests(context.Background(), pkey, schemas.FetchExpectation{Timeout: 300 * time.Millisecond, PollEvery: 20 * time.Millisecond})
	if err != nil || plog.Complete || !strings.Contains(plog.Reason, "has no ledger entry") {
		t.Errorf("proxied key without ledger: complete %t reason %q err %v", plog.Complete, plog.Reason, err)
	}
}

// TestDroppedLogs: the counter is dropped_requests alone (the maintenance
// count is a subset of it), and a counter that cannot be read is an error at
// preflight and a nil counter on the run's log, never 0.
func TestDroppedLogs(t *testing.T) {
	tests := []struct {
		name    string
		answer  map[string]any
		down    bool
		want    int64
		wantErr string
	}{
		{name: "zero", answer: map[string]any{"dropped_requests": 0}, want: 0},
		{name: "maintenance is a subset, not added", answer: map[string]any{"dropped_requests": 7, "dropped_during_maintenance": 2}, want: 7},
		{name: "no dropped_requests field", answer: map[string]any{"dropped_during_maintenance": 2}, wantErr: "answer has no dropped_requests"},
		{name: "server error", down: true, wantErr: "read dropped log counter: bifrost GET /api/logs/dropped: status 500"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, g := fakeWithRows(t)
			f.dropped = tt.answer
			if tt.down {
				f.down = map[string]bool{"GET /api/logs/dropped": true}
			}
			got, err := g.DroppedLogs(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
			} else if err != nil || got != tt.want {
				t.Fatalf("got %d, %v, want %d", got, err, tt.want)
			}
			log := g.finish(context.Background(), &schemas.RequestLog{Complete: true})
			if tt.wantErr != "" && log.Dropped != nil {
				t.Fatalf("unreadable counter recorded as %d", *log.Dropped)
			}
			if tt.wantErr == "" && (log.Dropped == nil || *log.Dropped != tt.want) {
				t.Fatalf("log counter = %v, want %d", log.Dropped, tt.want)
			}
			m, err := g.Preflight(context.Background())
			if err != nil {
				t.Fatalf("Preflight: %v", err)
			}
			if tt.wantErr == "" && (m.DroppedLogs == nil || *m.DroppedLogs != tt.want) {
				t.Fatalf("preflight baseline = %v, want %d", m.DroppedLogs, tt.want)
			}
			if tt.wantErr != "" && (m.DroppedLogs != nil || !strings.Contains(strings.Join(m.Problems, "\n"), "dropped log counter: ")) {
				t.Fatalf("preflight baseline %v, problems %q", m.DroppedLogs, m.Problems)
			}
		})
	}
}
