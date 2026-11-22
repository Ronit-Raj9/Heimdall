package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// sameIndex fails when two indexes differ in anything a reader sees.
func sameIndex(t *testing.T, got, want *Index) {
	t.Helper()
	g, w := got.snap.Load(), want.snap.Load()
	if !reflect.DeepEqual(g.experiments, w.experiments) {
		t.Fatalf("experiments %v, want %v", g.experiments, w.experiments)
	}
	for id := range w.manifests {
		if g.manifests[id] == nil || g.manifests[id].Hash != w.manifests[id].Hash {
			t.Fatalf("manifest %s differs", id)
		}
	}
	if !reflect.DeepEqual(g.runsByExp, w.runsByExp) {
		t.Fatalf("runs by experiment differ:\n got %+v\nwant %+v", g.runsByExp, w.runsByExp)
	}
	if !reflect.DeepEqual(g.byRun, w.byRun) {
		t.Fatalf("runs by id differ")
	}
	if !reflect.DeepEqual(g.byPair, w.byPair) {
		t.Fatalf("pairs differ:\n got %+v\nwant %+v", g.byPair, w.byPair)
	}
}

func TestRefreshSlotsMatchesRebuild(t *testing.T) {
	ctx := context.Background()
	s, _ := indexFixture(t)
	ix := NewIndex(s)
	if err := ix.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	m := testManifest("e_test")
	// A second attempt seals in slot b1, and a run starts in e_open.
	r := mustNewRun(t, s, m, "s_b1", "r_b1_2", 2, schemas.OutcomePass)
	mustSeal(t, s, r)
	m2 := testManifest("e_open")
	mustNewRun(t, s, m2, "s_b1", "r_open_b", 1, schemas.OutcomeUnknown)
	if err := ix.RefreshSlots(ctx, "e_test", []string{"s_b1"}); err != nil {
		t.Fatal(err)
	}
	if err := ix.RefreshSlots(ctx, "e_open", []string{"s_b1"}); err != nil {
		t.Fatal(err)
	}
	full := NewIndex(s)
	if err := full.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	sameIndex(t, ix, full)

	// An experiment the index has never seen is indexed whole.
	m3 := mustCreateExp(t, s, "e_new")
	mustSeal(t, s, mustNewRun(t, s, m3, "s_a1", "r_new", 1, schemas.OutcomeFail))
	if err := ix.RefreshSlots(ctx, "e_new", []string{"s_a1"}); err != nil {
		t.Fatal(err)
	}
	if err := full.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	sameIndex(t, ix, full)
	if err := ix.RefreshExperiment(ctx, "e_test"); err != nil {
		t.Fatal(err)
	}
	sameIndex(t, ix, full)
	if err := ix.RefreshSlots(ctx, "../x", nil); err == nil {
		t.Fatal("bad experiment id: want error")
	}
}

