package findings

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSetOrigin(t *testing.T) {
	no, yes := false, true
	t0 := time.Unix(1000, 0)
	rows := []RunRow{
		{Experiment: "e_old", ExperimentCreated: t0, RunID: "r1", Arm: "A", Passed: &no},
		{Experiment: "e_new", ExperimentCreated: t0.Add(time.Hour), RunID: "r2", Arm: "B", Passed: &no},
		{Experiment: "e_newest", ExperimentCreated: t0.Add(2 * time.Hour), RunID: "r3", Arm: "A", Passed: &yes},
		{Experiment: "e_other", ExperimentCreated: t0.Add(3 * time.Hour), RunID: "rx", Arm: "A", Passed: &no},
	}
	f := &Finding{Runs: []string{"r1", "r2", "r3"}}
	SetOrigin(f, rows)
	if f.Experiment != "e_new" || f.Arm != "B" {
		t.Fatalf("got %q %q", f.Experiment, f.Arm)
	}
	// Two arms failed in the same experiment: the arm is not known.
	rows = append(rows, RunRow{Experiment: "e_new", ExperimentCreated: t0.Add(time.Hour), RunID: "r4", Arm: "A", Passed: &no})
	f = &Finding{Runs: []string{"r2", "r4"}}
	SetOrigin(f, rows)
	if f.Experiment != "e_new" || f.Arm != "" {
		t.Fatalf("two arms: %q %q", f.Experiment, f.Arm)
	}
	// Already set, or nothing failed: unchanged.
	f = &Finding{Runs: []string{"r1"}, Experiment: "e_keep"}
	SetOrigin(f, rows)
	if f.Experiment != "e_keep" || f.Arm != "" {
		t.Fatalf("kept: %+v", f)
	}
	f = &Finding{Runs: []string{"r3"}}
	SetOrigin(f, rows)
	if f.Experiment != "" {
		t.Fatalf("passed only: %+v", f)
	}
	SetOrigin(nil, rows)
}

func TestOriginFieldsOmittedWhenEmpty(t *testing.T) {
	b, _ := json.Marshal(Finding{})
	if strings.Contains(string(b), `"arm"`) || strings.Contains(string(b), `"experiment"`) {
		t.Fatalf("old shape changed: %s", b)
	}
	b, _ = json.Marshal(Finding{Experiment: "e1", Arm: "B"})
	if !strings.Contains(string(b), `"experiment":"e1"`) || !strings.Contains(string(b), `"arm":"B"`) {
		t.Fatalf("fields missing: %s", b)
	}
}
