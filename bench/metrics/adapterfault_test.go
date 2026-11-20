package metrics

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// An operation already in flight when the fault fired is not a retry; only a
// later decision's own completion recovers the fault (audit 1-C-08).
func TestAdapterFaultRecoveryJoinsByOperation(t *testing.T) {
	entry := func(op, phase, mode string, ok bool) schemas.AdapterFaultEntry {
		return schemas.AdapterFaultEntry{Session: "s1", OperationID: op, Kind: "tool", Name: "search", Phase: phase, Mode: mode, Success: ok}
	}
	cases := []struct {
		name    string
		entries []schemas.AdapterFaultEntry
		want    float64
	}{
		{"earlier in-flight completion", []schemas.AdapterFaultEntry{
			entry("early", "decision", "pass", false), entry("fault", "decision", "error", false),
			entry("fault", "applied", "", false), entry("early", "completed", "", true)}, 0},
		{"retry after the fault", []schemas.AdapterFaultEntry{
			entry("fault", "decision", "error", false), entry("fault", "applied", "", false),
			entry("retry", "decision", "pass", false), entry("retry", "completed", "", true)}, 1},
	}
	for _, c := range cases {
		ev := &schemas.Evidence{AdapterFault: &schemas.AdapterFaultEvidence{Complete: true, Entries: c.entries}}
		got, err := (&adapterFaultRecoveryMetric{}).Compute(context.Background(), nil, ev)
		if err != nil {
			t.Fatal(err)
		}
		if x, ok := got.Float(); !ok || x != c.want {
			t.Fatalf("%s: got %+v, want %v", c.name, got, c.want)
		}
	}
}
