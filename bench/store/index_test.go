package store

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// indexFixture builds e_test, where slot a1 has an aborted attempt 1 and a
// passing attempt 2 with every optional file, b1 has one sealed run with no
// optional files and c1 (uncounted) has one sealed run, plus e_open with one
// unsealed run.
func indexFixture(t *testing.T) (*FSStore, map[string]*schemas.SealRecord) {
	t.Helper()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	seals := map[string]*schemas.SealRecord{}

	aborted := mustNewRun(t, s, m, "s_a1", "r_a1_1", 1, schemas.OutcomeAbortedInfra)
	seals[aborted.RunID] = mustSeal(t, s, aborted)

	pass := mustNewRun(t, s, m, "s_a1", "r_a1_2", 2, schemas.OutcomePass)
	mustWrite(t, s, pass, "grade.json", `{"passed":true,"tests_ran":4,"suite_version":"1.0.0","grader_version":"1.0.0"}`)
	mustWrite(t, s, pass, "metrics.json", `{"verified_success":{"state":"observed","v":1,"evidence":"outcome"}}`)
	mustWrite(t, s, pass, "gates.json", `[{"gate":"g1","version":"1.0.0","effect":"veto","fired":false}]`)
	mustWrite(t, s, pass, "receipts.json", `[{"probe_id":"stale_notes","layer":"fixture","steps":{}}]`)
	mustWrite(t, s, pass, "evidence.json", `{"requests":{"complete":true,"row_ids":["q1","q2","q3","q4"],"dropped_global":3,"requests":[`+
		`{"id":"q1","kind":"message","status":"success","tokens":{"fresh":10,"cache_read":0,"cache_write":0,"out":2}},`+
		`{"id":"q2","kind":"message","status":"success"},`+
		`{"id":"q3","kind":"message","status":"error","error_status":503},`+
		`{"id":"q4","kind":"count_tokens","status":"success"}]}}`)
	seals[pass.RunID] = mustSeal(t, s, pass)

	b := mustNewRun(t, s, m, "s_b1", "r_b1", 1, schemas.OutcomeFail)
	seals[b.RunID] = mustSeal(t, s, b)

	c := mustNewRun(t, s, m, "s_c1", "r_c1", 1, schemas.OutcomePass)
	seals[c.RunID] = mustSeal(t, s, c)

	m2 := mustCreateExp(t, s, "e_open")
	mustNewRun(t, s, m2, "s_a1", "r_open", 1, schemas.OutcomeUnknown)
	return s, seals
}

func TestSealedRuns(t *testing.T) {
	ctx := context.Background()
	s, seals := indexFixture(t)
	regraded := &schemas.Grade{Passed: false, Reason: "clean regrade failed", SuiteVersion: "1.1.0", GraderVersion: "1.0.0"}
	if err := s.WriteRegrade(ctx, "e_test", "rg_1", "r_a1_2", regraded); err != nil {
		t.Fatalf("WriteRegrade: %v", err)
	}
	type want struct {
		runID       string
		gradeSource string
		passed      *bool
	}
	yes, no := true, false
	tests := []struct {
		name        string
		gradeSource string
		want        []want
		wantErr     error
	}{
		{name: "original", gradeSource: "original", want: []want{{"r_a1_2", "original", &yes}, {"r_b1", "original", nil}}},
		{name: "empty means original", gradeSource: "", want: []want{{"r_a1_2", "original", &yes}, {"r_b1", "original", nil}}},
		{name: "regrade with fallback", gradeSource: "rg_1", want: []want{{"r_a1_2", "rg_1", &no}, {"r_b1", "original", nil}}},
		{name: "unknown regrade", gradeSource: "rg_none", wantErr: schemas.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.SealedRuns(ctx, "e_test", tc.gradeSource)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SealedRuns: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %d runs, want %d: %+v", len(got), len(tc.want), got)
			}
			for i, w := range tc.want {
				g := got[i]
				if g.Run.RunID != w.runID || g.GradeSource != w.gradeSource {
					t.Fatalf("run %d = %s/%s, want %s/%s", i, g.Run.RunID, g.GradeSource, w.runID, w.gradeSource)
				}
				if g.SealHash != seals[w.runID].Hash || g.Slot.SlotID != g.Run.SlotID {
					t.Fatalf("run %d seal/slot mismatch: %+v", i, g)
				}
				switch {
				case w.passed == nil && g.Grade != nil:
					t.Fatalf("run %d: grade %+v, want none", i, g.Grade)
				case w.passed != nil && (g.Grade == nil || g.Grade.Passed != *w.passed):
					t.Fatalf("run %d: grade %+v, want passed=%v", i, g.Grade, *w.passed)
				}
			}
			first := got[0]
			if v := first.Metrics["verified_success"]; v.State != schemas.ValueObserved || v.V == nil || *v.V != 1 {
				t.Fatalf("metrics = %+v", first.Metrics)
			}
			if len(first.Gates) != 1 || len(first.Receipts) != 1 {
				t.Fatalf("gates/receipts = %+v / %+v", first.Gates, first.Receipts)
			}
			if tc.gradeSource == "rg_1" && first.Grade.ParentSeal != seals["r_a1_2"].Hash {
				t.Fatalf("regrade parent_seal = %s", first.Grade.ParentSeal)
			}
			if len(got[1].Metrics) != 0 || got[1].Gates != nil || got[1].Gateway != nil {
				t.Fatalf("missing optional files: %+v", got[1])
			}
			// One success row with usage, one without, an error row and a
			// count_tokens row: two model requests should carry usage.
			gw := first.Gateway
			if gw == nil || gw.Requests != 2 || gw.WithUsage != 1 || !gw.Complete || gw.Dropped == nil || *gw.Dropped != 3 {
				t.Fatalf("gateway tally = %+v", gw)
			}
		})
	}
}

