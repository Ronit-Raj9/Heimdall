package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestFollowUpBindingSurvivesArchive is the regression test for audit
// qc2-03-01: the first follow-up binds the parent's alpha when it is frozen,
// and removing its manifest (as archiving does) never rebinds a sibling.
func TestFollowUpBindingSurvivesArchive(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, nil)
	parent := mustCreateExp(t, s, "e_parent")
	child := func(id, hash string) {
		t.Helper()
		m := testManifest(id)
		m.Experiment.Extends = &schemas.ExtendsRef{ExpID: parent.ExpID, ManifestHash: hash, Kind: "tasks"}
		if err := s.CreateExperiment(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	child("e_other", "sha256:another-freeze") // wrong parent hash: binds nothing
	if _, ok, err := s.FollowUp(parent.ExpID, parent.Hash); err != nil || ok {
		t.Fatalf("binding after a mismatched child: ok %v err %v", ok, err)
	}
	child("e_first", parent.Hash)
	child("e_second", parent.Hash)
	got, ok, err := s.FollowUp(parent.ExpID, parent.Hash)
	if err != nil || !ok || got != "e_first" {
		t.Fatalf("binding %q ok %v err %v, want e_first", got, ok, err)
	}
	// Archiving e_first leaves only a stub in its folder.
	if err := os.Remove(filepath.Join(s.Root(), "e_first", manifestFile)); err != nil {
		t.Fatal(err)
	}
	if got, err := s.BindFollowUp(parent.ExpID, parent.Hash, "e_second"); err != nil || got != "e_first" {
		t.Fatalf("rebind after archive gave %q err %v, want e_first", got, err)
	}
}
