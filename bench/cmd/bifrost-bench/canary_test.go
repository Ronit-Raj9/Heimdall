package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

// TestCanaryInitAndStatus writes a definition with `canary init` and reads
// it back with `canary status`, with no gateway.
func TestCanaryInitAndStatus(t *testing.T) {
	cfgPath, root := scheduleProject(t)
	var out bytes.Buffer
	args := []string{"nightly", "-config", cfgPath, "-agent", filepath.Join(root, "agents", "toy.yaml"), "-suite", "coding-core@1.0.0",
		"-tasks", "t01,t02", "-trials", "3", "-target", "anthropic/claude-sonnet-4-5", "-target", "bedrock/claude-sonnet-4-5",
		"-cron", "0 6 * * *", "-timezone", "UTC", "-per-run-usd", "1", "-per-day-usd", "3"}
	if code := canaryInit(args, &out); code != exitOK {
		t.Fatalf("init exit %d: %s", code, out.String())
	}
	d, err := canary.Load(canary.Path(root, "nightly"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Agent != "agents/toy.yaml" || len(d.Targets) != 2 || d.Targets[1].Provider != "bedrock" || d.Trials != 3 || d.Schedule.Cron != "0 6 * * *" {
		t.Errorf("definition = %+v", d)
	}
	if code := canaryInit(args, &out); code == exitOK {
		t.Error("init overwrote an existing definition without -force")
	}
	// A canary that can spend needs both caps.
	noCaps := []string{"nocap", "-config", cfgPath, "-agent", "agents/toy.yaml", "-suite", "coding-core@1.0.0", "-tasks", "t01", "-target", "anthropic/m"}
	if code := canaryInit(noCaps, &out); code == exitOK {
		t.Error("init accepted a live canary without caps")
	}
	out.Reset()
	if code := canaryStatus([]string{"-config", cfgPath}, &out, true); code != exitOK {
		t.Fatalf("list exit %d", code)
	}
	if !strings.Contains(out.String(), "nightly") || !strings.Contains(out.String(), "no_baseline") || !strings.Contains(out.String(), "0.00 / 3.00") {
		t.Errorf("list = %s", out.String())
	}
	out.Reset()
	if code := canaryStatus([]string{"nightly", "-config", cfgPath}, &out, false); code != exitOK || !strings.Contains(out.String(), "canary nightly: no_baseline") {
		t.Errorf("status exit %d: %s", code, out.String())
	}
}

// TestCanarySchedulesJoinTheDaemon checks the daemon hook: a scheduled
// canary becomes canary-<name> with its daily cap, its file loads as an
// experiment with the per-run cap, and the schedule file validates.
func TestCanarySchedulesJoinTheDaemon(t *testing.T) {
	cfgPath, root := scheduleProject(t)
	cfg, err := lib.LoadConfig(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	d := &canary.Definition{Name: "nightly", Agent: "agents/toy.yaml", Suite: "coding-core@1.0.0", Tasks: schemas.TaskSelector{IDs: []string{"t01"}},
		Trials: 1, Targets: []canary.Target{{Model: "m"}}, Budget: canary.Budget{PerRunUSD: 1, PerDayUSD: 3},
		Schedule: &canary.ScheduleSpec{Cron: "0 6 * * *", Timezone: "UTC"}}
	if err := canary.Save(canary.Path(root, "nightly"), d); err != nil {
		t.Fatal(err)
	}
	unscheduled := *d
	unscheduled.Name, unscheduled.Schedule = "adhoc", nil
	if err := canary.Save(canary.Path(root, "adhoc"), &unscheduled); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(canary.Path(root, "broken"), []byte("name: broken\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &app{cfg: cfg, logger: schemas.NoopLogger{}}
	file := &schedule.File{}
	addCanarySchedules(a, file)
	if len(file.Schedules) != 1 {
		t.Fatalf("schedules = %+v", file.Schedules)
	}
	s := file.Schedules[0]
	if s.Name != "canary-nightly" || s.Experiment != "canaries/nightly.yaml" || s.MaxUSDPerDay != 3 || !isCanarySchedule(a, s) {
		t.Errorf("schedule = %+v", s)
	}
	exp, err := loadScheduled(schedule.Resolve(root, s.Experiment))
	if err != nil || exp.Budget.TotalUSD != 1 || exp.Smoke {
		t.Errorf("loaded = %+v, %v", exp, err)
	}
	if err := file.Validate(root, loadScheduled); err != nil {
		t.Errorf("validate: %v", err)
	}
	if isCanarySchedule(a, schedule.Schedule{Experiment: "experiments/smoke.yaml"}) {
		t.Error("an experiment file read as a canary")
	}
	// An experiment path still loads as an experiment.
	if e, err := loadScheduled(filepath.Join(root, "experiments", "smoke.yaml")); err != nil || !e.Smoke {
		t.Errorf("experiment = %+v, %v", e, err)
	}
}

// TestCanarySourceReadsStateFiles serves list and detail from the state
// folder, and refuses a run on an offline server.
func TestCanarySourceReadsStateFiles(t *testing.T) {
	_, root := scheduleProject(t)
	d := &canary.Definition{Name: "nightly", Agent: "agents/toy.yaml", Suite: "coding-core@1.0.0", Tasks: schemas.TaskSelector{IDs: []string{"t01"}},
		Trials: 1, Targets: []canary.Target{{Model: "m"}}, Smoke: true}
	if err := canary.Save(canary.Path(root, "nightly"), d); err != nil {
		t.Fatal(err)
	}
	src := canarySource{root: root, state: filepath.Join(root, "runs", canaryStateDir)}
	ctx := context.Background()
	l, err := src.List(ctx)
	if err != nil || len(l.Canaries) != 1 || l.Canaries[0].State != canary.StateNoBaseline {
		t.Fatalf("list = %+v, %v", l, err)
	}
	if _, err := src.Get(ctx, "missing"); err == nil {
		t.Error("a missing canary was found")
	}
	if _, err := src.Get(ctx, "../etc"); err == nil {
		t.Error("a path name was accepted")
	}
	if _, err := src.Start(ctx, "nightly"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("offline start = %v", err)
	}
}
