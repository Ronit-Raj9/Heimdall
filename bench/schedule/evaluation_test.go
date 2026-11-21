package schedule

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func writeEval(t *testing.T, root, id string, budget float64) {
	t.Helper()
	dir := filepath.Join(root, ".heimdall", "evaluations")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"id":"` + id + `","spec":{"name":"nightly","mode":"score","arms":[{"label":"A","agent":"codex","model":"m"}],` +
		`"benchmarks":["hd-a"],"tasks_per_benchmark":4,"trials":1,"faults":["clean"],"budget_usd":` + strconv.FormatFloat(budget, 'f', -1, 64) +
		`},"status":"done","children":[],"created_at":"2026-11-20T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadEvaluationSpec(t *testing.T) {
	root := t.TempDir()
	writeEval(t, root, "ev_20261120_000000_abc", 12)
	spec, err := LoadEvaluationSpec(root, "ev_20261120_000000_abc")
	if err != nil || spec.BudgetUSD != 12 || spec.Arms[0].Agent != "codex" {
		t.Fatalf("by id = %+v, %v", spec, err)
	}
	yml := "name: weekly\nmode: score\narms:\n  - label: A\n    agent: codex\n    model: m\nbenchmarks: [hd-a]\ntasks_per_benchmark: 3\ntrials: 1\nfaults: [clean]\n"
	if err := os.MkdirAll(filepath.Join(root, "evaluations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "evaluations", "weekly.yaml"), []byte(yml), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err = LoadEvaluationSpec(root, "evaluations/weekly.yaml")
	if err != nil || spec.TasksPerBenchmark != 3 || spec.Name != "weekly" {
		t.Fatalf("by file = %+v, %v", spec, err)
	}
	if err := os.WriteFile(filepath.Join(root, "evaluations", "bad.yaml"), []byte("nmae: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEvaluationSpec(root, "evaluations/bad.yaml"); err == nil {
		t.Error("an unknown key loaded")
	}
	if _, err := LoadEvaluationSpec(root, "ev_missing"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("missing id: %v", err)
	}
	if _, err := LoadEvaluationSpec(root, "../x.json"); err == nil {
		t.Error("a path outside the project loaded")
	}
}

func TestValidateEvaluationSchedule(t *testing.T) {
	root := t.TempDir()
	writeEval(t, root, "ev_20261120_000000_abc", 12)
	base := Schedule{Name: "nightly-codex", Cron: "0 2 * * *", Evaluation: "ev_20261120_000000_abc"}
	tests := []struct {
		name string
		mod  func(*Schedule)
		want string
	}{
		{name: "ok", mod: func(s *Schedule) { s.MaxUSDPerDay = 20 }},
		{name: "no cap", mod: func(*Schedule) {}, want: "max_usd_per_day is required"},
		{name: "budget above cap", mod: func(s *Schedule) { s.MaxUSDPerDay = 5 }, want: "above max_usd_per_day"},
		{name: "both", mod: func(s *Schedule) { s.MaxUSDPerDay = 20; s.Experiment = "experiments/x.yaml" }, want: "not both"},
		{name: "missing", mod: func(s *Schedule) { s.MaxUSDPerDay = 20; s.Evaluation = "ev_nope" }, want: "does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := base
			tt.mod(&s)
			err := s.Validate(root, nil)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestApproveEvaluation(t *testing.T) {
	root := t.TempDir()
	writeEval(t, root, "ev_20261120_000000_abc", 0)
	s := Schedule{Name: "nightly-codex", Cron: "0 2 * * *", Evaluation: "ev_20261120_000000_abc", MaxUSDPerDay: 8}
	d, err := New(&File{Schedules: []Schedule{s}}, Options{Root: root, StateDir: t.TempDir(), Runner: &fakeRunner{},
		Load: func(string) (*schemas.Experiment, error) { return nil, errors.New("not an experiment") }})
	if err != nil {
		t.Fatal(err)
	}
	a, reason := d.approve(s, "2026-11-20")
	if reason != "" || a == nil || a.CapUSD != 8 || !a.Limited {
		t.Fatalf("approve = %+v, %q; want the daily cap as the budget", a, reason)
	}
	if _, err := d.addAmount(s.Name, "2026-11-20", 8); err != nil {
		t.Fatal(err)
	}
	if _, reason := d.approve(s, "2026-11-20"); !strings.Contains(reason, "daily cap") {
		t.Fatalf("second approve reason = %q, want the daily cap", reason)
	}
}
