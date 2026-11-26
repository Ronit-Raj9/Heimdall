package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestListRunsFailedOnly checks that failed=1 keeps every run that did not
// pass (a fail and an infrastructure error) and counts them in the total.
func TestListRunsFailedOnly(t *testing.T) {
	idx := runListIndex()
	for i := range idx.runs {
		if idx.runs[i].Run.RunID == "r_t02_B" {
			idx.runs[i].Run.Outcome = schemas.OutcomeInfraError
		}
	}
	c := runListClient(t, idx, runListSuites())
	out := getRunList(t, c, "?failed=1")
	if out.Total != 2 || len(out.Runs) != 2 {
		t.Fatalf("failed=1 total %d rows %d, want 2", out.Total, len(out.Runs))
	}
	for _, row := range out.Runs {
		if row.Outcome == schemas.OutcomePass {
			t.Errorf("failed=1 returned passing run %s", row.RunID)
		}
	}
	if out := getRunList(t, c, ""); out.Total != 4 {
		t.Errorf("without failed total %d, want 4", out.Total)
	}
}
