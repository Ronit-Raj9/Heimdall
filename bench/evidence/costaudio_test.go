package evidence

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X8 voice agents: speech, transcription and Realtime rows are priced at
// Bifrost's own row cost; a row without one is unknown, an injected outage
// costs nothing.
func TestCostRowsAudio(t *testing.T) {
	usd := func(v float64) *float64 { return &v }
	speech := schemas.GatewayRequest{ID: "s1", Timestamp: t0, Status: "success", Kind: kindAudio, ModelRequested: "gpt-4o-mini-tts", BifrostUSD: usd(0.000018)}
	stt := schemas.GatewayRequest{ID: "s2", Timestamp: t0.Add(1), Status: "success", Kind: kindAudio, ModelRequested: "whisper-1", BifrostUSD: usd(0.0001)}
	rt := schemas.GatewayRequest{ID: "s3", Timestamp: t0.Add(2), Status: "success", Kind: kindAudio, ModelRequested: "gpt-realtime", BifrostUSD: usd(0.000136),
		Tokens: &schemas.Tokens{Fresh: 10, Out: 6}}
	noPrice := schemas.GatewayRequest{ID: "s4", Timestamp: t0.Add(3), Status: "success", Kind: kindAudio, ModelRequested: "whisper-1"}
	outage := schemas.GatewayRequest{ID: "s5", Timestamp: t0.Add(4), Status: "error", ErrorStatus: 503, Kind: kindAudio, ModelRequested: "whisper-1"}
	rows := CostRows(&schemas.RequestLog{Requests: []schemas.GatewayRequest{speech, stt, rt, noPrice, outage}}, nil, nil)
	want := []string{"s1#1 0.000018", "s2#1 0.000100", "s3#1 0.000136", "s4#1 unknown", "s5#1 0.000000 inject"}
	if len(rows) != len(want) {
		t.Fatalf("rows %d", len(rows))
	}
	for i, r := range rows {
		if got := describeRow(r); got != want[i] {
			t.Errorf("row %d = %q, want %q", i, got, want[i])
		}
	}
	if rows[0].PriceRef != audioPriceRef || rows[2].Tokens.Fresh != 10 {
		t.Fatalf("price ref %q tokens %+v", rows[0].PriceRef, rows[2].Tokens)
	}
}
