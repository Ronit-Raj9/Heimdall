package handlers

import (
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBuildGrid(t *testing.T) {
	idx := testIndex()
	m := idx.manifests[testExp]
	t.Run("discordant first", func(t *testing.T) {
		g := BuildGrid(m, idx.Runs(testExp))
		if len(g.Rows) != 2 || g.Rows[0].Task != "t01" || !g.Rows[0].Discordant || g.Rows[1].Discordant {
			t.Fatalf("rows = %+v", g.Rows)
		}
		if d := g.Rows[0].Diff; d.D != 1 || d.NA != 1 || d.NB != 1 {
			t.Errorf("t01 diff = %+v, want d 1", d)
		}
		if len(g.Arms) != 2 || g.Trials != 1 || len(g.Conditions) != 1 {
			t.Errorf("grid header = %+v", g)
		}
	})
	t.Run("missing and unsealed runs stay unknown", func(t *testing.T) {
		runs := idx.Runs(testExp)
		runs[0].Sealed = false      // t01 A in flight
		g := BuildGrid(m, runs[:3]) // t02 B has no run
		for _, row := range g.Rows {
			for _, c := range row.Cells {
				switch c.SlotID {
				case "s_t01_A": // in flight: no counted run yet
					if c.Y.State != schemas.ValueUnknown || c.Y.Reason != "not_sealed" || c.RunID != nil || c.Outcome != nil {
						t.Errorf("unsealed cell = %+v", c)
					}
				case "s_t02_B":
					if c.Y.State != schemas.ValueUnknown || c.Y.Reason != "no_run" || c.RunID != nil || c.Outcome != nil {
						t.Errorf("planned cell = %+v", c)
					}
				}
			}
			if row.Discordant {
				t.Errorf("row %s discordant with unknown cells: %+v", row.Task, row.Diff)
			}
		}
	})
	t.Run("retry after aborted_infra is the counted run", func(t *testing.T) {
		runs := idx.Runs(testExp)
		runs[0].Run.Outcome = schemas.OutcomeAbortedInfra
		retry := runs[0]
		retry.Run.Attempt, retry.Run.RunID, retry.Run.Outcome = 2, "r_retry", schemas.OutcomePass
		retry.Run.Y = schemas.Observed(1, schemas.EvidenceOutcome)
		g := BuildGrid(m, append([]RunView{retry}, runs...))
		for _, row := range g.Rows {
			for _, c := range row.Cells {
				if c.SlotID == "s_t01_A" && (c.RunID == nil || *c.RunID != "r_retry") {
					t.Errorf("cell = %+v, want r_retry", c)
				}
			}
		}
	})
}

// TestArmIDsAAArmLast checks that the A/A arm sorts after B only in an
// experiment with aa: true (Eval integrity area, S2).
func TestArmIDsAAArmLast(t *testing.T) { // Eval integrity area (S2)
	arms := map[string]schemas.ArmManifest{"A": {}, "A2": {}, "B": {}} // Eval integrity area (S2)
	tests := []struct {                                                // Eval integrity area (S2)
		name string // Eval integrity area (S2)
		aa   bool   // Eval integrity area (S2)
		want string // Eval integrity area (S2)
	}{ // Eval integrity area (S2)
		{"aa: A2 last", true, "A,B,A2,"},               // Eval integrity area (S2)
		{"declared A2 stays sorted", false, "A,A2,B,"}, // Eval integrity area (S2)
	} // Eval integrity area (S2)
	for _, tt := range tests { // Eval integrity area (S2)
		t.Run(tt.name, func(t *testing.T) { // Eval integrity area (S2)
			m := &schemas.Manifest{Arms: arms, Experiment: schemas.Experiment{AA: tt.aa}} // Eval integrity area (S2)
			got := ""                                                                     // Eval integrity area (S2)
			for _, a := range armIDs(m) {                                                 // Eval integrity area (S2)
				got += a + "," // Eval integrity area (S2)
			} // Eval integrity area (S2)
			if got != tt.want { // Eval integrity area (S2)
				t.Fatalf("armIDs = %s, want %s", got, tt.want) // Eval integrity area (S2)
			} // Eval integrity area (S2)
		}) // Eval integrity area (S2)
	} // Eval integrity area (S2)
} // Eval integrity area (S2)
