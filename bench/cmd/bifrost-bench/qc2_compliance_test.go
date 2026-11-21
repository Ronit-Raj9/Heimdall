package main

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/compliance"
	"github.com/maximhq/bifrost/bench/release"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Audit qc2-01-04: a release-linked compliance pack is built only when the
// live inputs are the evidence the release recorded.
func TestReleaseSnapshotRefusesChangedEvidence(t *testing.T) {
	a, m := analysisFixture(t)
	runs, err := a.store.SealedRuns(context.Background(), m.ExpID, "")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := utils.AnalysisInputsDigest(runs)
	if err != nil {
		t.Fatal(err)
	}
	var hashes []string
	for _, r := range runs {
		hashes = append(hashes, r.SealHash)
	}
	rel := &schemas.ReleaseRecord{ReleaseID: "rel_1", ExpID: m.ExpID, AnalysisID: "a_1", SealsDigest: release.SealsDigest(hashes)}
	ex := compliance.ExperimentInput{Manifest: m, Runs: runs, Analysis: &schemas.Analysis{AnalysisID: "a_1", InputsDigest: digest}}
	if err := releaseSnapshot(rel, ex); err != nil {
		t.Fatalf("unchanged evidence refused: %v", err)
	}
	later := *rel
	later.SealsDigest = release.SealsDigest(hashes[1:])
	if err := releaseSnapshot(&later, ex); err == nil || !strings.Contains(err.Error(), "seals digest") {
		t.Fatalf("other seal set: err = %v", err)
	}
	changed := ex
	changed.Analysis = &schemas.Analysis{AnalysisID: "a_1", InputsDigest: strings.Repeat("0", 64)}
	if err := releaseSnapshot(rel, changed); err == nil || !strings.Contains(err.Error(), "changed after analysis") {
		t.Fatalf("changed inputs: err = %v", err)
	}
}
