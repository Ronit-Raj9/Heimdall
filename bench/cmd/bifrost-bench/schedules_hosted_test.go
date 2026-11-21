package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// fakeScheduleLauncher records what a hosted fire plans and starts.
type fakeScheduleLauncher struct {
	mu      sync.Mutex
	planned []string
	created []string
	started []string
}

func (f *fakeScheduleLauncher) Plan(_ context.Context, e *schemas.Experiment) (*schemas.Manifest, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planned = append(f.planned, e.Name)
	return &schemas.Manifest{ExpID: "exp-sched-1"}, nil
}
func (f *fakeScheduleLauncher) Create(_ context.Context, m *schemas.Manifest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created = append(f.created, m.ExpID)
	return nil
}
func (f *fakeScheduleLauncher) Start(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, id)
	return nil
}
func (f *fakeScheduleLauncher) Events(string) (<-chan schemas.LiveEvent, func()) {
	ch := make(chan schemas.LiveEvent)
	close(ch)
	return ch, func() {}
}

func TestHostedScheduleFirePlansThroughLauncher(t *testing.T) {
	config, root := scheduleProject(t)
	a, err := newApp(context.Background(), config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "policies"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(handlers.WebSchedulesPath(root), []byte("schedules:\n  - {name: nightly, experiment: experiments/smoke.yaml, cron: \"0 6 * * *\", timezone: UTC}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := scheduleRunnerCheck
	scheduleRunnerCheck = func(handlers.Launcher) error { return nil }
	t.Cleanup(func() { scheduleRunnerCheck = prev })
	fl := &fakeScheduleLauncher{}
	env := jobEnv{App: a, OrgID: "org1", ProjectRoot: root, Hosted: true,
		Services: handlers.Services{OrgID: "org1", Hosted: true, Launcher: fl}}
	rec, err := fireWebScheduleWith(context.Background(), env, "nightly", &bridgeTestLog{})
	if err != nil {
		t.Fatal(err)
	}
	if len(fl.planned) != 1 || fl.planned[0] != "smoke" || len(fl.started) != 1 || fl.started[0] != "exp-sched-1" {
		t.Fatalf("launcher planned %v created %v started %v", fl.planned, fl.created, fl.started)
	}
	if rec.Result == nil || rec.Result.ExpID != "exp-sched-1" {
		t.Fatalf("record %+v", rec)
	}
	last, err := schedule.LastRecords(handlers.ScheduleStateDir(root))
	if err != nil || last["nightly"].Schedule != "nightly" {
		t.Fatalf("no record in .heimdall/schedules: %v %+v", err, last)
	}
	// No launcher: refused, nothing runs in the server process.
	env.Services.Launcher = nil
	if _, err := fireWebScheduleWith(context.Background(), env, "nightly", &bridgeTestLog{}); err == nil {
		t.Fatal("hosted fire without a launcher must fail")
	}
}

func TestHostedScheduleFireRefusesShortInterval(t *testing.T) {
	config, root := scheduleProject(t)
	a, err := newApp(context.Background(), config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(root, "policies"), 0o755)
	if err := os.WriteFile(handlers.WebSchedulesPath(root), []byte("schedules:\n  - {name: often, experiment: experiments/smoke.yaml, cron: \"*/5 * * * *\", timezone: UTC}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fl := &fakeScheduleLauncher{}
	env := jobEnv{App: a, OrgID: "org1", ProjectRoot: root, Hosted: true, Services: handlers.Services{Hosted: true, Launcher: fl}}
	if _, err := fireWebScheduleWith(context.Background(), env, "often", &bridgeTestLog{}); err == nil {
		t.Fatal("a schedule under the minimum interval must be refused")
	}
	if len(fl.planned) != 0 {
		t.Fatalf("planned %v", fl.planned)
	}
	// The hosted ticker does not queue it either.
	fj := &fakeJobs{}
	start := time.Date(2026, 11, 21, 9, 59, 0, 0, time.UTC)
	tk := &scheduleTicker{root: root, org: "org1", hosted: true, jobs: fj, last: start}
	if got := tk.tick(context.Background(), start.Add(2*time.Minute)); len(got) != 0 || len(fj.created) != 0 {
		t.Fatalf("queued %v", got)
	}
}

func TestHostedTickerStopsOnEviction(t *testing.T) {
	config, _ := scheduleProject(t)
	a, err := newApp(context.Background(), config, appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := handlers.Services{OrgID: "org-evict", Hosted: true, Jobs: &fakeJobs{}}
	stop1 := startHostedScheduleTicker(a, s)
	if !hostedTickerRunning("org-evict") {
		t.Fatal("ticker not started")
	}
	// A rebuilt runtime replaces the ticker; the old runtime's Close must
	// not stop the new one.
	stop2 := startHostedScheduleTicker(a, s)
	stop1()
	if !hostedTickerRunning("org-evict") {
		t.Fatal("old runtime's close stopped the new ticker")
	}
	stop2()
	if hostedTickerRunning("org-evict") {
		t.Fatal("ticker still registered after the runtime closed")
	}
	// The local service hook never starts a hosted ticker.
	startScheduleTicker(a, &s)
	if hostedTickerRunning("org-evict") {
		t.Fatal("service hook started a hosted ticker")
	}
}

func TestOrgsToWake(t *testing.T) {
	storage := t.TempDir()
	l := tenancy.Layout{Hosted: true, StorageRoot: storage}
	p, _ := l.Paths("orgwake")
	_ = os.MkdirAll(filepath.Join(p.ProjectRoot, "policies"), 0o755)
	if err := os.WriteFile(handlers.WebSchedulesPath(p.ProjectRoot), []byte("schedules:\n  - {name: h, experiment: experiments/n.yaml, cron: \"0 * * * *\", timezone: UTC}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 21, 9, 59, 0, 0, time.UTC)
	none := func(string) bool { return false }
	if got := orgsToWake(l, now, none); len(got) != 1 || got[0] != "orgwake" {
		t.Fatalf("wake %v", got)
	}
	if got := orgsToWake(l, now.Add(-30*time.Minute), none); len(got) != 0 {
		t.Fatalf("woke early %v", got)
	}
	if got := orgsToWake(l, now, func(string) bool { return true }); len(got) != 0 {
		t.Fatalf("woke a running org %v", got)
	}
}
