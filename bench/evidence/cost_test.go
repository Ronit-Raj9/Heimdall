package evidence

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// describeRow renders a cost row as "req#attempt usd [inject]" for compact
// checks. An unknown value shows its bounds when it has them.
func describeRow(r schemas.CostRow) string {
	usd := "?"
	switch {
	case r.USD.State == schemas.ValueNotApplicable:
		usd = "n/a"
	case r.USD.IsObserved():
		v, _ := r.USD.Float()
		usd = fmt.Sprintf("%.6f", v)
	case r.USD.Bounds != nil:
		usd = fmt.Sprintf("unknown[%.6f,%.6f]", r.USD.Bounds[0], r.USD.Bounds[1])
	default:
		usd = "unknown"
	}
	s := fmt.Sprintf("%s#%d %s", r.RequestID, r.Attempt, usd)
	if r.FaultInject {
		s += " inject"
	}
	return s
}

func msgReq(id string, at time.Duration, status string) schemas.GatewayRequest {
	return schemas.GatewayRequest{ID: id, Timestamp: t0.Add(at), Status: status, Kind: "message", Provider: "anthropic", ModelRequested: "claude-x", ModelServed: "claude-x"}
}

func TestCostRows(t *testing.T) {
	tok := &schemas.Tokens{Fresh: 1000, CacheRead: 2000, CacheWrite: 100, Out: 500} // 0.011475 at claude-x prices
	retried := msgReq("r1", 0, "success")
	retried.Attempts = []schemas.Attempt{{N: 1, StatusCode: 503}, {N: 2, StatusCode: 200}}
	retried.Tokens = tok
	rejected := msgReq("r2", 0, "error")
	rejected.ErrorStatus = 429
	unknownModel := msgReq("r3", 0, "success")
	unknownModel.ModelServed = "claude-new"
	unknownModel.Tokens = &schemas.Tokens{Fresh: 1000, Out: 1000}
	noUsage := msgReq("r4", 0, "success")
	noUsage.ResponseID = "msg_4"
	retriedNoUsage := msgReq("r5", 0, "success")
	retriedNoUsage.Attempts = retried.Attempts
	timedOut := msgReq("r6", 0, "success")
	timedOut.Attempts = []schemas.Attempt{{N: 1, FailReason: "timeout"}, {N: 2, StatusCode: 200}}
	timedOut.Tokens = tok
	plain := msgReq("r7", 0, "success")
	plain.Tokens = tok
	cached := msgReq("r8", 0, "success")
	cached.CacheHit = true
	countTokens := schemas.GatewayRequest{ID: "c1", Timestamp: t0, Status: "success", Kind: "count_tokens"}
	billed := 0.02
	retried.BifrostUSD = &billed

	inject := func(call, status int) schemas.LedgerEntry {
		return schemas.LedgerEntry{Kind: "message", Call: call, Action: "inject", Status: status}
	}
	pass := func(call int, resp string, tk *schemas.Tokens) schemas.LedgerEntry {
		return schemas.LedgerEntry{Kind: "message", Call: call, Action: "pass", Status: 200, ResponseID: resp, Tokens: tk}
	}

	teamReq := func(id string, at time.Duration, resp string) schemas.GatewayRequest {
		r := msgReq(id, at, "success")
		r.ResponseID = resp
		return r
	}
	teamA, teamB, teamC := teamReq("ta", 0, "msg_a"), teamReq("tb", time.Second, "msg_b"), teamReq("tc", 2*time.Second, "msg_c")
	slotPass := func(slot string, call int, resp string, outM int64) schemas.LedgerEntry {
		e := pass(call, resp, &schemas.Tokens{Out: outM * 1_000_000})
		e.Slot = slot
		return e
	}

	tests := []struct {
		name   string
		reqs   []schemas.GatewayRequest
		ledger []schemas.LedgerEntry
		prices *PriceTable
		want   []string
	}{
		{name: "retry after an upstream 5xx is not injected", reqs: []schemas.GatewayRequest{retried}, want: []string{"r1#1 0.000000", "r1#2 0.011475"}},
		{name: "passed-through upstream 503 is not injected", reqs: []schemas.GatewayRequest{retried},
			ledger: []schemas.LedgerEntry{{Kind: "message", Call: 1, Action: "pass", Status: 503}, pass(2, "", nil)},
			want:   []string{"r1#1 0.000000", "r1#2 0.011475"}},
		{name: "team slots match by response id, not call order", reqs: []schemas.GatewayRequest{teamA, teamB, teamC},
			ledger: []schemas.LedgerEntry{slotPass("lead", 1, "msg_a", 1), slotPass("lead", 2, "msg_b", 2), slotPass("member", 1, "msg_c", 3)},
			want:   []string{"ta#1 15.000000", "tb#1 30.000000", "tc#1 45.000000"}},
		{name: "duplicate response id borrows nothing", reqs: []schemas.GatewayRequest{teamA},
			ledger: []schemas.LedgerEntry{slotPass("lead", 1, "msg_a", 1), slotPass("member", 1, "msg_a", 2)},
			want:   []string{"ta#1 unknown"}},
		{name: "proxy-injected 429", reqs: []schemas.GatewayRequest{rejected}, ledger: []schemas.LedgerEntry{inject(1, 429)}, want: []string{"r2#1 0.000000 inject"}},
		{name: "provider 429 is rejected, not injected", reqs: []schemas.GatewayRequest{rejected}, want: []string{"r2#1 0.000000"}},
		{name: "unknown model bounded by table maximum", reqs: []schemas.GatewayRequest{unknownModel}, want: []string{"r3#1 unknown[0.000000,0.090000]"}},
		{name: "missing usage", reqs: []schemas.GatewayRequest{noUsage}, want: []string{"r4#1 unknown"}},
		{name: "ledger usage by response id", reqs: []schemas.GatewayRequest{noUsage},
			ledger: []schemas.LedgerEntry{pass(1, "msg_other", nil), pass(2, "msg_4", &schemas.Tokens{Out: 1_000_000})},
			want:   []string{"r4#1 15.000000"}},
		{name: "ledger usage in order", reqs: []schemas.GatewayRequest{retriedNoUsage},
			ledger: []schemas.LedgerEntry{pass(2, "", &schemas.Tokens{Fresh: 1_000_000}), inject(1, 503)},
			want:   []string{"r5#1 0.000000 inject", "r5#2 3.000000"}},
		{name: "contradicting ledger is not paired", reqs: []schemas.GatewayRequest{plain}, ledger: []schemas.LedgerEntry{inject(1, 503)}, want: []string{"r7#1 0.011475"}},
		{name: "attempt failed without status", reqs: []schemas.GatewayRequest{timedOut}, want: []string{"r6#1 unknown", "r6#2 0.011475"}},
		{name: "cache hit", reqs: []schemas.GatewayRequest{cached}, want: []string{"r8#1 0.000000"}},
		{name: "token counting does not apply", reqs: []schemas.GatewayRequest{countTokens}, want: []string{"c1#1 n/a"}},
		{name: "no price table", reqs: []schemas.GatewayRequest{plain}, prices: &PriceTable{}, want: []string{"r7#1 unknown"}},
		{name: "processing", reqs: []schemas.GatewayRequest{msgReq("r9", 0, "processing")}, want: []string{"r9#1 unknown"}},
		{name: "time order", reqs: []schemas.GatewayRequest{msgReq("late", time.Second, "error"), msgReq("early", 0, "error")}, want: []string{"early#1 unknown", "late#1 unknown"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prices := tt.prices
			if prices == nil {
				prices = testPrices(t)
			}
			rows := CostRows(&schemas.RequestLog{Requests: tt.reqs, Complete: true}, tt.ledger, prices)
			var got []string
			for _, r := range rows {
				got = append(got, describeRow(r))
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Errorf("rows:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestCostRowFields(t *testing.T) {
	prices := testPrices(t)
	req := msgReq("r1", 0, "success")
	req.Attempts = []schemas.Attempt{{N: 1, StatusCode: 503}, {N: 2, StatusCode: 200}}
	req.Tokens = &schemas.Tokens{Fresh: 1, CacheRead: 2, CacheWrite: 3, Out: 4}
	billed := 0.5
	req.BifrostUSD = &billed
	rows := CostRows(&schemas.RequestLog{Requests: []schemas.GatewayRequest{req}}, nil, prices)
	if len(rows) != 2 {
		t.Fatalf("got %d rows", len(rows))
	}
	first, last := rows[0], rows[1]
	checks := []struct {
		name string
		ok   bool
	}{
		{"price ref names the file hash", strings.HasPrefix(last.PriceRef, "test-snap@sha256:") && last.PriceRef == prices.Ref()},
		{"model is the served model", last.Model == "claude-x"},
		{"usage on the answering attempt only", last.Tokens == *req.Tokens && first.Tokens == schemas.Tokens{}},
		{"bifrost cost on the last attempt only", first.BifrostUSD == nil && last.BifrostUSD != nil && *last.BifrostUSD == 0.5},
		{"bifrost cost is a copy", last.BifrostUSD != req.BifrostUSD},
		{"gateway evidence", last.USD.Evidence == schemas.EvidenceGateway},
	}
	for _, c := range checks {
		t.Run(c.name, func(t *testing.T) {
			if !c.ok {
				t.Errorf("rows = %+v", rows)
			}
		})
	}
	t.Run("nil price table", func(t *testing.T) {
		rows := CostRows(&schemas.RequestLog{Requests: []schemas.GatewayRequest{req}}, nil, nil)
		if rows[1].PriceRef != "" || rows[1].USD.State != schemas.ValueUnknown || rows[1].USD.Bounds != nil || rows[1].USD.Reason != "no price table" {
			t.Errorf("row = %+v", rows[1])
		}
	})
	t.Run("nil log", func(t *testing.T) {
		if CostRows(nil, nil, prices) != nil {
			t.Errorf("want nil rows")
		}
	})
}

func TestCostCollect(t *testing.T) {
	t.Run("writes rows and prices responses", func(t *testing.T) {
		rc := testRC(t)
		log := requestLogFixture()
		ev := &schemas.Evidence{Requests: log, Events: NormalizeRequests(rc, log)}
		if err := newCostSource(Deps{Prices: testPrices(t)}).Collect(context.Background(), rc, ev); err != nil {
			t.Fatal(err)
		}
		// req1 has 2 attempts; req2, req2fb, req3 and the count_tokens row 1 each.
		if len(ev.Cost) != 6 {
			t.Fatalf("got %d cost rows, want 6", len(ev.Cost))
		}
		b, err := os.ReadFile(filepath.Join(rc.Dir, costFile))
		if err != nil {
			t.Fatal(err)
		}
		var onDisk []schemas.CostRow
		if err := json.Unmarshal(b, &onDisk); err != nil {
			t.Fatal(err)
		}
		if len(onDisk) != 6 {
			t.Errorf("%s has %d rows, want 6", costFile, len(onDisk))
		}
		priced := map[string]string{}
		for _, e := range ev.Events {
			if e.Type == schemas.EventModelResponse && e.USD != nil {
				v, _ := e.USD.Float()
				priced[e.RequestID] = fmt.Sprintf("%s %.6f", e.USD.State, v)
			}
		}
		want := map[string]string{
			"req1":   "observed 0.011475", // the 503 attempt adds 0
			"req2":   "observed 0.000000", // a 503 the provider does not bill
			"req2fb": "unknown 0.000000",  // claude-y is not in the table
			"req3":   "observed 0.011475",
		}
		for id, w := range want {
			if priced[id] != w {
				t.Errorf("%s response usd = %q, want %q", id, priced[id], w)
			}
		}
	})
	t.Run("no gateway log writes nothing", func(t *testing.T) {
		rc := testRC(t)
		ev := &schemas.Evidence{}
		if err := newCostSource(Deps{Prices: testPrices(t)}).Collect(context.Background(), rc, ev); err != nil {
			t.Fatal(err)
		}
		if ev.Cost != nil {
			t.Errorf("cost = %+v", ev.Cost)
		}
		if _, err := os.Stat(filepath.Join(rc.Dir, costFile)); !os.IsNotExist(err) {
			t.Errorf("%s written without a gateway log", costFile)
		}
	})
}

func TestRequestTotals(t *testing.T) {
	obs := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }
	rows := []schemas.CostRow{
		{RequestID: "a", USD: obs(1)}, {RequestID: "a", USD: obs(2)},
		{RequestID: "b", USD: obs(1)}, {RequestID: "b", USD: schemas.UnknownWithin("x", schemas.EvidenceGateway, 0, 4)},
		{RequestID: "c", USD: obs(1)}, {RequestID: "c", USD: schemas.Unknown("x", schemas.EvidenceGateway)},
		{RequestID: "d", USD: schemas.NotApplicable()},
	}
	got := requestTotals(rows)
	if v, ok := got["a"].Float(); !ok || v != 3 {
		t.Errorf("a = %+v, want observed 3", got["a"])
	}
	if b := got["b"]; b.State != schemas.ValueUnknown || b.Bounds == nil || b.Bounds[0] != 1 || b.Bounds[1] != 5 {
		t.Errorf("b = %+v, want unknown within [1, 5]", b)
	}
	if c := got["c"]; c.State != schemas.ValueUnknown || c.Bounds != nil {
		t.Errorf("c = %+v, want unbounded unknown", c)
	}
	if _, ok := got["d"]; ok {
		t.Errorf("d has a total, want none")
	}
}
