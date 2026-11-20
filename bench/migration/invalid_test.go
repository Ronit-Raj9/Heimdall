package migration

import (
	"strings"
	"testing"
)

// Audit 5-C-01: an INVALID release verdict (a model_fidelity gate fired in
// arm B) withholds the migrate recommendation the statistics alone give.
func TestInvalidReleaseVerdictWithholdsRecommendation(t *testing.T) {
	in := fixture()
	if a := pairTo(t, build(t, in, Query{From: "old-model"}), "B").Recommendation.Action; a != ActionMigrate {
		t.Fatalf("baseline A to B = %s, want migrate", a)
	}
	in.Invalid = []string{"gate model_fidelity fired in 1 of 8 runs (12.5%) of arm B, above 5%"}
	rec := pairTo(t, build(t, in, Query{From: "old-model"}), "B").Recommendation
	if rec.Action != ActionNotEnough || !strings.Contains(rec.Reason, "model_fidelity") {
		t.Fatalf("invalid release verdict recommendation %+v", rec)
	}
}
