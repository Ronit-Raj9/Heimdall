package main

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
)

type fakePlatforms struct{ l sandbox.ConnectionsList }

func (f *fakePlatforms) List(context.Context, string) (*sandbox.ConnectionsList, error) {
	l := f.l
	l.Platforms = append([]sandbox.ConnectionView(nil), f.l.Platforms...)
	for i := range l.Platforms {
		l.Platforms[i].Default = l.Platforms[i].Name == l.Default
	}
	return &l, nil
}

func (f *fakePlatforms) SetDefault(_ context.Context, _, name, _ string) (*sandbox.ConnectionsList, error) {
	f.l.Default = name
	return &f.l, nil
}

type fakePools struct{ l schemas.WorkerConnectionsList }

func (f *fakePools) List(context.Context, string) (*schemas.WorkerConnectionsList, error) {
	l := f.l
	return &l, nil
}

func (f *fakePools) SetDefaultWorker(_ context.Context, _, _, pool string) (string, error) {
	f.l.DefaultWorker = pool
	return pool, nil
}

func (f *fakePools) EvaluationPool(_ context.Context, _, worker string) (string, error) {
	for _, c := range f.l.Connections {
		if c.Pool == worker {
			return worker, nil
		}
	}
	return "", errors.New("no worker named " + worker)
}

// TestWorkerTargetsOneSetting: setting a platform clears the own worker
// default and the other way round, through the new route and both old
// aliases; evaluations are routed by the one setting.
func TestWorkerTargetsOneSetting(t *testing.T) {
	ctx := context.Background()
	pl := &fakePlatforms{l: sandbox.ConnectionsList{Platforms: []sandbox.ConnectionView{
		{Info: sandbox.Info{Name: "modal", Title: "Modal"}, Connected: true, Status: "ok"},
		{Info: sandbox.Info{Name: "e2b", Title: "E2B"}},
	}}}
	ws := &fakePools{l: schemas.WorkerConnectionsList{Connections: []schemas.WorkerConnection{{Name: "gpu", Pool: "gpu", PlatformTitle: "Fly Machines", Status: "online"}}}}
	tg := workerTargets{platforms: pl, workers: ws}
	withImagesReady(t, true) // fix wave 3: a platform needs an image registry to become where runs go

	v, err := tg.Target(ctx, "org")
	if err != nil || v.Target != "shared" || len(v.Choices) != 3 {
		t.Fatalf("start %+v %v", v, err)
	}
	if v, err = tg.SetTarget(ctx, "org", "alice", "platform:modal"); err != nil || v.Target != "platform:modal" || pl.l.Default != "modal" || ws.l.DefaultWorker != "" {
		t.Fatalf("platform %+v %v", v, err)
	}
	// While modal takes the runs, the shared worker cannot take one
	// evaluation; own workers and a platform with images can (N208).
	for _, c := range v.Choices {
		if want := c.Kind == schemas.WorkerTargetWorker || c.Target == "platform:modal"; c.PerEvaluation != want {
			t.Errorf("choice %+v: per evaluation %v, want %v", c, c.PerEvaluation, want)
		}
	}
	if pool, err := tg.EvaluationPool(ctx, "org", ""); err != nil || pool != sharedpool.PlatformPool("modal") {
		t.Fatalf("default with a platform: %q %v", pool, err)
	}
	if _, err := tg.EvaluationPool(ctx, "org", "shared"); err == nil {
		t.Fatal("shared accepted while a platform takes the shared queue")
	}
	if pool, err := tg.EvaluationPool(ctx, "org", "worker:gpu"); err != nil || pool != "gpu" {
		t.Fatalf("own worker: %q %v", pool, err)
	}
	// The old /workers/default route sets a worker and clears the platform.
	aw := aliasedWorkers{t: tg}
	if pool, err := aw.SetDefaultWorker(ctx, "org", "alice", "gpu"); err != nil || pool != "gpu" || pl.l.Default != "" || ws.l.DefaultWorker != "gpu" {
		t.Fatalf("alias worker: %q %v platform %q", pool, err, pl.l.Default)
	}
	if pool, err := tg.EvaluationPool(ctx, "org", ""); err != nil || pool != "gpu" {
		t.Fatalf("default with a worker: %q %v", pool, err)
	}
	// N208: a platform with an image registry takes one evaluation in its
	// own pool even while it is not where runs go.
	if pool, err := tg.EvaluationPool(ctx, "org", "platform:modal"); err != nil || pool != sharedpool.PlatformPool("modal") {
		t.Fatalf("platform pick while not where runs go: %q %v", pool, err)
	}
	if pool, err := tg.EvaluationPool(ctx, "org", "shared"); err != nil || pool != "" {
		t.Fatalf("shared with a worker target: %q %v", pool, err)
	}
	if v, err = tg.SetTarget(ctx, "org", "alice", "shared"); err != nil || v.Target != "shared" || ws.l.DefaultWorker != "" || pl.l.Default != "" {
		t.Fatalf("shared %+v %v", v, err)
	}
	for _, bad := range []string{"platform:e2b", "worker:nope", "vm:x"} {
		if _, err := tg.SetTarget(ctx, "org", "alice", bad); !errors.Is(err, handlers.ErrWorkerTarget) {
			t.Errorf("%s: %v", bad, err)
		}
	}
}
