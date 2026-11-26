package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

// watchGateway is a fake Bifrost serving /api/models/newest.
type watchGateway struct {
	mu     sync.Mutex
	models []newestModel
	fail   bool
	token  string
}

func (g *watchGateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.URL.Path != "/api/models/newest" || r.Header.Get("X-Bifrost-Setup-Token") != g.token {
		http.Error(w, "no", http.StatusForbidden)
		return
	}
	if g.fail {
		http.Error(w, "catalog down", http.StatusBadGateway)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"models": g.models})
}

func (g *watchGateway) set(m ...newestModel) {
	g.mu.Lock()
	g.models = m
	g.mu.Unlock()
}

type watchFixture struct {
	t         *testing.T
	root      string
	app       *app
	svc       *handlers.Services
	gw        *watchGateway
	s         schedule.Schedule
	launched  []schemas.EvaluationSpec
	onLaunch  func()
	launchErr error
}

func newWatchFixture(t *testing.T) *watchFixture {
	t.Helper()
	root := t.TempDir()
	evDir := filepath.Join(root, ".heimdall", "evaluations")
	if err := os.MkdirAll(evDir, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := `{"id":"ev_20261120_000000_abc","spec":{"name":"n","mode":"versions","arms":[{"label":"today","agent":"codex","version":2,"model":"gpt-a","provider":"openai"},{"label":"other","agent":"codex","version":1,"model":"gpt-a"}],"benchmarks":["hd-a"],"tasks_per_benchmark":2,"trials":1,"faults":["clean"],"budget_usd":5},"status":"done","children":[],"created_at":"2026-11-20T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(evDir, "ev_20261120_000000_abc.json"), []byte(saved), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WATCH_TEST_SETUP_TOKEN", "tok")
	gw := &watchGateway{token: "tok"}
	srv := httptest.NewServer(gw)
	t.Cleanup(srv.Close)
	f := &watchFixture{t: t, root: root, gw: gw,
		app: &app{cfg: &lib.Config{Root: root, Gateway: lib.GatewayConfig{URL: srv.URL, AdminTokenEnv: "WATCH_TEST_SETUP_TOKEN"}}},
		svc: &handlers.Services{Launcher: &fakeScheduleLauncher{}},
		s: schedule.Schedule{Name: "watch", Evaluation: "ev_20261120_000000_abc", Cron: "0 6 * * *", Timezone: "UTC", MaxUSDPerDay: 20,
			NewModels: &schedule.NewModelWatch{Providers: []string{"openai"}, Current: "today", USDPerModel: 4}}}
	prevLaunch, prevNow := launchScheduledEvaluation, watchNow
	launchScheduledEvaluation = func(_ context.Context, _ handlers.OrgEnv, spec schemas.EvaluationSpec, _, _ string) (*schemas.Evaluation, error) {
		if f.onLaunch != nil {
			f.onLaunch()
		}
		f.launched = append(f.launched, spec)
		if f.launchErr != nil {
			return nil, f.launchErr
		}
		return &schemas.Evaluation{ID: "ev_new_" + spec.Arms[1].Model}, nil
	}
	watchNow = func() time.Time { return time.Date(2026, 11, 27, 6, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { launchScheduledEvaluation, watchNow = prevLaunch, prevNow })
	return f
}

func (f *watchFixture) run() (*schedule.Result, error) {
	f.t.Helper()
	return runEvaluationSchedule(context.Background(), f.app, f.svc, f.s)
}

func (f *watchFixture) state() *schedule.WatchState {
	f.t.Helper()
	st, err := schedule.LoadWatchState(f.root, "watch")
	if err != nil || st == nil {
		f.t.Fatalf("state %+v, %v", st, err)
	}
	return st
}

func TestWatchFirstCheckRecordsBaselineOnly(t *testing.T) {
	f := newWatchFixture(t)
	f.gw.set(newestModel{Name: "gpt-a", Provider: "openai"}, newestModel{Name: "gpt-b", Provider: "openai"})
	res, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.launched) != 0 || res.CostUSD != 0 || !strings.Contains(res.Summary, "2 models known") {
		t.Fatalf("launched %d, result %+v", len(f.launched), res)
	}
	if st := f.state(); len(st.Baseline["openai"]) != 2 || len(st.Seen) != 0 {
		t.Fatalf("state %+v", st)
	}
}

func TestWatchStartsOncePerModel(t *testing.T) {
	f := newWatchFixture(t)
	f.gw.set(newestModel{Name: "gpt-a", Provider: "openai"})
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	f.gw.set(newestModel{Name: "gpt-a", Provider: "openai"}, newestModel{Name: "gpt-c", Provider: "openai"})
	res, err := f.run()
	if err != nil {
		t.Fatal(err)
	}
	if res.EvaluationID != "ev_new_gpt-c" || res.CostUSD != 4 || !strings.Contains(res.Summary, "New model openai/gpt-c found") {
		t.Fatalf("result %+v", res)
	}
	res, err = f.run()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.launched) != 1 || res.CostUSD != 0 || !strings.Contains(res.Summary, "No new model") {
		t.Fatalf("launched %d, second result %+v", len(f.launched), res)
	}
	if it := f.state().Seen["openai/gpt-c"]; it.State != schedule.WatchStarted || it.EvaluationID != "ev_new_gpt-c" {
		t.Fatalf("item %+v", it)
	}
}

func TestWatchClaimWrittenBeforeLaunch(t *testing.T) {
	f := newWatchFixture(t)
	f.gw.set()
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	f.gw.set(newestModel{Name: "gpt-c", Provider: "openai"})
	var during string
	f.onLaunch = func() { during = f.state().Seen["openai/gpt-c"].State }
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	if during != schedule.WatchClaimed {
		t.Fatalf("state during launch %q, want claimed", during)
	}
}

func TestWatchInterruptedClaimFailsNotRetried(t *testing.T) {
	f := newWatchFixture(t)
	now := watchNow()
	st := &schedule.WatchState{Schedule: "watch", Baseline: map[string][]string{"openai": {}},
		Seen: map[string]schedule.WatchItem{"openai/gpt-c": {Provider: "openai", Model: "gpt-c", State: schedule.WatchClaimed, ClaimedAt: now.Add(-time.Hour), FirstSeen: now.Add(-time.Hour)}}}
	if err := schedule.SaveWatchState(f.root, st); err != nil {
		t.Fatal(err)
	}
	f.gw.set(newestModel{Name: "gpt-c", Provider: "openai"})
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	it := f.state().Seen["openai/gpt-c"]
	if len(f.launched) != 0 || it.State != schedule.WatchFailed || !strings.Contains(it.Reason, "the start was interrupted") {
		t.Fatalf("launched %d, item %+v", len(f.launched), it)
	}
}

func TestWatchQuotaSkipsExtraModels(t *testing.T) {
	f := newWatchFixture(t)
	f.gw.set()
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	t0 := watchNow()
	f.gw.set(newestModel{Name: "gpt-old", Provider: "openai", Created: t0.Add(-2 * time.Hour).Unix()},
		newestModel{Name: "gpt-new", Provider: "openai", Created: t0.Add(-time.Hour).Unix()})
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	st := f.state()
	if len(f.launched) != 1 || f.launched[0].Arms[1].Model != "gpt-new" {
		t.Fatalf("launched %+v", f.launched)
	}
	it := st.Seen["openai/gpt-old"]
	if it.State != schedule.WatchSkipped || !strings.Contains(it.Reason, "the watch had already started 1 evaluations today") {
		t.Fatalf("extra item %+v", it)
	}
}

func TestWatchMatchFilter(t *testing.T) {
	f := newWatchFixture(t)
	f.s.NewModels.Match = "mini"
	f.gw.set()
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	f.gw.set(newestModel{Name: "gpt-big", Provider: "openai"}, newestModel{Name: "gpt-mini", Provider: "openai"})
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	if len(f.launched) != 1 || f.launched[0].Arms[1].Model != "gpt-mini" {
		t.Fatalf("launched %+v", f.launched)
	}
	if _, ok := f.state().Seen["openai/gpt-big"]; ok {
		t.Fatal("a model outside the filter was recorded")
	}
}

func TestWatchGatewayErrorRecorded(t *testing.T) {
	f := newWatchFixture(t)
	f.gw.set()
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	f.gw.fail = true
	if _, err := f.run(); err == nil {
		t.Fatal("a gateway error was not returned")
	}
	if st := f.state(); st.LastError == "" || len(st.Baseline) == 0 {
		t.Fatalf("state %+v", st)
	}
}

func TestWatchLaunchSpecHasTwoArmsAndBudget(t *testing.T) {
	f := newWatchFixture(t)
	f.gw.set()
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	f.gw.set(newestModel{Name: "gpt-c", Provider: "openai"})
	if _, err := f.run(); err != nil {
		t.Fatal(err)
	}
	if len(f.launched) != 1 {
		t.Fatalf("launched %d", len(f.launched))
	}
	sp := f.launched[0]
	want := schemas.EvaluationArm{Label: "gpt-c (new)", Agent: "codex", Version: 2, Model: "gpt-c", Provider: "openai"}
	if len(sp.Arms) != 2 || sp.Arms[0].Label != "today" || sp.Arms[1] != want || sp.Mode != schemas.EvaluationModeModels ||
		sp.BudgetUSD != 4 || sp.Name != "New model check: gpt-c" {
		t.Fatalf("spec %+v", sp)
	}
}
