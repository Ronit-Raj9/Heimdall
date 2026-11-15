package schemas

import (
	"encoding/json"
	"testing"
)

// TestForecastRangeNulls pins the wire form the UI reads: an unknown range
// carries null bounds, never zeros, and an upper bound only its high end.
func TestForecastRangeNulls(t *testing.T) {
	hi := 2.5
	tests := []struct {
		name string
		r    ForecastRange
		want string
	}{
		{"unknown", ForecastRange{Kind: RangeUnknown, Reason: "no history, cannot estimate"},
			`{"kind":"unknown","low":null,"median":null,"high":null,"reason":"no history, cannot estimate"}`},
		{"upper bound", ForecastRange{Kind: RangeUpperBound, High: &hi}, `{"kind":"upper_bound","low":null,"median":null,"high":2.5}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.r)
			if err != nil || string(raw) != tt.want {
				t.Fatalf("got %s (%v), want %s", raw, err, tt.want)
			}
		})
	}
}
