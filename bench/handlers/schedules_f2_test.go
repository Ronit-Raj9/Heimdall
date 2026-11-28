package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

// PT-3: the form's default zone (the browser's old name) saves, through
// the same PUT the web sends, and is stored under its current name.
func TestSchedulesPutAcceptsOldZoneNames(t *testing.T) {
	for zone, want := range map[string]string{"Asia/Calcutta": "Asia/Kolkata", "Asia/Kolkata": "Asia/Kolkata", "UTC": "UTC"} {
		h, root := schedEnv(t)
		body := `{"file":{"schedules":[{"name":"nightly","experiment":"experiments/n.yaml","cron":"0 6 * * *","timezone":"` + zone + `","max_usd_per_day":5}]}}`
		ctx := schedCall(h.put, body, "")
		if ctx.Response.StatusCode() != 200 {
			t.Fatalf("%s: %d %s", zone, ctx.Response.StatusCode(), ctx.Response.Body())
		}
		f, err := LoadWebSchedules(root)
		if err != nil || f.Schedules[0].Timezone != want {
			t.Fatalf("%s saved as %+v %v", zone, f, err)
		}
	}
	h, _ := schedEnv(t)
	ctx := schedCall(h.put, `{"file":{"schedules":[{"name":"nightly","experiment":"experiments/n.yaml","cron":"0 6 * * *","timezone":"Mars/Olympus","max_usd_per_day":5}]}}`, "")
	if ctx.Response.StatusCode() != 422 || !strings.Contains(string(ctx.Response.Body()), "pick another zone") {
		t.Fatalf("wrong zone: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

// notReadyLauncher plans nothing: the benchmark's tasks are not admitted.
type notReadyLauncher struct{ fakeLauncher }

func (*notReadyLauncher) Plan(context.Context, *schemas.Experiment) (*schemas.Manifest, error) {
	return nil, errors.New("suite lock: task t01 is pending, not admitted; task t02 is pending, not admitted")
}

// PH5-12: a repeat of an evaluation whose firing would stop at the
// readiness gate is refused when it is saved, with the reason, through the
// PUT the Repeat runs form sends; the same evaluation saves when it plans.
func TestSchedulesPutRefusesRepeatThatCannotStart(t *testing.T) {
	oldCompat := evalCompat
	evalCompat = func(*schemas.AgentConfig, *schemas.Suite) (bool, []schemas.Capability, string) { return true, nil, "" }
	t.Cleanup(func() { evalCompat = oldCompat })
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"agents/bot.yaml": "id: bot\nadapter: remote_http\nremote:\n  url: http://127.0.0.1:1\n",
		".heimdall/evaluations/ev_20261122_203910_0a9001.json": `{"id":"ev_20261122_203910_0a9001","spec":{"name":"old","arms":[{"agent":"bot","label":"A"}],"benchmarks":["b1"],"tasks_per_benchmark":2,"trials":1,"budget_usd":0.2}}`,
	})
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	ps, err := projectfs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	suites := &fakeSuites{ids: []string{"b1"}, suites: map[string]*schemas.Suite{"b1": {ID: "b1", Version: "1.0.0", TaskIDs: []string{"t01", "t02"}}}}
	body := `{"file":{"schedules":[{"name":"rep","evaluation":"ev_20261122_203910_0a9001","cron":"8 22 * * *","timezone":"UTC","max_usd_per_day":0.4}]}}`

	h := &schedulesHandler{env: OrgEnv{Cfg: &lib.Config{Root: root}, Mode: schemas.ModeRecorded,
		Services: Services{Projects: ps, Suites: suites, Launcher: &notReadyLauncher{}}}}
	ctx := schedCall(h.put, body, "")
	if ctx.Response.StatusCode() != 422 || !strings.Contains(string(ctx.Response.Body()), "readiness check") {
		t.Fatalf("not ready: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if f, _ := LoadWebSchedules(root); len(f.Schedules) != 0 {
		t.Fatalf("a refused repeat was saved: %+v", f.Schedules)
	}
	if _, err := os.Stat(filepath.Join(root, "experiments")); err == nil {
		if ents, _ := os.ReadDir(filepath.Join(root, "experiments")); len(ents) > 0 {
			t.Fatalf("the check wrote experiment files: %v", ents)
		}
	}

	h.env.Services.Launcher = &fakeLauncher{}
	ctx = schedCall(h.put, body, "")
	if ctx.Response.StatusCode() != 200 {
		t.Fatalf("ready: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

// A schedule paused by itself after failed firings carries the notice;
// resuming it through PUT clears the notice.
func TestPauseWebScheduleAndResume(t *testing.T) {
	h, root := schedEnv(t)
	body := `{"file":{"schedules":[{"name":"nightly","experiment":"experiments/n.yaml","cron":"0 6 * * *","max_usd_per_day":5}]}}`
	if ctx := schedCall(h.put, body, ""); ctx.Response.StatusCode() != 200 {
		t.Fatalf("put: %s", ctx.Response.Body())
	}
	if err := PauseWebSchedule(root, "nightly", schedule.PauseReason(3, "not ready")); err != nil {
		t.Fatal(err)
	}
	f, _ := LoadWebSchedules(root)
	if !f.Schedules[0].Paused || !strings.Contains(f.Schedules[0].PausedReason, "3 firings") {
		t.Fatalf("paused %+v", f.Schedules[0])
	}
	if ctx := schedCall(h.put, body, ""); ctx.Response.StatusCode() != 200 {
		t.Fatalf("resume: %s", ctx.Response.Body())
	}
	if f, _ = LoadWebSchedules(root); f.Schedules[0].Paused || f.Schedules[0].PausedReason != "" {
		t.Fatalf("resumed %+v", f.Schedules[0])
	}
}
