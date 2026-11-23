package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A schedule that runs an experiment file on the benchmark also keeps it
// from being deleted (fix wave 3, N288 follow-up), and the refusal names the
// schedule.
func TestDeleteSuiteRefusesExperimentSchedule(t *testing.T) {
	c := suiteDeleteClient(t, nil)
	createTestSuite(t, c, "zz-del")
	for _, d := range []string{"policies", "experiments"} {
		if err := os.MkdirAll(filepath.Join(c.root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(c.root, "experiments", "nightly.yaml"), []byte("id: nightly\nsuite: zz-del@1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.root, "experiments", "other.yaml"), []byte("id: other\nsuite: coding-core\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sched := "schedules:\n  - name: other\n    experiment: experiments/other.yaml\n    cron: \"0 2 * * *\"\n" +
		"  - name: nightly\n    experiment: experiments/nightly.yaml\n    cron: \"0 3 * * *\"\n"
	if err := os.WriteFile(filepath.Join(c.root, "policies", "schedules.yaml"), []byte(sched), 0o644); err != nil {
		t.Fatal(err)
	}
	code, body := c.do(t, "DELETE", "/suites/zz-del", nil, nil)
	if code != 409 || !strings.Contains(string(body), "Schedule nightly runs a comparison that uses zz-del. Remove or change the schedule first.") {
		t.Fatalf("experiment schedule: %d %s", code, body)
	}
	if _, err := os.Stat(filepath.Join(c.root, "benchmarks", "zz-del", "suite.yaml")); err != nil {
		t.Fatalf("refused delete removed files: %v", err)
	}
}