func TestSealedRunsAllAbortedSlotIsKept(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_test")
	for i, id := range []string{"r_1", "r_2", "r_3"} {
		r := mustNewRun(t, s, m, "s_a1", id, i+1, schemas.OutcomeAbortedInfra)
		mustSeal(t, s, r)
	}
	got, err := s.SealedRuns(ctx, "e_test", "original")
	if err != nil {
		t.Fatalf("SealedRuns: %v", err)
	}
	if len(got) != 1 || got[0].Run.RunID != "r_3" {
		t.Fatalf("got %+v, want the last aborted attempt r_3", got)
	}
}

func TestIndex(t *testing.T) {
	ctx := context.Background()
	s, seals := indexFixture(t)
	ix := NewIndex(s)
	if len(ix.Experiments()) != 0 {
		t.Fatal("index not empty before Rebuild")
	}
	if err := ix.Rebuild(ctx); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if got := ix.Experiments(); len(got) != 2 || got[0] != "e_open" || got[1] != "e_test" {
		t.Fatalf("experiments = %v", got)
	}
	if m, ok := ix.Manifest("e_test"); !ok || m.ExpID != "e_test" {
		t.Fatalf("manifest = %+v, %v", m, ok)
	}
	if _, ok := ix.Manifest("e_none"); ok {
		t.Fatal("unknown manifest found")
	}
	runs, ok := ix.Runs("e_test")
	if !ok || len(runs) != 4 {
		t.Fatalf("runs = %d, %v, want 4 including uncounted", len(runs), ok)
	}
	wantOrder := []string{"r_a1_1", "r_a1_2", "r_b1", "r_c1"}
	for i, r := range runs {
		if r.Run.RunID != wantOrder[i] {
			t.Fatalf("run %d = %s, want %s", i, r.Run.RunID, wantOrder[i])
		}
	}
	lookups := []struct {
		runID    string
		ok       bool
		sealed   bool
		counted  bool
		sealHash string
	}{
		{"r_a1_2", true, true, true, seals["r_a1_2"].Hash},
		{"r_c1", true, true, false, seals["r_c1"].Hash},
		{"r_open", true, false, true, ""},
		{"r_none", false, false, false, ""},
	}
	for _, tc := range lookups {
		t.Run("run "+tc.runID, func(t *testing.T) {
			r, ok := ix.Run(tc.runID)
			if ok != tc.ok {
				t.Fatalf("ok = %v", ok)
			}
			if ok && (r.Sealed != tc.sealed || r.Slot.Counted != tc.counted || r.SealHash != tc.sealHash) {
				t.Fatalf("entry = %+v", r)
			}
		})
	}
	pair, ok := ix.Pair("p_1")
	if !ok || len(pair) != 3 || pair[0].Slot.Arm != "a" || pair[2].Slot.Arm != "b" {
		t.Fatalf("pair = %+v", pair)
	}
	if _, ok := ix.Pair("p_none"); ok {
		t.Fatal("unknown pair found")
	}
}

func TestIndexRunFile(t *testing.T) {
	s, _ := indexFixture(t)
	ix := NewIndex(s)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		runID   string
		file    string
		wantErr error
	}{
		{name: "run.json", runID: "r_a1_2", file: "run.json"},
		{name: "seal.json", runID: "r_a1_2", file: "seal.json"},
		{name: "metrics.json", runID: "r_a1_2", file: "metrics.json"},
		{name: "allowed but missing", runID: "r_b1", file: "metrics.json", wantErr: schemas.ErrNotFound},
		{name: "not in allowlist", runID: "r_a1_2", file: "notes.txt", wantErr: schemas.ErrNotFound},
		{name: "traversal", runID: "r_a1_2", file: "../../../manifest.json", wantErr: schemas.ErrNotFound},
		{name: "traversal to allowed name", runID: "r_a1_2", file: "../r_a1_1/run.json", wantErr: schemas.ErrNotFound},
		{name: "absolute", runID: "r_a1_2", file: "/etc/passwd", wantErr: schemas.ErrNotFound},
		{name: "unknown run", runID: "r_none", file: "run.json", wantErr: schemas.ErrNotFound},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ix.RunFile(tc.runID, tc.file)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || len(b) == 0 {
				t.Fatalf("RunFile = %d bytes, %v", len(b), err)
			}
		})
	}
}

// TestIndexRebuildSwapsAtomically reads the index from many goroutines while
// it rebuilds; -race checks that nothing is mutated in place, and a reader
// holding an old result keeps it unchanged.
func TestIndexRebuildSwapsAtomically(t *testing.T) {
	ctx := context.Background()
	s, _ := indexFixture(t)
	ix := NewIndex(s)
	if err := ix.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := ix.Runs("e_open")
	m, ok := ix.Manifest("e_open")
	if !ok {
		t.Fatal("e_open not indexed")
	}
	mustNewRun(t, s, m, "s_b1", "r_new", 1, schemas.OutcomeUnknown)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ix.Experiments()
				if runs, ok := ix.Runs("e_test"); ok && len(runs) != 4 {
					t.Errorf("e_test runs = %d during rebuild", len(runs))
					return
				}
				ix.Pair("p_1")
				ix.Run("r_open")
			}
		}()
	}
	for range 5 {
		if err := ix.Rebuild(ctx); err != nil {
			t.Errorf("Rebuild: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	after, _ := ix.Runs("e_open")
	if len(before) != 1 || len(after) != 2 {
		t.Fatalf("runs before/after = %d/%d, want 1/2", len(before), len(after))
	}
	if _, ok := ix.Run("r_new"); !ok {
		t.Fatal("new run not indexed")
	}
}
