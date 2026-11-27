package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// TestServeAnalyzeRebuildsOnlyAfterSave: the serve analyzer rebuilds the
// organization's index only when it saved a new analysis. Reopening a
// finished comparison returns the saved analysis and rebuilds nothing; the
// answer is the one a fresh analysis gives.
func TestServeAnalyzeRebuildsOnlyAfterSave(t *testing.T) {
	ctx := context.Background()
	a, m := analysisFixture(t)
	x := analyzerAdapter{a: a, ix: store.NewIndex(a.store), locks: &sync.Map{}, rebuilds: &atomic.Int64{}}
	start := x.rebuilds.Load()
	first, err := x.Analyze(ctx, m.ExpID, "")
	if err != nil {
		t.Fatal(err)
	}
	if n := x.rebuilds.Load() - start; n != 1 {
		t.Fatalf("first analysis: %d rebuilds, want 1 (it saved)", n)
	}
	for i := 0; i < 3; i++ { // verdict, diagnostics, adaptive of one tab
		again, err := x.Analyze(ctx, m.ExpID, "")
		if err != nil {
			t.Fatal(err)
		}
		if again.AnalysisID != first.AnalysisID {
			t.Fatalf("reopen saved %s, want the saved %s", again.AnalysisID, first.AnalysisID)
		}
	}
	if n := x.rebuilds.Load() - start; n != 1 {
		t.Fatalf("after 3 reopens: %d rebuilds, want 1", n)
	}
	t.Logf("index rebuilds for 1 analysis + 3 reopens: 4 before the fix, %d now", x.rebuilds.Load()-start)

	// The reused answer equals a fresh one: a recompute that always saves
	// gives the same analysis apart from its ID and time, and the same report.
	fresh, report, err := a.analyzeWith(ctx, m.ExpID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	kf, _ := analysisKey(fresh)
	k1, _ := analysisKey(first)
	if kf != k1 {
		t.Fatalf("reused analysis differs from a fresh one\nreused %s\nfresh  %s", k1, kf)
	}
	saved, err := os.ReadFile(filepath.Join(a.store.Root(), m.ExpID, "analysis", first.AnalysisID, "report.md"))
	if err != nil || reportKey(string(saved), first) != reportKey(string(report), fresh) {
		t.Fatalf("reused report differs from a fresh one (%v)", err)
	}

	// A changed input (here the original or reviewed switch) still computes
	// and saves its own analysis, so the index is rebuilt.
	before := x.rebuilds.Load()
	rev, err := x.Analyze(ctx, m.ExpID, schemas.GradeSourceReviewed)
	if err != nil {
		t.Fatal(err)
	}
	if rev.GradeSource != schemas.GradeSourceReviewed || x.rebuilds.Load()-before != 1 {
		t.Fatalf("grade source switch: source %s, rebuilds %d", rev.GradeSource, x.rebuilds.Load()-before)
	}
}
