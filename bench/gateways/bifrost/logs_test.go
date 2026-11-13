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
