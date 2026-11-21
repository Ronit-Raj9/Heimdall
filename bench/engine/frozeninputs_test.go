package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResumeRefusesChangedFrozenInput is QC2-09-03: a local resume after a
// file the manifest froze outside the suite (an arm's notes.user_file)
// changed must refuse to run more slots under the same manifest.
func TestResumeRefusesChangedFrozenInput(t *testing.T) {
	side := newRemoteSide(t)
	b := side.b
	m := b.plan(t, testExperiment())
	ctx := context.Background()
	if err := side.st.CreateExperiment(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true}); err != nil {
		t.Fatalf("unchanged inputs: %v", err)
	}
	notes := filepath.Join(b.root, "experiments/fixtures/codewins.md")
	if err := os.WriteFile(notes, []byte("changed after the freeze\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := NewScheduler(b.reg, b.root, nil, nil).Run(ctx, m, RunOptions{Resume: true})
	if err == nil || !strings.Contains(err.Error(), "frozen input files changed") {
		t.Fatalf("resume after a frozen input changed: %v", err)
	}
}
