package evidence

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fakeFault is a FaultController that serves a fixed ledger.
type fakeFault struct {
	entries []schemas.LedgerEntry
	err     error
	gotRun  string
}

func (f *fakeFault) SetPlan(context.Context, string, schemas.FaultPlan) error { return nil }
func (f *fakeFault) ClearPlan(context.Context, string) error                  { return nil }
func (f *fakeFault) Ledger(_ context.Context, runID string) ([]schemas.LedgerEntry, error) {
	f.gotRun = runID
	return f.entries, f.err
}

// chain fills PrevHash and Hash the way the fault proxy does: per slot, each
// entry's hash is the sha256 of its canonical JSON with Hash empty. prev
// seeds the first PrevHash of each slot, as if earlier runs used it.
func chain(t *testing.T, prev map[string]string, entries ...schemas.LedgerEntry) []schemas.LedgerEntry {
	t.Helper()
	last := map[string]string{}
	for k, v := range prev {
		last[k] = v
	}
	out := make([]schemas.LedgerEntry, len(entries))
	for i, e := range entries {
		e.PrevHash = last[e.Slot]
		h, err := ledgerEntryHash(e)
		if err != nil {
			t.Fatal(err)
		}
		e.Hash = h
		last[e.Slot] = h
		out[i] = e
	}
	return out
}

func ledgerFixture(t *testing.T) []schemas.LedgerEntry {
	t0 := time.Date(2026, 11, 14, 10, 0, 0, 0, time.UTC)
	return chain(t, map[string]string{"slot-a": "abc123"},
		schemas.LedgerEntry{Slot: "slot-a", RunID: "r_1", Call: 1, Kind: "message", Path: "/v1/messages", Action: "pass", Status: 200, At: t0},
		schemas.LedgerEntry{Slot: "slot-a", RunID: "r_1", Call: 0, Kind: "count_tokens", Path: "/v1/messages/count_tokens", Action: "pass", Status: 200, At: t0.Add(time.Second)},
		schemas.LedgerEntry{Slot: "slot-a", RunID: "r_1", Call: 2, Kind: "message", Path: "/v1/messages", Action: "inject", Status: 503, Nonce: "nonce_secret", At: t0.Add(2 * time.Second)},
	)
}

func TestLedgerChainBreaks(t *testing.T) {
	good := ledgerFixture(t)
	tamper := func(f func(es []schemas.LedgerEntry)) []schemas.LedgerEntry {
		es := append([]schemas.LedgerEntry(nil), good...)
		f(es)
		return es
	}
	twoSlots := chain(t, nil,
		schemas.LedgerEntry{Slot: "a", Call: 1, Kind: "message"},
		schemas.LedgerEntry{Slot: "b", Call: 1, Kind: "message"},
		schemas.LedgerEntry{Slot: "a", Call: 2, Kind: "message"},
	)
	tests := []struct {
		name    string
		entries []schemas.LedgerEntry
		want    []string // substrings, one per break
	}{
		{name: "intact chain continuing an earlier run", entries: good},
		{name: "interleaved slots", entries: twoSlots},
		{name: "empty", entries: nil},
		{name: "edited content", entries: tamper(func(es []schemas.LedgerEntry) { es[2].Status = 200 }), want: []string{"entry 3", "hash does not match content"}},
		{name: "deleted entry", entries: tamper(func(es []schemas.LedgerEntry) { copy(es[1:], es[2:]) })[:2], want: []string{"entry 2", "prev_hash does not point"}},
		{name: "reordered entries", entries: []schemas.LedgerEntry{good[0], good[2], good[1]}, want: []string{"entry 2", "prev_hash", "entry 3", "prev_hash"}},
		{name: "missing hash", entries: tamper(func(es []schemas.LedgerEntry) { es[0].Hash = "" }), want: []string{"entry 1", "empty hash", "entry 2", "prev_hash"}},
		{name: "duplicated entry", entries: []schemas.LedgerEntry{good[0], good[0]}, want: []string{"entry 2", "prev_hash", "entry 2", "repeats entry 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			breaks := LedgerChainBreaks(tt.entries)
			joined := strings.Join(breaks, "\n")
			if len(tt.want) == 0 && len(breaks) > 0 {
				t.Fatalf("unexpected breaks:\n%s", joined)
			}
			if len(tt.want) > 0 && len(breaks) == 0 {
				t.Fatalf("no break detected, want %v", tt.want)
			}
			rest := joined
			for _, w := range tt.want {
				i := strings.Index(rest, w)
				if i < 0 {
					t.Fatalf("breaks %q missing %q in order", joined, w)
				}
				rest = rest[i+len(w):]
			}
		})
	}
}

