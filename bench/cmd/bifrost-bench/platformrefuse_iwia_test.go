package main

import (
	"context"
	"testing"
)

// TestPlatformRefusalExplicitPlatformPick (N208 with N216): an explicit
// platform pick resolves to the platform's own pool, and the refusal before
// launch must recognise that pool as the platform, so a benchmark the
// platform cannot run is refused (handlers.refuseOnPlatform refuses per
// benchmark once the resolver names a platform, see
// handlers/platformrefuse_test.go TestPlatformRefusalRefusesBenchmark).
// It holds while the shared worker is where runs go.
func TestPlatformRefusalExplicitPlatformPick(t *testing.T) {
	ctx := context.Background()
	tg, _ := hw05Targets()

	withImagesReady(t, true)
	pool, err := tg.EvaluationPool(ctx, "org", "platform:modal")
	if err != nil || pool == "" {
		t.Fatalf("explicit pick pool %q %v", pool, err)
	}
	name, ready, err := tg.evaluationPlatform(ctx, nil, "org", pool)
	if err != nil || name != "modal" || !ready {
		t.Fatalf("resolver for pool %q: %q ready %v err %v, want modal ready", pool, name, ready, err)
	}

	// The images check of the resolver follows the platform too.
	withImagesReady(t, false)
	if name, ready, _ := tg.evaluationPlatform(ctx, nil, "org", pool); name != "modal" || ready {
		t.Fatalf("without images: %q ready %v", name, ready)
	}
}
