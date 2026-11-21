package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestHostedScheduleFiresAnEvaluation(t *testing.T) {
	config, root := scheduleProject(t)
	a, err := newApp(context.Background(), config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	evDir := filepath.Join(root, ".heimdall", "evaluations")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := `{"id":"ev_20261120_000000_abc","spec":{"name":"n","mode":"score","arms":[{"label":"A","agent":"codex","model":"m"}],"benchmarks":["hd-a"],"tasks_per_benchmark":2,"trials":1,"faults":["clean"]},"status":"done","children":[],"created_at":"2026-11-20T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(evDir, "ev_20261120_000000_abc.json"), []byte(saved), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handlers.WebSchedulesPath(root), []byte("schedules:\n  - {name: nightly-codex, evaluation: ev_20261120_000000_abc, cron: \"0 2 * * *\", timezone: UTC, max_usd_per_day: 6}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prevCheck := scheduleRunnerCheck
	scheduleRunnerCheck = func(handlers.Launcher) error { return nil }
	var got schemas.EvaluationSpec
	var gotSchedule string
	prevLaunch := launchScheduledEvaluation
	launchScheduledEvaluation = func(_ context.Context, _ handlers.OrgEnv, spec schemas.EvaluationSpec, _, name string) (*schemas.Evaluation, error) {
		got, gotSchedule = spec, name
		return &schemas.Evaluation{ID: "ev_20261121_020000_def", Children: []schemas.EvaluationChild{{Benchmark: "hd-a", ExperimentID: "e1"}}}, nil
	}
	t.Cleanup(func() { scheduleRunnerCheck, launchScheduledEvaluation = prevCheck, prevLaunch })
	env := jobEnv{App: a, OrgID: "org1", ProjectRoot: root, Hosted: true,
		Services: handlers.Services{OrgID: "org1", Hosted: true, Launcher: &fakeScheduleLauncher{}}}
	rec, err := fireWebScheduleWith(context.Background(), env, "nightly-codex", &bridgeTestLog{})
	if err != nil {
		t.Fatal(err)
	}
	if rec.Status != schedule.StatusOK || rec.Result == nil || rec.Result.EvaluationID != "ev_20261121_020000_def" {
		t.Fatalf("record %+v", rec)
	}
	if got.BudgetUSD != 6 || gotSchedule != "nightly-codex" || got.Arms[0].Agent != "codex" {
		t.Fatalf("launched spec %+v schedule %q; want the daily cap as budget", got, gotSchedule)
	}
	if rec.SpentTodayUSD != 6 || !strings.Contains(rec.Result.Summary, "started on 1 of 1") {
		t.Fatalf("spent %v summary %q", rec.SpentTodayUSD, rec.Result.Summary)
	}
}
