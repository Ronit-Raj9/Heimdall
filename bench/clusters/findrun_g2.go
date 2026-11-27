package clusters

// RunFailure is the failure group one run belongs to, for the run page
// ("the same failure in N runs"): the group's signature, its error text
// (the title when the group has none) and how many runs share it.
type RunFailure struct {
	Signature string `json:"failure_signature"`
	Text      string `json:"failure_text"`
	Count     int    `json:"failure_count"`
}

// FindRun returns the group of rep that holds runID, below Min included.
// ok is false when the run is in no group (it passed, or it was left out of
// the report). It is pure.
func FindRun(rep *Report, runID string) (RunFailure, bool) {
	if rep == nil || runID == "" {
		return RunFailure{}, false
	}
	for _, c := range rep.all {
		for _, r := range c.runs {
			if r.RunID == runID {
				text := c.ErrorText
				if text == "" {
					text = c.Title
				}
				return RunFailure{Signature: c.Signature, Text: text, Count: c.Count}, true
			}
		}
	}
	return RunFailure{}, false
}
