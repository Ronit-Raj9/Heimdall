package main

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/export"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// TestEEEValidityMatchesSnapshot covers QC2-23-04: the EEE export reads the
// verdict of its own grade source, and an analysis whose inputs digest no
// longer matches the exported runs cannot authorize them.
func TestEEEValidityMatchesSnapshot(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join(recordedRunsRoot, smokeExp), filepath.Join(root, smokeExp))
	ctx := context.Background()
	st, err := store.NewFSStore(root, schemas.NoopLogger{})
	if err != nil {
		t.Fatal(err)
	}
	runs, err := st.SealedRuns(ctx, smokeExp, schemas.GradeSourceOriginal)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := utils.AnalysisInputsDigest(runs)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	write := func(id, source, inputs string, d schemas.Decision) {
		t.Helper()
		at = at.Add(time.Hour)
		a := &schemas.Analysis{AnalysisID: id, ExpID: smokeExp, CreatedAt: at, GradeSource: source, InputsDigest: inputs,
			Verdict: schemas.Verdict{Decision: d}}
		if err := st.WriteAnalysis(ctx, a, nil); err != nil {
			t.Fatal(err)
		}
	}
	load := func() export.EEEInput {
		t.Helper()
		in, err := loadResultsFrom(ctx, st, smokeExp, schemas.GradeSourceOriginal)
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	write("a_zz000001", schemas.GradeSourceOriginal, digest, schemas.DecisionInvalid)
	write("a_zz000002", "rg_later", "other", schemas.DecisionInconclusive)
	if in := load(); !in.Invalid || in.Stale {
		t.Fatalf("a later regrade's verdict replaced the original's INVALID: %+v", in.Invalid)
	}
	write("a_zz000003", schemas.GradeSourceOriginal, "not-these-runs", schemas.DecisionInconclusive)
	in := load()
	if in.Invalid || !in.Stale {
		t.Fatalf("stale analysis: invalid %v stale %v", in.Invalid, in.Stale)
	}
	if _, err := export.EEE(ctx, in, export.EEEOptions{AllowSynthetic: true, GradeSource: schemas.GradeSourceOriginal}); !errors.Is(err, export.ErrStale) {
		t.Fatalf("EEE over a stale verdict: %v", err)
	}
}
