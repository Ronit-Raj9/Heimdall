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

// PH5-12, PH5-13: a repeat whose evaluation starts no benchmark is an
// error that spent nothing, and after three such firings in a row the
// schedule pauses itself with the reason.
func TestScheduledRepeatThatCannotStartPausesItself(t *testing.T) {
	config, root := scheduleProject(t)
	a, err := newApp(context.Background(), config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	evDir := filepath.Join(root, ".heimdall", "evaluations")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := `{"id":"ev_20261120_000000_abc","spec":{"name":"n","mode":"score","arms":[{"label":"A","agent":"codex","model":"m"}],"benchmarks":["hd-a"],"tasks_per_benchmark":2,"trials":1,"faults":["clean"],"budget_usd":0.4},"status":"done","children":[],"created_at":"2026-11-20T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(evDir, "ev_20261120_000000_abc.json"), []byte(saved), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handlers.WebSchedulesPath(root), []byte("schedules:\n  - {name: rep, evaluation: ev_20261120_000000_abc, cron: \"8 22 * * *\", timezone: UTC, max_usd_per_day: 0.4}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prevCheck, prevLaunch := scheduleRunnerCheck, launchScheduledEvaluation
	scheduleRunnerCheck = func(handlers.Launcher) error { return nil }
	launchScheduledEvaluation = func(context.Context, handlers.OrgEnv, schemas.EvaluationSpec, string, string) (*schemas.Evaluation, error) {
		return &schemas.Evaluation{ID: "ev_20261121_020000_def", Children: []schemas.EvaluationChild{{Benchmark: "hd-a", Status: schemas.EvaluationChildSkipped, Skipped: "not ready"}}}, nil
	}
	t.Cleanup(func() { scheduleRunnerCheck, launchScheduledEvaluation = prevCheck, prevLaunch })
	env := jobEnv{App: a, OrgID: "org1", ProjectRoot: root, Hosted: true,
		Services: handlers.Services{OrgID: "org1", Hosted: true, Launcher: &fakeScheduleLauncher{}}}
	for i := 1; i <= schedule.PauseAfterFailures; i++ {
		rec, err := fireWebScheduleWith(context.Background(), env, "rep", &bridgeTestLog{})
		if err != nil {
			t.Fatal(err)
		}
		if rec.Status != schedule.StatusError || rec.SpentTodayUSD != 0 {
			t.Fatalf("firing %d: status %s spent %v; want error and nothing spent", i, rec.Status, rec.SpentTodayUSD)
		}
		f, _ := handlers.LoadWebSchedules(root)
		if paused := f.Schedules[0].Paused; paused != (i == schedule.PauseAfterFailures) {
			t.Fatalf("after firing %d paused=%v", i, paused)
		}
	}
	f, _ := handlers.LoadWebSchedules(root)
	if !strings.Contains(f.Schedules[0].PausedReason, "started no benchmark") {
		t.Fatalf("pause notice %q", f.Schedules[0].PausedReason)
	}
}
