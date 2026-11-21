package evidence

import (
	"math"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestRepriceCost(t *testing.T) {
	tok := schemas.Tokens{Fresh: 1_000_000, Out: 500_000, CacheRead: 200_000}
	unpriced := schemas.CostRow{Model: "xcodebest/coder-1", Tokens: tok, USD: schemas.UnknownWithin(`no price for model "xcodebest/coder-1" in snapshot s1`, schemas.EvidenceGateway, 0, 99)}
	priced := schemas.CostRow{Model: "gpt-x", USD: schemas.Observed(0.25, schemas.EvidenceGateway)}
	na := schemas.CostRow{USD: schemas.NotApplicable()}
	noUsage := schemas.CostRow{Model: "xcodebest/coder-1", USD: schemas.Unknown("usage not recorded", schemas.EvidenceGateway)}
	price := ModelPrice{InputPerMTok: 1, OutputPerMTok: 4, CacheReadPerMTok: 0.5, CacheWritePerMTok: 1}
	lookup := func(m string) (ModelPrice, bool) { return price, m == "xcodebest/coder-1" }

	v, ok := RepriceCost([]schemas.CostRow{unpriced, priced, na}, lookup)
	got, known := v.Float()
	if !ok || !known || math.Abs(got-(1+2+0.1+0.25)) > 1e-9 || v.Reason == "" {
		t.Fatalf("reprice = %+v %v, want 3.35 observed with a reason", v, ok)
	}
	tests := []struct {
		name   string
		rows   []schemas.CostRow
		lookup func(string) (ModelPrice, bool)
	}{
		{"usage not recorded stays unknown", []schemas.CostRow{unpriced, noUsage}, lookup},
		{"no key price for the model", []schemas.CostRow{unpriced}, func(string) (ModelPrice, bool) { return ModelPrice{}, false }},
		{"nothing to reprice", []schemas.CostRow{priced}, lookup},
		{"no lookup", []schemas.CostRow{unpriced}, nil},
	}
	for _, tt := range tests {
		if _, ok := RepriceCost(tt.rows, tt.lookup); ok {
			t.Errorf("%s: repriced, want unchanged", tt.name)
		}
	}
}
