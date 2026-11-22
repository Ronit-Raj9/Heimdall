package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
)

// A paused web schedule and a paused drift monitor are listed but never
// queued by the ticker; the running ones still are.
func TestScheduleTickerSkipsPaused(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "policies"), 0o755)
	_ = os.MkdirAll(filepath.Join(root, "canaries"), 0o755)
	web := "schedules:\n" +
		"  - {name: hourly, experiment: experiments/n.yaml, cron: \"0 * * * *\", timezone: UTC}\n" +
		"  - {name: held, experiment: experiments/n.yaml, cron: \"0 * * * *\", timezone: UTC, paused: true}\n"
	if err := os.WriteFile(handlers.WebSchedulesPath(root), []byte(web), 0o644); err != nil {
		t.Fatal(err)
	}
	def := "name: held\nagent: agents/a.yaml\nsuite: coding-core@1.0.0\ntasks: [t01]\ntrials: 1\ntargets: [{model: m}]\n" +
		"schedule: {cron: \"0 * * * *\", timezone: UTC, paused: true}\nbudget: {per_run_usd: 0, per_day_usd: 0}\nsmoke: true\n"
	if err := os.WriteFile(filepath.Join(root, "canaries", "held.yaml"), []byte(def), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := webScheduleFile(root)
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := f.Find("canary-held"); !ok || !s.Paused {
		t.Fatalf("paused canary schedule not listed as paused: %+v %v", s, ok)
	}
	fj := &fakeJobs{}
	start := time.Date(2026, 11, 21, 9, 59, 0, 0, time.UTC)
	tk := &scheduleTicker{root: root, org: "local", jobs: fj, last: start}
	if got := tk.tick(context.Background(), start.Add(90*time.Second)); len(got) != 1 || got[0] != "hourly" {
		t.Fatalf("fired %v, want only hourly", got)
	}
}