func TestProxyLedgerCollect(t *testing.T) {
	t.Run("records ledger, file and fault events", func(t *testing.T) {
		rc := testRC(t)
		fake := &fakeFault{entries: ledgerFixture(t)}
		ev := &schemas.Evidence{}
		if err := newProxyLedgerSource(Deps{Fault: fake}).Collect(context.Background(), rc, ev); err != nil {
			t.Fatal(err)
		}
		if fake.gotRun != "r_1" {
			t.Errorf("ledger asked for run %q, want r_1", fake.gotRun)
		}
		if len(ev.Ledger) != 3 {
			t.Fatalf("ledger has %d entries, want 3", len(ev.Ledger))
		}
		b, err := os.ReadFile(filepath.Join(rc.Dir, ledgerFile))
		if err != nil {
			t.Fatal(err)
		}
		if n := strings.Count(string(b), "\n"); n != 3 {
			t.Errorf("%s has %d lines, want 3", ledgerFile, n)
		}
		if len(ev.Events) != 1 {
			t.Fatalf("got %d events, want 1 fault event", len(ev.Events))
		}
		e := ev.Events[0]
		if e.Type != schemas.EventFault || e.Source != schemas.SourceProxyLedger || e.EventID != "r_1:proxy_ledger:1" || !e.IsError {
			t.Errorf("fault event = %+v", e)
		}
		if strings.Contains(e.Summary, "nonce_secret") {
			t.Errorf("fault event leaks the nonce: %q", e.Summary)
		}
	})
	t.Run("chain break becomes an error event", func(t *testing.T) {
		rc := testRC(t)
		entries := ledgerFixture(t)
		entries[1].Status = 500
		ev := &schemas.Evidence{}
		if err := newProxyLedgerSource(Deps{Fault: &fakeFault{entries: entries}}).Collect(context.Background(), rc, ev); err != nil {
			t.Fatal(err)
		}
		var found bool
		for _, e := range ev.Events {
			if e.Type == schemas.EventError && strings.Contains(e.Summary, "ledger hash chain broken") {
				found = true
			}
		}
		if !found {
			t.Errorf("no chain break event in %+v", ev.Events)
		}
	})
	t.Run("no fault controller records nothing", func(t *testing.T) {
		ev := &schemas.Evidence{}
		if err := newProxyLedgerSource(Deps{}).Collect(context.Background(), testRC(t), ev); err != nil {
			t.Fatal(err)
		}
		if ev.Ledger != nil || ev.Events != nil {
			t.Errorf("evidence changed: %+v", ev)
		}
	})
	t.Run("ledger error is returned", func(t *testing.T) {
		err := newProxyLedgerSource(Deps{Fault: &fakeFault{err: errors.New("proxy down")}}).Collect(context.Background(), testRC(t), &schemas.Evidence{})
		if err == nil || !strings.Contains(err.Error(), "proxy down") {
			t.Errorf("err = %v, want proxy down", err)
		}
	})
}

// TestLedgerChainBreakTaintsGatewayAndCost is the regression test for audit
// finding 3-B-07: a broken proxy ledger chain is recorded on the evidence,
// and the gateway log collected after it is incomplete and every cost row
// unknown, even when the gateway itself reported a complete log.
func TestLedgerChainBreakTaintsGatewayAndCost(t *testing.T) {
	rc := testRC(t)
	rc.Key = &schemas.RunKey{ID: "vk_1"}
	entries := ledgerFixture(t)
	entries[1].Status = 500
	ev := &schemas.Evidence{}
	ctx := context.Background()
	if err := newProxyLedgerSource(Deps{Fault: &fakeFault{entries: entries}}).Collect(ctx, rc, ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.LedgerBreaks) == 0 {
		t.Fatal("chain break not recorded on the evidence")
	}
	log := requestLogFixture()
	log.Complete = true
	if err := newGatewaySource(Deps{Gateway: &fakeGateway{log: log}}).Collect(ctx, rc, ev); err != nil {
		t.Fatal(err)
	}
	if ev.Requests.Complete || !strings.Contains(ev.Requests.Reason, "proxy ledger hash chain broken") {
		t.Fatalf("gateway log complete=%v reason %q after a ledger chain break", ev.Requests.Complete, ev.Requests.Reason)
	}
	if ev.Requests.Tally().Complete {
		t.Fatal("tally reads complete")
	}
	if err := newCostSource(Deps{Prices: testPrices(t)}).Collect(ctx, rc, ev); err != nil {
		t.Fatal(err)
	}
	if len(ev.Cost) == 0 {
		t.Fatal("no cost rows")
	}
	for _, r := range ev.Cost {
		if _, ok := r.USD.Float(); ok {
			t.Fatalf("cost row %s priced from a broken ledger: %+v", r.RequestID, r.USD)
		}
	}
}
