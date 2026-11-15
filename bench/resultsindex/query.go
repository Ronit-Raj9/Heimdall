package resultsindex

import (
	"fmt"
	"strings"
	"time"
)

// Synthetic filter values of a query.
const (
	SyntheticInclude = "include" // live and synthetic runs (the query default)
	SyntheticExclude = "exclude" // live runs only
	SyntheticOnly    = "only"    // synthetic runs only
)

// Filter selects run rows. Empty fields match everything; string fields
// match exactly. Since and Until bound the run's start time (Until is
// exclusive). Limit caps the rows returned, 0 for no cap.
type Filter struct {
	Experiment string
	RunID      string
	Suite      string
	Task       string
	Arm        string
	Agent      string
	Model      string
	Condition  string
	Outcome    string
	ConfigHash string
	Synthetic  string // include | exclude | only
	Since      time.Time
	Until      time.Time
	Limit      int
}

// ParseFilter builds a Filter from key=value terms, as `index query` takes
// them: experiment, run_id, suite, task, arm, agent, model, condition, outcome,
// config_hash, synthetic, since, until (RFC 3339 or YYYY-MM-DD).
func ParseFilter(terms []string) (Filter, error) {
	var f Filter
	for _, term := range terms {
		key, value, ok := strings.Cut(term, "=")
		if !ok || value == "" {
			return f, fmt.Errorf("filter %q is not key=value", term)
		}
		if err := f.set(strings.TrimSpace(key), strings.TrimSpace(value)); err != nil {
			return f, err
		}
	}
	return f, nil
}

// Match reports whether a run row passes the filter.
func (f *Filter) Match(r *RunRow) bool {
	pairs := [][2]string{
		{f.Experiment, r.Experiment}, {f.RunID, r.RunID}, {f.Suite, r.Suite}, {f.Task, r.Task}, {f.Arm, r.Arm},
		{f.Agent, r.Agent}, {f.Model, r.Model}, {f.Condition, r.Condition}, {f.Outcome, r.Outcome},
		{f.ConfigHash, r.ConfigHash},
	}
	for _, p := range pairs {
		if p[0] != "" && p[0] != p[1] {
			return false
		}
	}
	switch f.Synthetic {
	case SyntheticExclude:
		if r.Synthetic {
			return false
		}
	case SyntheticOnly:
		if !r.Synthetic {
			return false
		}
	}
	if !f.Since.IsZero() && r.Started.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && !r.Started.Before(f.Until) {
		return false
	}
	return true
}

// set assigns one filter term.
func (f *Filter) set(key, value string) error {
	fields := map[string]*string{
		"experiment": &f.Experiment, "run_id": &f.RunID, "suite": &f.Suite, "task": &f.Task, "arm": &f.Arm, "agent": &f.Agent,
		"model": &f.Model, "condition": &f.Condition, "outcome": &f.Outcome, "config_hash": &f.ConfigHash,
	}
	if p, ok := fields[key]; ok {
		*p = value
		return nil
	}
	switch key {
	case "synthetic":
		if value != SyntheticInclude && value != SyntheticExclude && value != SyntheticOnly {
			return fmt.Errorf("synthetic %q is not include, exclude or only", value)
		}
		f.Synthetic = value
	case "since", "until":
		t, err := parseTime(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if key == "since" {
			f.Since = t
		} else {
			f.Until = t
		}
	default:
		return fmt.Errorf("unknown filter %q", key)
	}
	return nil
}

// parseTime reads RFC 3339 or a plain date (midnight UTC).
func parseTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%q is not RFC 3339 or YYYY-MM-DD", s)
	}
	return t, nil
}
