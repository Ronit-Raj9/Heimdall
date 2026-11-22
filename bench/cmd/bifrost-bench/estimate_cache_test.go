package main

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestHistoryCacheHitAndInvalidate: a second forecast of the same suite
// reads the sealed history from the cache (D4.slow); a run sealed since then
// changes the store's fingerprint, so the next read loads it again and sees
// the new runs.
func TestHistoryCacheHitAndInvalidate(t *testing.T) {
	ctx := context.Background()
	st := forecastStore(t)
	a := &app{store: st, prices: testPrices(t), logger: schemas.NoopLogger{}}
	planned := &schemas.Manifest{ExpID: "e_new", SuiteID: "s", SuiteVersion: "1",
		Arms: map[string]schemas.ArmManifest{"A": {ConfigHash: "hA", Config: schemas.AgentConfig{ID: "codex", Model: "m-a"}}}}
	for _, task := range []string{"t1", "t2", "t3", "t4"} {
		planned.Slots = append(planned.Slots, schemas.Slot{Arm: "A", Task: task, Counted: true})
	}

	h, hit, err := a.cachedHistory(ctx, st, planned, false)
	if err != nil {
		t.Fatal(err)
	}
	if hit || len(h.Runs) != 4 || h.Synthetic != 4 {
		t.Fatalf("first load: hit %v runs %d synthetic %d, want a miss with 4 runs and 4 synthetic", hit, len(h.Runs), h.Synthetic)
	}
	h, hit, err = a.cachedHistory(ctx, st, planned, false)
	if err != nil || !hit || len(h.Runs) != 4 {
		t.Fatalf("second load: hit %v runs %d err %v, want a hit with 4 runs", hit, len(h.Runs), err)
	}
	// The library of the same suite comes from the same cached read.
	lib, hit, err := a.cachedHistory(ctx, st, planned, true)
	if err != nil || !hit || len(lib.Runs) != 4 {
		t.Fatalf("library: hit %v runs %d err %v", hit, len(lib.Runs), err)
	}

	addSealedExperiment(t, st, "e_more", map[string]float64{"A": 0.2}, `{"kind":"message","action":"pass","upstream":"real","status":200}`)
	h, hit, err = a.cachedHistory(ctx, st, planned, false)
	if err != nil {
		t.Fatal(err)
	}
	if hit || len(h.Runs) != 8 {
		t.Fatalf("after a new sealed experiment: hit %v runs %d, want a miss with 8 runs", hit, len(h.Runs))
	}

	// The forecast itself still reads the same history (TestForecastFromStore
	// pins its numbers); here only that it answers through the cache.
	f, err := a.forecast(ctx, planned)
	if err != nil || f.Arms[0].HistoryRuns != 8 {
		t.Fatalf("forecast %+v err %v", f, err)
	}
}
