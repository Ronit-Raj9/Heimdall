package handlers

import (
	"cmp"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// BuildGrid lays out every counted slot of the manifest with its counted run.
// Each row's diff is mean(second arm) minus mean(first arm) over the cells
// with an observed score; unknown cells are counted apart, never as 0. Rows
// whose arms differ (discordant) come first, then the primary condition, then
// condition and task order. It is pure.
func BuildGrid(m *schemas.Manifest, runs []RunView) schemas.Grid {
	counted := countedRuns(runs)
	inFlight := map[string]bool{}
	for _, rv := range runs {
		if !rv.Sealed {
			inFlight[rv.Slot.SlotID] = true
		}
	}
	g := schemas.Grid{ExpID: m.ExpID, Arms: armIDs(m), Conditions: []string{}}
	type rowKey struct{ task, cond string }
	rows := map[rowKey]*schemas.GridRow{}
	var order []rowKey
	for _, s := range m.Slots {
		if !s.Counted {
			continue
		}
		k := rowKey{s.Task, s.Condition}
		row, ok := rows[k]
		if !ok {
			row = &schemas.GridRow{Task: s.Task, Condition: s.Condition}
			rows[k] = row
			order = append(order, k)
			if !slices.Contains(g.Conditions, s.Condition) {
				g.Conditions = append(g.Conditions, s.Condition)
			}
		}
		rv, ok := counted[s.SlotID]
		row.Cells = append(row.Cells, gridCell(s, rv, ok, inFlight[s.SlotID]))
		g.Trials = max(g.Trials, s.Trial)
	}
	g.Rows = make([]schemas.GridRow, 0, len(order))
	for _, k := range order {
		row := rows[k]
		slices.SortFunc(row.Cells, func(x, y schemas.GridCell) int {
			return cmp.Or(cmp.Compare(x.Arm, y.Arm), cmp.Compare(x.Trial, y.Trial))
		})
		row.Diff = rowDiff(row.Task, g.Arms, row.Cells)
		row.Discordant = row.Diff.NA > row.Diff.UnknownA && row.Diff.NB > row.Diff.UnknownB && row.Diff.D != 0
		g.Rows = append(g.Rows, *row)
	}
	primary := m.Experiment.Primary.Condition
	slices.SortStableFunc(g.Rows, func(x, y schemas.GridRow) int {
		return cmp.Or(
			compareTrueFirst(x.Discordant, y.Discordant),
			compareTrueFirst(x.Condition == primary, y.Condition == primary),
			cmp.Compare(slices.Index(g.Conditions, x.Condition), slices.Index(g.Conditions, y.Condition)),
			cmp.Compare(x.Task, y.Task),
		)
	})
	return g
}

// armIDs returns the manifest's arms in sorted order. The compatibility
// agent is not an arm.
func armIDs(m *schemas.Manifest) []string {
	arms := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		arms = append(arms, id)
	}
	slices.Sort(arms)
	return arms
}

// gridCell builds one cell from a slot and its counted run. Run id and
// outcome stay null until the slot has a counted run; until then y is
// unknown, "not_sealed" while a run is in flight and "no_run" before.
func gridCell(s schemas.Slot, rv RunView, counted, inFlight bool) schemas.GridCell {
	c := schemas.GridCell{SlotID: s.SlotID, PairID: s.PairID, Arm: s.Arm, Trial: s.Trial}
	switch {
	case !counted && inFlight:
		c.Y = schemas.Unknown("not_sealed", schemas.EvidenceOutcome)
		return c
	case !counted:
		c.Y = schemas.Unknown("no_run", schemas.EvidenceOutcome)
		return c
	}
	runID, outcome := rv.Run.RunID, rv.Run.Outcome
	c.RunID = &runID
	if outcome != "" {
		c.Outcome = &outcome
	}
	c.Y = runScore(rv)
	return c
}

// runScore is the run's y. Only a sealed run counts; an unsealed run is in
// flight and stays unknown.
func runScore(rv RunView) schemas.Value {
	if !rv.Sealed {
		return schemas.Unknown("not_sealed", schemas.EvidenceOutcome)
	}
	if rv.Run.Y.State != "" {
		return rv.Run.Y
	}
	if y, ok := rv.Run.Outcome.Score(); ok {
		return schemas.Observed(y, schemas.EvidenceOutcome)
	}
	return schemas.Unknown(string(rv.Run.Outcome), schemas.EvidenceOutcome)
}

// rowDiff summarizes a row as a TaskDiff between the first two arms. Means
// are over observed scores only; NA and NB count every slot of the arm.
func rowDiff(task string, arms []string, cells []schemas.GridCell) schemas.TaskDiff {
	d := schemas.TaskDiff{Task: task}
	if len(arms) < 2 {
		return d
	}
	var sumA, sumB float64
	for _, c := range cells {
		y, observed := c.Y.Float()
		switch c.Arm {
		case arms[0]:
			d.NA++
			if observed {
				sumA += y
			} else {
				d.UnknownA++
			}
		case arms[1]:
			d.NB++
			if observed {
				sumB += y
			} else {
				d.UnknownB++
			}
		}
	}
	if n := d.NA - d.UnknownA; n > 0 {
		d.MeanA = sumA / float64(n)
	}
	if n := d.NB - d.UnknownB; n > 0 {
		d.MeanB = sumB / float64(n)
	}
	d.D = d.MeanB - d.MeanA
	return d
}

// compareTrueFirst orders true before false.
func compareTrueFirst(x, y bool) int {
	switch {
	case x == y:
		return 0
	case x:
		return -1
	default:
		return 1
	}
}
