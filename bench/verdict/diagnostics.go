package verdict

import "github.com/maximhq/bifrost/bench/schemas"

// axisSolve is the diagnostics axis of the clean condition.
const axisSolve = "solve"

// diagnostics returns one row per condition that ran (DESIGN.md 9.6), in the
// experiment's declared order, then any undeclared condition by id. Each row
// carries per-arm success on the primary metric, the B-minus-A effect on that
// metric, and receipt step ratios. Only the primary condition's effect carries
// a p-value; every other row is descriptive (DESIGN.md 9.2).
//
// The manifest does not carry probe specs, so a probe row's axis falls back to
// the probe id until the manifest records it.
func diagnostics(m *schemas.Manifest, idx runIndex, primary schemas.Endpoint) []schemas.DiagnosticsRow {
	conds := conditionOrder(m, idx)
	rows := make([]schemas.DiagnosticsRow, 0, len(conds))
	for _, cond := range conds {
		isPrimary := cond == primary.Condition
		row := schemas.DiagnosticsRow{
			Condition:   cond,
			Axis:        axisFor(cond),
			NTasks:      len(idx[cond]),
			PerArm:      map[string]schemas.Ratio{},
			Descriptive: !isPrimary,
		}
		for _, arm := range armsIn(idx, cond) {
			row.PerArm[arm] = successRatio(idx.armRuns(cond, arm), primary.Metric)
		}
		eff := pairedEffect(cond, primary.Metric, ArmBaseline, ArmCandidate,
			idx.cells(cond, ArmBaseline, primary.Metric), idx.cells(cond, ArmCandidate, primary.Metric), isPrimary)
		if eff.LowerBound.NTasks > 0 {
			row.Effect = eff
		}
		row.Receipts = receiptRatios(idx, cond)
		rows = append(rows, row)
	}
	return rows
}

// conditionOrder lists the conditions with counted runs: declared ones first,
// in declaration order, then the rest sorted.
func conditionOrder(m *schemas.Manifest, idx runIndex) []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range m.Experiment.Conditions {
		if _, ran := idx[c.ID]; ran && !seen[c.ID] {
			seen[c.ID] = true
			out = append(out, c.ID)
		}
	}
	for _, c := range sortedKeys(idx) {
		if !seen[c] {
			out = append(out, c)
		}
	}
	return out
}

// axisFor returns the diagnostics axis of a condition.
func axisFor(condition string) string {
	if condition == schemas.ConditionClean {
		return axisSolve
	}
	return condition
}

// receiptRatios counts, per arm and receipt step, the runs where the step
// happened over the runs where it applies, with unknowns kept apart. Steps
// that never apply are left out, and so is the whole map for clean.
func receiptRatios(idx runIndex, condition string) map[string]map[schemas.ReceiptStep]schemas.Ratio {
	out := map[string]map[schemas.ReceiptStep]schemas.Ratio{}
	for _, arm := range armsIn(idx, condition) {
		steps := map[schemas.ReceiptStep]schemas.Ratio{}
		for _, r := range idx.armRuns(condition, arm) {
			for _, rc := range r.Receipts {
				if rc.ProbeID != "" && rc.ProbeID != condition {
					continue
				}
				for step, entry := range rc.Steps {
					countStep(steps, step, entry.State)
				}
			}
		}
		if len(steps) > 0 {
			out[arm] = steps
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// countStep adds one receipt entry to its step's ratio. A blank or
// unrecognised state counts as unknown, never as no.
func countStep(steps map[schemas.ReceiptStep]schemas.Ratio, step schemas.ReceiptStep, state schemas.StepState) {
	if state == schemas.StepNotApplicable {
		return
	}
	r := steps[step]
	r.Den++
	switch state {
	case schemas.StepYes:
		r.Num++
	case schemas.StepNo:
	default:
		r.Unknown++
	}
	steps[step] = r
}
