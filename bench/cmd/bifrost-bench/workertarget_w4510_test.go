package main

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
)

// TestEvaluationPoolExplicitPlatform (N208): an explicit platform pick
// with images ready goes to the platform's own pool even while the shared
// worker is where runs go; without images it is refused and its choice
// says why; with a platform as where runs go, the default goes there too.
func TestEvaluationPoolExplicitPlatform(t *testing.T) {
	ctx := context.Background()
	tg, pl := hw05Targets()
	pool := sharedpool.PlatformPool("modal")

	withImagesReady(t, true)
	if got, err := tg.EvaluationPool(ctx, "org", "platform:modal"); err != nil || got != pool {
		t.Fatalf("explicit pick: %q %v", got, err)
	}
	if got, err := tg.EvaluationPool(ctx, "org", ""); err != nil || got != "" {
		t.Fatalf("default on the shared worker: %q %v", got, err)
	}
	v, err := tg.Target(ctx, "org")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range v.Choices {
		if c.Kind == schemas.WorkerTargetPlatform && (!c.PerEvaluation || c.Note != "") {
			t.Fatalf("ready platform choice %+v", c)
		}
	}
	pl.l.Default = "modal"
	if got, err := tg.EvaluationPool(ctx, "org", ""); err != nil || got != pool {
		t.Fatalf("default with a platform target: %q %v", got, err)
	}

	pl.l.Default = ""
	withImagesReady(t, false)
	if _, err := tg.EvaluationPool(ctx, "org", "platform:modal"); err == nil {
		t.Fatal("platform without images accepted")
	}
	v, _ = tg.Target(ctx, "org")
	for _, c := range v.Choices {
		if c.Kind == schemas.WorkerTargetPlatform && (c.PerEvaluation || c.Note != "Add an image registry on the Modal card first.") {
			t.Fatalf("platform choice without images %+v", c)
		}
	}
}
