package main

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// A seal marks its slot dirty; the debounced flush refreshes that slot, and
// once indexFullEvery has passed the flush rebuilds everything instead.
func TestSlotIndexRefresherDebouncesAndFallsBackToFull(t *testing.T) {
	oldDelay, oldFull := indexRefreshDelay, indexFullEvery
	indexRefreshDelay, indexFullEvery = 20*time.Millisecond, time.Hour
	t.Cleanup(func() { indexRefreshDelay, indexFullEvery = oldDelay, oldFull })
	ctx := context.Background()
	st, err := store.NewFSStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	m := &schemas.Manifest{ExpID: "e_ref", SuiteID: "coding_core", Hash: "sha256:m",
		Slots: []schemas.Slot{{SlotID: "s_1", PairID: "p_1", Task: "t01", Arm: "a", Condition: "clean", Trial: 1, Counted: true}}}
	if err := st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	newRun := func(id string) {
		sl := m.Slots[0]
		sl.ExpID = m.ExpID
		r := &schemas.Run{RunID: id, SlotID: "s_1", PairID: "p_1", ExpID: m.ExpID, Attempt: 1, Stage: schemas.StageCleanup, ManifestHash: m.Hash}
		if _, err := st.NewRun(ctx, &sl, r); err != nil {
			t.Fatal(err)
		}
	}
	r := refresherFor(ix, utils.NewStdLogger(os.Stderr, false))
	newRun("r_1")
	r.noteSeal("e_ref", "s_1")
	r.noteSeal("e_ref", "s_1") // a burst: one refresh
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := ix.Run("r_1"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot refresh never landed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Past indexFullEvery the flush is a full rebuild.
	indexFullEvery = 0
	newRun("r_2")
	r.noteSeal("e_ref", "")
	for {
		if _, ok := ix.Run("r_2"); ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("full rebuild never landed")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
