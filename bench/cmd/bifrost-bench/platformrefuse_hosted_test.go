package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/sandbox"
	"github.com/maximhq/bifrost/bench/schemas"
)

// withImagesReady sets platformImagesReady for one test.
func withImagesReady(t *testing.T, ready bool) {
	t.Helper()
	old := platformImagesReady
	platformImagesReady = func(context.Context, *hostedEnv, string, string) bool { return ready }
	t.Cleanup(func() { platformImagesReady = old })
}

func hw05Targets() (workerTargets, *fakePlatforms) {
	pl := &fakePlatforms{l: sandbox.ConnectionsList{Platforms: []sandbox.ConnectionView{
		{Info: sandbox.Info{Name: "modal", Title: "Modal"}, Connected: true, Status: "ok"},
	}}}
	ws := &fakePools{l: schemas.WorkerConnectionsList{Connections: []schemas.WorkerConnection{{Name: "gpu", Pool: "gpu", PlatformTitle: "Fly Machines"}}}}
	return workerTargets{platforms: pl, workers: ws}, pl
}

// TestSetTargetRefusesPlatformWithoutImages covers N216: while a platform
// cannot pull Heimdall's images (no image registry on its card), it cannot
// become where runs go: 409 (schemas.ErrExists) with the registry sentence,
// and the stored target does not change. Other targets still switch.
func TestSetTargetRefusesPlatformWithoutImages(t *testing.T) {
	withImagesReady(t, false)
	ctx := context.Background()
	tg, pl := hw05Targets()
	_, err := tg.SetTarget(ctx, "org", "alice", "platform:modal")
	if !errors.Is(err, schemas.ErrExists) || err.Error() != "Modal needs an image registry it can pull from. Add one on the Modal card." {
		t.Fatalf("err = %v", err)
	}
	if pl.l.Default != "" {
		t.Fatalf("target stored: %q", pl.l.Default)
	}
	if v, err := tg.SetTarget(ctx, "org", "alice", "worker:gpu"); err != nil || v.Target != "worker:gpu" {
		t.Fatalf("own worker %+v %v", v, err)
	}
	withImagesReady(t, true)
	if v, err := tg.SetTarget(ctx, "org", "alice", "platform:modal"); err != nil || v.Target != "platform:modal" {
		t.Fatalf("with a registry %+v %v", v, err)
	}
}

// TestEvaluationPlatformResolver: the shared queue goes to the platform
// that is where runs go, a per-evaluation platform pool names its platform,
// an own worker is no platform.
func TestEvaluationPlatformResolver(t *testing.T) {
	withImagesReady(t, false)
	ctx := context.Background()
	tg, pl := hw05Targets()
	if name, _, err := tg.evaluationPlatform(ctx, nil, "org", ""); err != nil || name != "" {
		t.Fatalf("shared target: %q %v", name, err)
	}
	pl.l.Default = "modal"
	if name, ready, err := tg.evaluationPlatform(ctx, nil, "org", ""); err != nil || name != "modal" || ready {
		t.Fatalf("platform target: %q %v %v", name, ready, err)
	}
	if name, _, _ := tg.evaluationPlatform(ctx, nil, "org", "gpu"); name != "" {
		t.Fatalf("own worker: %q", name)
	}
	if name, _, _ := tg.evaluationPlatform(ctx, nil, "org", "shared/platform/modal"); !strings.EqualFold(name, "modal") {
		t.Fatalf("platform pool: %q", name)
	}
}
