package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// jobsProject is a project folder with one suite folder, one agent config,
// a frozen experiment, and a symlink that leaves the project.
func jobsProject(t *testing.T) (*app, string) {
	t.Helper()
	root := t.TempDir()
	outside := t.TempDir()
	for _, d := range []string{"benchmarks/demo", "agents", "runs"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for p, body := range map[string]string{"agents/mine.yaml": "id: mine\n", "frozen.lock": "{}\n"} {
		if err := os.WriteFile(filepath.Join(root, p), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "evil.yaml"), []byte("id: evil\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "evil.yaml"), filepath.Join(root, "agents", "link.yaml")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "benchmarks", "escape")); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFSStore(filepath.Join(root, "runs"), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := &schemas.Manifest{ExpID: "e_demo", Slots: []schemas.Slot{{SlotID: "s_1", ExpID: "e_demo"}}}
	if err := st.CreateExperiment(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	cfg := &lib.Config{Root: root, RunsRoot: "runs", BenchmarksRoot: "benchmarks"}
	a := &app{cfg: cfg, logger: utils.NewStdLogger(os.Stderr, false), reg: &schemas.Registry{Store: st}, store: st}
	return a, root
}

func TestJobKindsValidateParamsAndConfinePaths(t *testing.T) {
	a, _ := jobsProject(t)
	hub := newJobsHub(dbtest.Open(t), a.logger, false, true, "")
	l := newLauncher(a, nil)
	hub.add(l)
	ctx := context.Background()
	create := func(kind, params string) (*jobs.Job, error) {
		return hub.runner.Create(ctx, jobs.NewJob{OrgID: "local", Kind: kind, Params: json.RawMessage(params)})
	}
	for _, tc := range []struct {
		kind, params string
		want         error // nil: accepted
	}{
		{kindSuiteLint, `{"suite":"demo"}`, nil},
		{kindSuiteLint, `{"suite":"../demo"}`, jobs.ErrInvalidParams},
		{kindSuiteLint, `{"suite":"nope"}`, jobs.ErrInvalidParams},
		{kindSuiteLint, `{"suite":"escape"}`, jobs.ErrInvalidParams}, // a symlink out of the project
		{kindSuiteLint, `{"suite":"demo","task":"t01"}`, jobs.ErrInvalidParams},
		{kindSuiteLint, `{"suite":"demo","bogus":1}`, jobs.ErrInvalidParams},
		{kindSuiteLock, `{"suite":"demo","pin_local_images":true}`, nil},
		{kindSuiteAdmit, `{"suite":"demo","task":"t/01"}`, jobs.ErrInvalidParams},
		{kindAgentCheck, `{"agent":"agents/mine.yaml","suite":"demo"}`, nil},
		{kindAgentCheck, `{"agent":"../../etc/passwd.yaml","suite":"demo"}`, jobs.ErrInvalidParams},
		{kindAgentCheck, `{"agent":"/etc/hosts.yaml","suite":"demo"}`, jobs.ErrInvalidParams},
		{kindAgentCheck, `{"agent":"agents/link.yaml","suite":"demo"}`, jobs.ErrInvalidParams},
		{kindAgentCheck, `{"agent":"agents/mine.txt","suite":"demo"}`, jobs.ErrInvalidParams},
		{kindIndexRebuild, `{}`, nil},
		{kindIndexRebuild, `{"x":1}`, jobs.ErrInvalidParams},
		{kindExperimentRegate, `{"exp_id":"e_nope"}`, jobs.ErrNotFound},
		{kindExperimentRegate, `{"exp_id":"e_demo","suite_lock":"../frozen.lock"}`, jobs.ErrInvalidParams},
		{kindExperimentRegate, `{"exp_id":"e_demo","id":"original"}`, jobs.ErrInvalidParams},
		{kindExperimentRun, `{"exp_id":"e_demo"}`, jobs.ErrNotCreatable},
	} {
		j, err := create(tc.kind, tc.params)
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Fatalf("%s %s: %v, want %v", tc.kind, tc.params, err, tc.want)
		}
		if j != nil && j.Target != "" {
			if _, err := hub.runner.Cancel(ctx, "local", j.ID); err != nil { // free the target for the next case
				t.Fatal(err)
			}
		}
	}
	j, err := create(kindExperimentRegate, `{"exp_id":"e_demo","suite_lock":"frozen.lock","note":"why"}`)
	if err != nil {
		t.Fatal(err)
	}
	var p regateParams
	if err := json.Unmarshal(j.Params, &p); err != nil || p.ID == "" || p.SuiteLock != "frozen.lock" || j.Target != "exp:e_demo" {
		t.Fatalf("regate job %+v params %s", j, j.Params)
	}
	// One job per target: the experiment's run is refused while the regrade waits.
	if err := l.Start(ctx, "e_demo"); !errors.Is(err, schemas.ErrExists) {
		t.Fatalf("start during a regrade: %v", err)
	}
	if !slices.Contains(hub.runner.Kinds(), kindAgentCheck) || slices.Contains(hub.runner.Kinds(), kindExperimentRun) {
		t.Fatalf("local kinds %v", hub.runner.Kinds())
	}
	// Hosted: no kind that starts containers from the project (rule 4).
	hosted := newJobsHub(dbtest.Open(t), a.logger, true, true, "")
	if k := hosted.runner.Kinds(); slices.Contains(k, kindAgentCheck) || slices.Contains(k, kindSuiteAdmit) {
		t.Fatalf("hosted kinds %v", k)
	}
	// Offline: no experiment.run claims.
	offline := newJobsHub(dbtest.Open(t), a.logger, false, false, "")
	if _, err := offline.runner.Enqueue(ctx, jobs.NewJob{OrgID: "local", Kind: kindExperimentRun}); !errors.Is(err, jobs.ErrUnknownKind) {
		t.Fatalf("offline experiment.run: %v", err)
	}
	// 8-C-02: offline launches nothing, so no kind that starts containers or
	// reaches the gateway; the read-only ops kinds stay.
	if k := offline.runner.Kinds(); slices.Contains(k, kindAgentCheck) || slices.Contains(k, kindSuiteAdmit) || !slices.Contains(k, kindSuiteLint) {
		t.Fatalf("offline kinds %v", k)
	}
	for _, kind := range []string{kindAgentCheck, kindSuiteAdmit} {
		if _, err := offline.runner.Enqueue(ctx, jobs.NewJob{OrgID: "local", Kind: kind}); !errors.Is(err, jobs.ErrUnknownKind) {
			t.Fatalf("offline %s: %v", kind, err)
		}
	}
}

func TestLauncherStartCancelResumeAsJobs(t *testing.T) {
	a, _ := jobsProject(t)
	hub := newJobsHub(dbtest.Open(t), a.logger, false, true, "")
	l := newLauncher(a, nil)
	hub.add(l) // the runner is not started: the job stays queued
	ctx := context.Background()
	var rc handlers.RunController = l
	if err := l.Start(ctx, "e_missing"); err == nil {
		t.Fatal("started an unknown experiment")
	}
	if err := rc.Cancel(ctx, "e_demo"); !errors.Is(err, handlers.ErrNotRunning) {
		t.Fatalf("cancel before start: %v", err)
	}
	if err := l.Start(ctx, "e_demo"); err != nil {
		t.Fatal(err)
	}
	if !l.Active("e_demo") || rc.Cancelled("e_demo") {
		t.Fatal("queued experiment is not active")
	}
	if err := l.Start(ctx, "e_demo"); !errors.Is(err, schemas.ErrExists) {
		t.Fatalf("second start: %v", err)
	}
	j, err := hub.runner.Latest(ctx, "local", kindExperimentRun, expTarget("e_demo"))
	if err != nil || j.Status != jobs.StatusQueued || j.CreatedBy != "system" {
		t.Fatalf("run job %+v %v", j, err)
	}
	if err := rc.Cancel(ctx, "e_demo"); err != nil {
		t.Fatal(err)
	}
	if l.Active("e_demo") || !rc.Cancelled("e_demo") {
		t.Fatal("cancelled experiment still active")
	}
	if err := rc.Cancel(ctx, "e_demo"); !errors.Is(err, handlers.ErrNotRunning) {
		t.Fatalf("cancel twice: %v", err)
	}
	if err := rc.Resume(ctx, "e_demo"); err != nil {
		t.Fatal(err)
	}
	if !l.Active("e_demo") || rc.Cancelled("e_demo") {
		t.Fatal("resumed experiment is not active")
	}
	// Events opens the fan-out of a queued job, so a client can follow it.
	ch, stop := l.Events("e_demo")
	defer stop()
	select {
	case _, ok := <-ch:
		if !ok {
			t.Fatal("events of a queued experiment closed at once")
		}
	default:
	}
}

// Cancelling a queued experiment run through the generic jobs API closes
// its live fan-out and leaves the runtime evictable (audit 4-C-09).
func TestGenericCancelClosesQueuedExperimentStream(t *testing.T) {
	a, _ := jobsProject(t)
	hub := newJobsHub(dbtest.Open(t), a.logger, false, true, "")
	l := newLauncher(a, nil)
	hub.add(l) // not started: the job stays queued
	ctx := context.Background()
	if err := l.Start(ctx, "e_demo"); err != nil {
		t.Fatal(err)
	}
	ch, stop := l.Events("e_demo")
	defer stop()
	if !l.Busy() {
		t.Fatal("queued experiment not busy")
	}
	j, err := hub.runner.Latest(ctx, "local", kindExperimentRun, expTarget("e_demo"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hub.runner.Cancel(ctx, "local", j.ID); err != nil { // what POST /jobs/{id}/cancel calls
		t.Fatal(err)
	}
	if _, ok := <-ch; ok {
		t.Fatal("live stream still open after the generic cancel")
	}
	if l.Busy() {
		t.Fatal("cancelled experiment keeps the runtime busy")
	}
}

// Busy closes a fan-out left open for a job that ended without a handler.
func TestBusyIgnoresStaleFanOut(t *testing.T) {
	a, _ := jobsProject(t)
	hub := newJobsHub(dbtest.Open(t), a.logger, false, true, "")
	l := newLauncher(a, nil)
	hub.add(l)
	r := l.liveRunFor("e_demo") // a fan-out with no job behind it
	if l.Busy() {
		t.Fatal("fan-out without an active job counted as busy")
	}
	if !r.isDone() {
		t.Fatal("stale fan-out left open")
	}
}

// Experiment runs on the hosted runner are capped so waiting experiments
// leave workers to the other kinds (audit 4-C-08).
func TestHostedHubLimitsExperimentWorkers(t *testing.T) {
	if hostedExpLimit >= hostedWorkers || hostedExpPerOrg > hostedExpLimit || hostedExpPerOrg < 1 {
		t.Fatalf("hosted sizing %d workers, %d experiments, %d per org", hostedWorkers, hostedExpLimit, hostedExpPerOrg)
	}
}
