package findings

// SetOrigin fills f.Experiment and f.Arm from rows: the newest experiment
// (by ExperimentCreated) among the rows of f's runs that failed, and the arm
// of those runs in it. When the failed runs of that experiment are on more
// than one arm, Arm stays empty. A finding that already names its
// experiment, or has no failed run among rows, is left as it is.
func SetOrigin(f *Finding, rows []RunRow) {
	if f == nil || f.Experiment != "" {
		return
	}
	mine := map[string]bool{}
	for _, r := range f.Runs {
		mine[r] = true
	}
	var best *RunRow
	for i := range rows {
		r := &rows[i]
		if !mine[r.RunID] || r.Passed == nil || *r.Passed || r.Experiment == "" {
			continue
		}
		if best == nil || r.ExperimentCreated.After(best.ExperimentCreated) {
			best = r
		}
	}
	if best == nil {
		return
	}
	arm := best.Arm
	for _, r := range rows {
		if mine[r.RunID] && r.Experiment == best.Experiment && r.Passed != nil && !*r.Passed && r.Arm != arm {
			arm = ""
			break
		}
	}
	f.Experiment, f.Arm = best.Experiment, arm
}
