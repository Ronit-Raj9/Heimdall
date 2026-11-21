package metrics

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/voice"
)

// TestVoiceCostWitnessedFreeCall covers QC2-24-04: a complete request log
// with no request is an observed zero per minute, as cost_usd reads it.
func TestVoiceCostWitnessedFreeCall(t *testing.T) {
	ev := &schemas.Evidence{Requests: &schemas.RequestLog{Complete: true, Requests: []schemas.GatewayRequest{}}}
	v := computeVoiceCost(nil, ev, &voice.Analysis{CallMS: 60000})
	if f, ok := v.Float(); !ok || f != 0 {
		t.Fatalf("voice cost %+v, want observed 0", v)
	}
	ev.Requests.Complete = false
	if _, ok := computeVoiceCost(nil, ev, &voice.Analysis{CallMS: 60000}).Float(); ok {
		t.Fatal("an incomplete log was read as a free call")
	}
}
