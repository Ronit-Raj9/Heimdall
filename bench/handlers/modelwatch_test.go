package handlers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/valyala/fasthttp"
)

func modelWatchEnv(t *testing.T, schedules string) (*modelWatchHandler, string) {
	t.Helper()
	root := t.TempDir()
	p := WebSchedulesPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(schedules), 0o644); err != nil {
		t.Fatal(err)
	}
	return &modelWatchHandler{env: OrgEnv{Cfg: &lib.Config{Root: root}}}, root
}

const modelWatchSchedules = "schedules:\n" +
	"  - {name: watch, evaluation: ev_20261120_000000_abc, cron: \"0 6 * * *\", max_usd_per_day: 10, new_models: {providers: [openai], current: A, usd_per_model: 5}}\n" +
	"  - {name: plain, evaluation: ev_20261120_000000_abc, cron: \"0 6 * * *\", max_usd_per_day: 10}\n"

func TestModelWatch404WithoutWatch(t *testing.T) {
	h, _ := modelWatchEnv(t, modelWatchSchedules)
	for _, name := range []string{"plain", "missing"} {
		ctx := schedCall(h.get, "", name)
		if ctx.Response.StatusCode() != fasthttp.StatusNotFound {
			t.Fatalf("%s: status %d", name, ctx.Response.StatusCode())
		}
		ctx = schedCall(h.retry, `{"model":"openai/x"}`, name)
		if ctx.Response.StatusCode() != fasthttp.StatusNotFound {
			t.Fatalf("%s retry: status %d", name, ctx.Response.StatusCode())
		}
	}
	ctx := schedCall(h.get, "", "watch")
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("watch before its first check: status %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

func TestModelWatchRetryClearsFailed(t *testing.T) {
	h, root := modelWatchEnv(t, modelWatchSchedules)
	now := time.Date(2026, 11, 27, 6, 0, 0, 0, time.UTC)
	st := &schedule.WatchState{Schedule: "watch", Baseline: map[string][]string{"openai": {"gpt-a"}}, CheckedAt: now,
		Seen: map[string]schedule.WatchItem{
			"openai/gpt-b": {Provider: "openai", Model: "gpt-b", State: schedule.WatchFailed, Reason: "r", ClaimedAt: now},
			"openai/gpt-c": {Provider: "openai", Model: "gpt-c", State: schedule.WatchStarted, EvaluationID: "ev_1", ClaimedAt: now},
		}}
	if err := schedule.SaveWatchState(root, st); err != nil {
		t.Fatal(err)
	}
	ctx := schedCall(h.retry, `{"model":"openai/gpt-b"}`, "watch")
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("retry: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var got schedule.WatchState
	if err := json.Unmarshal(ctx.Response.Body(), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.Seen["openai/gpt-b"]; ok {
		t.Fatalf("failed item still seen: %+v", got.Seen)
	}
	disk, err := schedule.LoadWatchState(root, "watch")
	if err != nil || len(disk.Seen) != 1 {
		t.Fatalf("saved %+v, %v", disk, err)
	}
	ctx = schedCall(h.retry, `{"model":"openai/gpt-c"}`, "watch")
	if ctx.Response.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("retry of a started model: %d", ctx.Response.StatusCode())
	}
	ctx = schedCall(h.retry, `{"model":"openai/nope"}`, "watch")
	if ctx.Response.StatusCode() != fasthttp.StatusNotFound {
		t.Fatalf("retry of an unknown model: %d", ctx.Response.StatusCode())
	}
}
