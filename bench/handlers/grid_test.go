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
				case "s_t01_A":
					if c.Y.State != schemas.ValueUnknown || c.RunID == nil {
						t.Errorf("unsealed cell = %+v", c)
					}
				case "s_t02_B":
					if c.Y.State != schemas.ValueUnknown || c.RunID != nil || c.Outcome != nil {
						t.Errorf("planned cell = %+v", c)
					}
				}
			}
			if row.Discordant {
				t.Errorf("row %s discordant with unknown cells: %+v", row.Task, row.Diff)
			}
		}
	})
	t.Run("latest attempt wins", func(t *testing.T) {
		runs := idx.Runs(testExp)
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
