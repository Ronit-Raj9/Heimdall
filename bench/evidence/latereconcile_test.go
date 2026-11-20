package evidence

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ledgerAwareGateway reports a complete log only when the expectation holds
// want ledger entries, as a gateway whose last row has a late ledger entry.
type ledgerAwareGateway struct {
	fakeGateway
	want  int
	calls int
}

func (g *ledgerAwareGateway) FetchRequests(_ context.Context, _ *schemas.RunKey, expect schemas.FetchExpectation) (*schemas.RequestLog, error) {
	g.calls++
	g.expect = expect
	if len(expect.LedgerEntries) == g.want {
		return &schemas.RequestLog{Complete: true}, nil
	}
	return &schemas.RequestLog{Reason: "row r2 has no ledger entry"}, nil
}

// TestGatewayReconcilesLateLedgerEntry (3-B-09): the ledger was read before
// the proxy persisted a streamed request's entry. The gateway source reads
// the ledger again, matches the rows against the grown ledger, and records
// the grown ledger as the evidence ledger.
func TestGatewayReconcilesLateLedgerEntry(t *testing.T) {
	rc := testRC(t)
	rc.Key = &schemas.RunKey{ID: "vk-1"}
	full := chain(t, nil,
		schemas.LedgerEntry{Slot: "s1", RunID: rc.Run.RunID, Call: 1, Kind: "message", Action: "pass", Status: 200},
		schemas.LedgerEntry{Slot: "s1", RunID: rc.Run.RunID, Call: 2, Kind: "message", Action: "pass", Status: 200})
	gw := &ledgerAwareGateway{want: 2}
	src := &gatewaySource{gateway: gw, fault: &fakeFault{entries: full}}
	ev := &schemas.Evidence{Ledger: full[:1]}
	if err := src.Collect(context.Background(), rc, ev); err != nil {
		t.Fatal(err)
	}
	if ev.Requests == nil || !ev.Requests.Complete || len(ev.Ledger) != 2 || gw.calls != 2 {
		t.Fatalf("complete %v ledger %d calls %d", ev.Requests != nil && ev.Requests.Complete, len(ev.Ledger), gw.calls)
	}
	// A re-read that does not extend the first one changes nothing.
	gw2 := &ledgerAwareGateway{want: 5}
	forked := chain(t, map[string]string{"s1": "other"}, full[0], full[1])
	ev2 := &schemas.Evidence{Ledger: full[:1]}
	src2 := &gatewaySource{gateway: gw2, fault: &fakeFault{entries: forked}}
	if err := src2.Collect(context.Background(), rc, ev2); err != nil {
		t.Fatal(err)
	}
	if ev2.Requests.Complete || len(ev2.Ledger) != 1 || gw2.calls != 1 {
		t.Fatalf("forked re-read: complete %t ledger %d calls %d", ev2.Requests.Complete, len(ev2.Ledger), gw2.calls)
	}
}
