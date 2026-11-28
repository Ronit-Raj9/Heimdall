package schedule

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/schemas"
)

// project writes an experiment file and a policy file under a temp root and
// returns the root and a loader that serves the given experiments by file
// name.
func project(t *testing.T, exps map[string]schemas.Experiment) (string, ExperimentLoader) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "experiments"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name := range exps {
		if err := os.WriteFile(filepath.Join(root, "experiments", name), []byte("name: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "policy.yaml"), []byte("name: p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	load := func(p string) (*schemas.Experiment, error) {
		e, ok := exps[filepath.Base(p)]
		if !ok {
			return nil, errors.New("no such experiment")
		}
		return &e, nil
	}
	return root, load
}

func TestScheduleValidate(t *testing.T) {
	root, load := project(t, map[string]schemas.Experiment{
		"live.yaml":   {Budget: schemas.BudgetSpec{TotalUSD: 5}},
		"nocap.yaml":  {},
		"smoke.yaml":  {Smoke: true},
		"bigcap.yaml": {Budget: schemas.BudgetSpec{TotalUSD: 50}},
	})
	ok := Schedule{Name: "nightly", Experiment: "experiments/live.yaml", Cron: "0 2 * * *", MaxUSDPerDay: 10}
	tests := []struct {
		name   string
		mutate func(*Schedule)
		want   string
	}{
		{"valid", func(*Schedule) {}, ""},
		{"smoke needs no cap", func(s *Schedule) { s.Experiment, s.MaxUSDPerDay = "experiments/smoke.yaml", 0 }, ""},
		{"smoke with daily cap needs spend cap", func(s *Schedule) { s.Experiment = "experiments/smoke.yaml" }, "no spend cap"},
		{"with policy and tz", func(s *Schedule) { s.Policy, s.Timezone = "policy.yaml", "Asia/Kolkata" }, ""},
		{"bad name", func(s *Schedule) { s.Name = "Bad Name" }, "name must be"},
		{"bad cron", func(s *Schedule) { s.Cron = "0 2 * *" }, "want 5 fields"},
		{"never fires", func(s *Schedule) { s.Cron = "0 0 30 2 *" }, "never fires"},
		{"bad tz", func(s *Schedule) { s.Timezone = "Mars/Olympus" }, "time zone"},
		{"missing experiment", func(s *Schedule) { s.Experiment = "experiments/none.yaml" }, "experiment file"},
		{"missing policy", func(s *Schedule) { s.Policy = "nope.yaml" }, "policy file"},
		{"non-smoke without daily cap", func(s *Schedule) { s.MaxUSDPerDay = 0 }, "max_usd_per_day is required"},
		{"negative cap", func(s *Schedule) { s.MaxUSDPerDay = -1 }, "negative"},
		{"experiment without spend cap", func(s *Schedule) { s.Experiment = "experiments/nocap.yaml" }, "no spend cap"},
		{"experiment cap above daily cap", func(s *Schedule) { s.Experiment = "experiments/bigcap.yaml" }, "no run could ever start"},
		{"bad fail_on", func(s *Schedule) { s.FailOn = []string{"bad"} }, "not a verdict decision"},
		{"literal URL alert", func(s *Schedule) { s.Alerts = []alerts.Config{{URLEnv: "https://x.example/hook"}} }, "never in the file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := ok
			tt.mutate(&s)
			err := s.Validate(root, load)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("Validate = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestFileRoundTrip(t *testing.T) {
	root, load := project(t, map[string]schemas.Experiment{"live.yaml": {Budget: schemas.BudgetSpec{TotalUSD: 5}}})
	p := filepath.Join(root, DefaultFile)
	f, err := Load(p)
	if err != nil || len(f.Schedules) != 0 {
		t.Fatalf("missing file: %+v, %v", f, err)
	}
	f.Alerts = []alerts.Config{{URLEnv: "BENCH_ALERT_URL", Format: alerts.FormatSlack}}
	s := Schedule{Name: "nightly", Experiment: "experiments/live.yaml", Cron: "0 2 * * *", MaxUSDPerDay: 10,
		Alerts: []alerts.Config{{URLEnv: "TEAM_HOOK", SecretEnv: "TEAM_SECRET", Events: []string{alerts.EventVerdictRegressed}}}}
	if err := f.Add(s); err != nil {
		t.Fatal(err)
	}
	if err := f.Add(s); err == nil {
		t.Fatal("duplicate name added")
	}
	if err := f.Validate(root, load); err != nil {
		t.Fatal(err)
	}
	if err := Save(p, f); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Schedules) != 1 || got.Schedules[0].MaxUSDPerDay != 10 || got.Schedules[0].Alerts[0].SecretEnv != "TEAM_SECRET" || len(got.Alerts) != 1 {
		t.Fatalf("round trip = %+v", got)
	}
	if n := len(got.AlertsFor(got.Schedules[0], []alerts.Config{{URLEnv: "SERVER_HOOK"}})); n != 3 {
		t.Fatalf("AlertsFor = %d receivers, want 3", n)
	}
	if err := got.Remove("nightly"); err != nil {
		t.Fatal(err)
	}
	if err := got.Remove("nightly"); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("second remove = %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".schedules-") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestLoadStrict(t *testing.T) {
	p := filepath.Join(t.TempDir(), DefaultFile)
	if err := os.WriteFile(p, []byte("schedules:\n  - name: a\n    max_usd_per_dya: 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "max_usd_per_dya") {
		t.Fatalf("Load = %v, want unknown key error", err)
	}
}

func TestFileValidateDuplicates(t *testing.T) {
	root, load := project(t, map[string]schemas.Experiment{"smoke.yaml": {Smoke: true}})
	s := Schedule{Name: "a", Experiment: "experiments/smoke.yaml", Cron: "* * * * *"}
	f := &File{Schedules: []Schedule{s, s}, Alerts: []alerts.Config{{}}}
	err := f.Validate(root, load)
	if err == nil || !strings.Contains(err.Error(), "more than once") || !strings.Contains(err.Error(), "url_env is empty") {
		t.Fatalf("Validate = %v", err)
	}
}

func TestFailDecisionsAndResolve(t *testing.T) {
	if got := (Schedule{}).FailDecisions(); len(got) != 2 {
		t.Fatalf("default fail_on = %v", got)
	}
	if got := (Schedule{FailOn: []string{"inconclusive"}}).FailDecisions(); got[0] != "inconclusive" {
		t.Fatalf("fail_on = %v", got)
	}
	if Resolve("/r", "./a/b.yaml") != filepath.Join("/r", "a", "b.yaml") || Resolve("/r", "/abs.yaml") != "/abs.yaml" {
		t.Fatal("Resolve")
	}
}
