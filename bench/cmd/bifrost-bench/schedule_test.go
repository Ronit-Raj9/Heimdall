package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
)

// scheduleProject writes a minimal project: deploy/bench.yaml, a smoke
// experiment and a live experiment with a 5 USD spend cap. It returns the
// config path and the project root.
func scheduleProject(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"deploy/bench.yaml": `runs_root: runs
benchmarks_root: benchmarks
server:
  listen: 127.0.0.1:1
  mode: live
alerts:
  - url_env: BENCH_ALERT_URL
    format: slack
`,
		"experiments/smoke.yaml": `name: smoke
smoke: true
suite: coding-core@1.0.0
base: agents/toy.yaml
arms:
  A: {label: a}
  B: {label: b}
primary: {condition: clean, metric: verified_success}
conditions:
  - {id: clean, tasks: [t01], trials: 1}
budget: {total_usd: 0}
verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}
seed: 1
concurrency: 1
manifest_mode: agent_exposure
`,
		"experiments/live.yaml": `name: live
suite: coding-core@1.0.0
base: agents/toy.yaml
arms:
  A: {label: a}
  B: {label: b}
primary: {condition: clean, metric: verified_success}
conditions:
  - {id: clean, tasks: [t01], trials: 1}
budget: {total_usd: 5}
verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}
seed: 1
concurrency: 1
manifest_mode: agent_exposure
`,
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Join(root, "deploy", "bench.yaml"), root
}

func TestScheduleAddListRemove(t *testing.T) {
	config, root := scheduleProject(t)
	file := filepath.Join(root, schedule.DefaultFile)
	var out bytes.Buffer
	add := func(args ...string) int {
		t.Helper()
		out.Reset()
		return scheduleAdd(append([]string{"-config", config}, args...), &out)
	}
	if code := add("-name", "nightly", "-experiment", filepath.Join(root, "experiments", "live.yaml"), "-cron", "0 2 * * *", "-max-usd-per-day", "10", "-timezone", "UTC"); code != exitOK {
		t.Fatalf("add live = %d", code)
	}
	if !strings.Contains(out.String(), "schedule nightly added") {
		t.Fatalf("add output = %q", out.String())
	}
	if code := add("-name", "smoke", "-experiment", filepath.Join(root, "experiments", "smoke.yaml"), "-cron", "*/30 * * * *", "-fail-on", "regressed,inconclusive"); code != exitOK {
		t.Fatalf("add smoke without a daily cap = %d", code)
	}
	refused := []struct {
		name string
		args []string
	}{
		{"live without daily cap", []string{"-name", "nocap", "-experiment", filepath.Join(root, "experiments", "live.yaml"), "-cron", "0 2 * * *"}},
		{"bad cron", []string{"-name", "badcron", "-experiment", filepath.Join(root, "experiments", "smoke.yaml"), "-cron", "0 2 * *"}},
		{"duplicate", []string{"-name", "nightly", "-experiment", filepath.Join(root, "experiments", "smoke.yaml"), "-cron", "0 2 * * *"}},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			if code := add(tt.args...); code == exitOK {
				t.Fatalf("add %v accepted", tt.args)
			}
		})
	}
	if code := add("-name", "x"); code != exitUsage {
		t.Fatalf("add with missing flags = %d, want usage", code)
	}

	f, err := schedule.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Schedules) != 2 || f.Schedules[0].Experiment != "experiments/live.yaml" || f.Schedules[1].FailOn[1] != "inconclusive" {
		t.Fatalf("schedules file = %+v", f.Schedules)
	}

	out.Reset()
	if code := scheduleList([]string{"-config", config, "-format", "json"}, &out); code != exitOK {
		t.Fatalf("list = %d", code)
	}
	var listed struct {
		Schedules []scheduleRow `json:"schedules"`
	}
	if err := json.Unmarshal(out.Bytes(), &listed); err != nil {
		t.Fatalf("list json %q: %v", out.String(), err)
	}
	if len(listed.Schedules) != 2 || listed.Schedules[0].Last != "never" || !strings.Contains(listed.Schedules[0].Next, "T02:00:00Z") {
		t.Fatalf("listed = %+v", listed.Schedules)
	}
	out.Reset()
	if code := scheduleList([]string{"-config", config}, &out); code != exitOK || !strings.Contains(out.String(), "0.00 / 10.00") || !strings.Contains(out.String(), "none (smoke)") {
		t.Fatalf("list table = %d %q", out.Len(), out.String())
	}

	out.Reset()
	if code := scheduleRemove([]string{"nightly", "-config", config}, &out); code != exitOK {
		t.Fatalf("remove (flags after the name) = %d", code)
	}
	if code := scheduleRemove([]string{"-config", config, "nightly"}, &out); code == exitOK {
		t.Fatal("removing a missing schedule succeeded")
	}
	if f, err = schedule.Load(file); err != nil || len(f.Schedules) != 1 || f.Schedules[0].Name != "smoke" {
		t.Fatalf("after remove = %+v, %v", f, err)
	}
}

func TestScheduleConfigAlerts(t *testing.T) {
	config, _ := scheduleProject(t)
	cfg, err := lib.LoadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Alerts) != 1 || cfg.Alerts[0].URLEnv != "BENCH_ALERT_URL" || cfg.Alerts[0].Format != "slack" {
		t.Fatalf("config alerts = %+v", cfg.Alerts)
	}
}

func TestScheduleUsage(t *testing.T) {
	if code := runSchedule(nil); code != exitUsage {
		t.Fatalf("no subcommand = %d", code)
	}
	if code := runSchedule([]string{"bogus"}); code != exitUsage {
		t.Fatalf("unknown subcommand = %d", code)
	}
	if code := scheduleRemove([]string{}, &bytes.Buffer{}); code != exitUsage {
		t.Fatalf("remove without a name = %d", code)
	}
}

func TestScheduleRows(t *testing.T) {
	config, root := scheduleProject(t)
	cfg, err := lib.LoadConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	file := &schedule.File{Schedules: []schedule.Schedule{{Name: "a", Cron: "0 0 30 2 *"}, {Name: "b", Cron: "bad"}, {Name: "c", Cron: "0 * * * *", Timezone: "UTC"}}}
	rows, err := scheduleRows(cfg, file, time.Date(2026, 11, 15, 10, 7, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Next != "never" || rows[1].Next != "invalid cron" || rows[2].Next != "2026-11-15T11:00:00Z" {
		t.Fatalf("rows = %+v", rows)
	}
	if got := projectRelative(root, filepath.Join(root, "experiments", "x.yaml")); got != "experiments/x.yaml" {
		t.Fatalf("projectRelative inside = %q", got)
	}
	if got := projectRelative(root, "/elsewhere/x.yaml"); got != "/elsewhere/x.yaml" {
		t.Fatalf("projectRelative outside = %q", got)
	}
}
