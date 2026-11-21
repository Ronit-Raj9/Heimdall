package main

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// TestOfflineModeNeverLive is the regression test for audit finding 7-B-03:
// `serve -offline` replays recorded runs, so a bench.yaml that says live is
// served as recorded and a strict client refuses it.
func TestOfflineModeNeverLive(t *testing.T) {
	tests := []struct {
		configured schemas.Mode
		offline    bool
		want       schemas.Mode
	}{
		{schemas.ModeLive, true, schemas.ModeRecorded},
		{schemas.ModeLive, false, schemas.ModeLive},
		{schemas.ModeRecorded, true, schemas.ModeRecorded},
		{schemas.ModeSynthetic, true, schemas.ModeSynthetic},
	}
	for _, tt := range tests {
		if got := offlineMode(tt.configured, tt.offline); got != tt.want {
			t.Errorf("offlineMode(%s, %v) = %s, want %s", tt.configured, tt.offline, got, tt.want)
		}
	}
}

// TestHostedOfflineKeepsMode covers QC2-20-01: -offline on a hosted server
// does not relabel live runner results as recorded.
func TestHostedOfflineKeepsMode(t *testing.T) {
	if got := serveMode(schemas.ModeLive, true, true); got != schemas.ModeLive {
		t.Fatalf("hosted -offline mode = %s, want live", got)
	}
	if got := serveMode(schemas.ModeLive, true, false); got != schemas.ModeRecorded {
		t.Fatalf("local -offline mode = %s, want recorded", got)
	}
}

// TestOfflineDoctorRunsNothing covers QC2-20-02: the offline doctor is
// static (it has no app, so it cannot reach Docker or the gateway).
func TestOfflineDoctorRunsNothing(t *testing.T) {
	for _, c := range offlineDoctor(context.Background()) {
		if c.Status != schemas.DoctorSkip {
			t.Fatalf("offline doctor row %+v", c)
		}
	}
}

// TestOfflineStartsNoEvidenceMirror covers QC2-20-06: serve -offline with
// a configured mirror starts no background sweep.
func TestOfflineStartsNoEvidenceMirror(t *testing.T) {
	a := &app{cfg: &lib.Config{Root: t.TempDir()}, logger: schemas.NoopLogger{}}
	a.cfg.Storage.MirrorDir = t.TempDir()
	if !a.cfg.Storage.Enabled() {
		t.Fatal("fixture: storage not enabled")
	}
	serveEvidenceMirror(context.Background(), a, true)
	evidenceMu.Lock()
	started := evidenceMirror != nil
	evidenceMu.Unlock()
	if started {
		t.Fatal("offline server started the evidence mirror")
	}
}

// TestServeAnalyzeConcurrentSavesOnce is the regression test for live lane
// l9: one experiment page load sends the verdict, diagnostics and cost
// requests at once, and each saved its own copy of the same analysis into
// the run folder. The serve analyzer serializes them, so every concurrent
// request gets the one saved analysis.
func TestServeAnalyzeConcurrentSavesOnce(t *testing.T) {
	a, m := analysisFixture(t)
	ix := store.NewIndex(a.store)
	x := analyzerAdapter{a: a, ix: ix, locks: &sync.Map{}}
	const n = 6
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			an, err := x.Analyze(context.Background(), m.ExpID, "")
			if err != nil {
				t.Errorf("analyze: %v", err)
				return
			}
			ids[i] = an.AnalysisID
		}(i)
	}
	wg.Wait()
	entries, err := os.ReadDir(filepath.Join(a.store.Root(), m.ExpID, "analysis"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d analyses saved by %d concurrent requests, want 1", len(entries), n)
	}
	for _, id := range ids {
		if id != entries[0].Name() {
			t.Fatalf("request got analysis %s, saved %s", id, entries[0].Name())
		}
	}
}
