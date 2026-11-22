package leaderboard

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func replayLedger(t *testing.T, entries ...schemas.LedgerEntry) []byte {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// A debug replay answers every model call from the recording: its ledger
// lines are passed with upstream "replay". Such a run is "replayed", not
// "unobserved" (row N27); one real call among them still makes it "model".
func TestLedgerProvenanceReplayed(t *testing.T) {
	replayed := schemas.LedgerEntry{Kind: "message", Action: "pass", Status: 200, Upstream: schemas.LedgerUpstreamReplay}
	got, err := LedgerProvenance(replayLedger(t, replayed, replayed))
	if err != nil || got != ProvenanceReplayed {
		t.Fatalf("all replayed: %q, %v; want %q", got, err, ProvenanceReplayed)
	}
	real := schemas.LedgerEntry{Kind: "message", Action: "pass", Status: 200, Upstream: schemas.LedgerUpstreamReal}
	if got, _ := LedgerProvenance(replayLedger(t, real, replayed)); got != ProvenanceModel {
		t.Fatalf("real plus replayed: %q, want %q", got, ProvenanceModel)
	}
	if got, _ := LedgerProvenance(nil); got != ProvenanceUnobserved {
		t.Fatalf("empty ledger: %q, want %q", got, ProvenanceUnobserved)
	}
}