func TestSealTailAndVerifySealed(t *testing.T) {
	old := sealsTailChunk
	sealsTailChunk = 150 // about one line: every path crosses chunk borders
	t.Cleanup(func() { sealsTailChunk = old })
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := manyManifest("e_tail", 30)
	if err := s.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	var runs []*schemas.Run
	for i := range m.Slots {
		r := runIn(m, i)
		if _, err := s.NewRun(ctx, slotOf(m, r.SlotID), r); err != nil {
			t.Fatal(err)
		}
		mustSeal(t, s, r)
		if err := s.VerifySealed(ctx, r); err != nil {
			t.Fatalf("VerifySealed %s: %v", r.RunID, err)
		}
		runs = append(runs, r)
	}
	if err := s.VerifyChain("e_tail"); err != nil {
		t.Fatalf("chain built from tail reads: %v", err)
	}
	// Every run, old or new, is found: the backward read reaches the start.
	for _, r := range runs {
		if err := s.VerifySealed(ctx, r); err != nil {
			t.Fatalf("VerifySealed %s: %v", r.RunID, err)
		}
	}
	// A seal whose line is gone from seals.log fails.
	logPath := filepath.Join(s.Root(), "e_tail", sealsLogFile)
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if err := os.WriteFile(logPath, []byte(strings.Join(lines[:len(lines)-1], "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifySealed(ctx, runs[len(runs)-1]); !errors.Is(err, schemas.ErrSealMismatch) {
		t.Fatalf("missing line: got %v, want ErrSealMismatch", err)
	}
	if err := s.VerifySealed(ctx, runs[0]); err != nil {
		t.Fatalf("first run: %v", err)
	}
	// A run folder that is not there is not found.
	ghost := *runs[0]
	ghost.RunID = "r_ghost"
	if err := s.VerifySealed(ctx, &ghost); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("ghost: got %v", err)
	}
}

func TestSealsLogLastHashSkipsMalformedTail(t *testing.T) {
	old := sealsTailChunk
	sealsTailChunk = 64
	t.Cleanup(func() { sealsTailChunk = old })
	s := newTestStore(t, nil)
	p := filepath.Join(t.TempDir(), "seals.log")
	good := "r_1 " + strings.Repeat("a", 64) + "\n"
	junk := strings.Repeat("not a seal line\n", 20)
	if err := os.WriteFile(p, []byte(good+junk), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h, err := s.sealsLogLastHash(f, "e")
	if err != nil || h != strings.Repeat("a", 64) {
		t.Fatalf("last hash %q, %v", h, err)
	}
}

func manyManifest(expID string, n int) *schemas.Manifest {
	m := &schemas.Manifest{ExpID: expID, SuiteID: "coding_core", Hash: "sha256:manifest"}
	for i := range n {
		m.Slots = append(m.Slots, schemas.Slot{SlotID: fmt.Sprintf("s_%05d", i), PairID: fmt.Sprintf("p_%05d", i/2),
			Task: fmt.Sprintf("t%05d", i/2), Arm: []string{"a", "b"}[i%2], Condition: "clean", Trial: 1, OrderIndex: i, Counted: true})
	}
	return m
}

func runIn(m *schemas.Manifest, i int) *schemas.Run {
	sl := m.Slots[i]
	return &schemas.Run{RunID: fmt.Sprintf("r_%05d", i), SlotID: sl.SlotID, PairID: sl.PairID, ExpID: m.ExpID, Attempt: 1,
		Stage: schemas.StageCleanup, ManifestHash: m.Hash, Outcome: schemas.OutcomePass}
}

// TestHotPathCostPerSealIsFlat is the before/after benchmark of INFRA_I4
// item 1. It grows one experiment and, at each size, times what the server
// does for one more sealed run: before (Verify, which lists every slot and
// reads all of seals.log, plus a full index Rebuild) and after (VerifySealed
// plus RefreshSlots of the one slot). The old cost grows with the runs
// already sealed; the new one must not.
func TestHotPathCostPerSealIsFlat(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a store of 800 sealed runs")
	}
	ctx := context.Background()
	s := newTestStore(t, nil)
	const total, probe = 800, 30
	m := manyManifest("e_big", total)
	if err := s.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(s)
	if err := ix.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	type cost struct{ before, after time.Duration }
	measure := func(from int) cost {
		var c cost
		for i := from; i < from+probe; i++ {
			r := runIn(m, i)
			if _, err := s.NewRun(ctx, slotOf(m, r.SlotID), r); err != nil {
				t.Fatal(err)
			}
			mustSeal(t, s, r)
			t0 := time.Now()
			if err := s.Verify(ctx, r.ExpID, r.RunID); err != nil {
				t.Fatal(err)
			}
			if err := ix.Rebuild(ctx); err != nil {
				t.Fatal(err)
			}
			t1 := time.Now()
			if err := s.VerifySealed(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := ix.RefreshSlots(ctx, r.ExpID, []string{r.SlotID}); err != nil {
				t.Fatal(err)
			}
			c.before += t1.Sub(t0)
			c.after += time.Since(t1)
		}
		return c
	}
	fill := func(from, to int) {
		for i := from; i < to; i++ {
			r := runIn(m, i)
			if _, err := s.NewRun(ctx, slotOf(m, r.SlotID), r); err != nil {
				t.Fatal(err)
			}
			mustSeal(t, s, r)
		}
		if err := ix.Rebuild(ctx); err != nil {
			t.Fatal(err)
		}
	}
	small := measure(0)
	fill(probe, total-probe)
	big := measure(total - probe)
	per := func(d time.Duration) time.Duration { return d / probe }
	t.Logf("per seal at %d runs: before %v, after %v", probe, per(small.before), per(small.after))
	t.Logf("per seal at %d runs: before %v, after %v", total, per(big.before), per(big.after))
	if big.after >= big.before {
		t.Fatalf("at %d runs the new path (%v) is not cheaper than the old one (%v)", total, big.after, big.before)
	}
	// The old path grows with the run count; the new one does no disk work
	// that depends on it. What is left is an in-memory copy of the
	// experiment's index entries per refresh, which the server bounds by
	// debouncing refreshes (cmd/bifrost-bench/indexrefresh.go), and timer
	// noise on a shared machine.
	if big.after*5 > big.before {
		t.Fatalf("at %d runs the new path (%v per seal) is not 5x cheaper than the old one (%v)", total, per(big.after), per(big.before))
	}
	if big.after > 10*small.after+5*time.Millisecond*probe {
		t.Fatalf("new path grew from %v to %v per seal", per(small.after), per(big.after))
	}
}
