package metrics

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestCostUSD(t *testing.T) {
	gw := schemas.EvidenceGateway
	priced := func(usd float64) schemas.CostRow {
		return schemas.CostRow{RequestID: "r", Attempt: 1, USD: schemas.Observed(usd, gw)}
	}
	unpriced := func() schemas.CostRow {
		return schemas.CostRow{RequestID: "r", Attempt: 2, USD: schemas.Unknown("model not in snapshot", gw)}
	}
	boundedRow := func(lo, hi float64) schemas.CostRow {
		return schemas.CostRow{RequestID: "r", Attempt: 3, USD: schemas.UnknownWithin("partial usage", gw, lo, hi)}
	}
	bifrost := 99.0
	tests := []struct {
		name string
		ev   *schemas.Evidence
		want schemas.Value
	}{
		{name: "sum of rows", ev: &schemas.Evidence{Cost: []schemas.CostRow{priced(0.25), priced(0.5), priced(0.125)}}, want: schemas.Observed(0.875, gw)},
		{
			name: "injected fault priced zero still counts as a row",
			ev:   &schemas.Evidence{Cost: []schemas.CostRow{priced(0.25), {RequestID: "r", Attempt: 2, FaultInject: true, USD: schemas.Observed(0, gw)}}},
			want: schemas.Observed(0.25, gw),
		},
		{
			name: "bifrost cost is never summed",
			ev:   &schemas.Evidence{Cost: []schemas.CostRow{{RequestID: "r", USD: schemas.Observed(0.1, gw), BifrostUSD: &bifrost}}},
			want: schemas.Observed(0.1, gw),
		},
		{
			name: "not applicable row adds nothing",
			ev:   &schemas.Evidence{Cost: []schemas.CostRow{priced(0.25), {RequestID: "r", USD: schemas.NotApplicable()}}},
			want: schemas.Observed(0.25, gw),
		},
		{
			name: "one unknown row with bounds gives bounded unknown",
			ev:   &schemas.Evidence{Cost: []schemas.CostRow{priced(0.25), priced(0.5), boundedRow(0, 0.1)}},
			want: schemas.UnknownWithin("1 of 3 cost rows unpriced", gw, 0.75, 0.85),
		},
		{
			name: "two bounded unknown rows add their upper bounds",
			ev:   &schemas.Evidence{Cost: []schemas.CostRow{priced(1), boundedRow(0.1, 0.2), boundedRow(0, 0.3)}},
			want: schemas.UnknownWithin("2 of 3 cost rows unpriced", gw, 1, 1.5),
		},
		{
			name: "one unbounded unknown row drops bounds",
			ev:   &schemas.Evidence{Cost: []schemas.CostRow{priced(0.25), boundedRow(0, 0.1), unpriced()}},
			want: schemas.Unknown("2 of 3 cost rows unpriced", gw),
		},
		{name: "no rows and no requests", ev: &schemas.Evidence{}, want: schemas.Unknown("no cost evidence", gw)},
		// 5-A-08: a complete, empty gateway witness is an observed zero (a
		// noop or oracle control); an incomplete one stays unknown.
		{name: "no rows and complete empty log", ev: &schemas.Evidence{Requests: completeLog()}, want: schemas.Observed(0, gw)},
		{name: "no rows and incomplete empty log", ev: &schemas.Evidence{Requests: &schemas.RequestLog{}}, want: schemas.Unknown("no cost evidence", gw)},
		{name: "no rows but requests", ev: &schemas.Evidence{Requests: completeLog(okMsg("a"), okMsg("b"))}, want: schemas.Unknown("no cost rows for 2 gateway requests", gw)},
		{name: "nil evidence", ev: nil, want: schemas.Unknown("no cost evidence", gw)},
	}
	m := newCostUSDMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checkValue(t, compute(t, m, nil, tt.ev), tt.want)
		})
	}
}

func TestNoCostReason(t *testing.T) {
	tests := []struct {
		name string
		log  *schemas.RequestLog
		want string
	}{
		{name: "nil log", log: nil, want: "no cost evidence"},
		{name: "empty log", log: completeLog(), want: "no cost evidence"},
		{name: "incomplete log with rows", log: &schemas.RequestLog{Requests: []schemas.GatewayRequest{okMsg("a")}}, want: "no cost rows for 1 gateway requests"},
		{name: "empty log that says why", log: &schemas.RequestLog{Reason: "model calls go outside this gateway"}, want: "model calls go outside this gateway"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := noCostReason(tt.log); got != tt.want {
				t.Errorf("noCostReason = %q, want %q", got, tt.want)
			}
		})
	}
}

// A priced prefix of an incomplete gateway log is not the run's spend
// (audit 1-C-05).
func TestCostUSDIncompleteLog(t *testing.T) {
	usd := schemas.Observed(0.03, schemas.EvidenceGateway)
	ev := &schemas.Evidence{
		Requests: &schemas.RequestLog{Complete: false, Reason: "rows still missing"},
		Cost:     []schemas.CostRow{{RequestID: "r1", USD: usd}},
	}
	got, err := newCostUSDMetric().Compute(context.Background(), nil, ev)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != schemas.ValueUnknown || !strings.Contains(got.Reason, "lower bound") {
		t.Fatalf("cost_usd = %+v, want unknown with a lower bound reason", got)
	}
	ev.Requests.Complete = true
	if got, _ := newCostUSDMetric().Compute(context.Background(), nil, ev); got.State != schemas.ValueObserved {
		t.Fatalf("complete log: cost_usd = %+v, want observed", got)
	}
}
