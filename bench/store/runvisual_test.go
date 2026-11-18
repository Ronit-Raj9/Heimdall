package store

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X1 vision grading: RunVisual serves only visual/{capture,reference,diff}.png.
func TestIndexRunVisual(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_vis")
	run := mustNewRun(t, s, m, "s_a1", "r_vis", 1, schemas.OutcomePass)
	mustWrite(t, s, run, "visual/capture.png", "png-bytes")
	mustWrite(t, s, run, "visual/other.png", "not served")
	mustSeal(t, s, run)
	// An unsealed run whose diff.png is a symlink, which is never followed.
	open := mustNewRun(t, s, m, "s_b1", "r_vis_open", 1, schemas.OutcomeUnknown)
	mustWrite(t, s, open, "visual/capture.png", "png-bytes")
	dir := s.runDir(open)
	if err := os.Symlink(filepath.Join(dir, "visual", "capture.png"), filepath.Join(dir, "visual", "diff.png")); err != nil {
		t.Fatal(err)
	}
	ix := NewIndex(s)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := ix.RunVisual("r_vis", "capture.png")
	if err != nil || !bytes.Equal(b, []byte("png-bytes")) {
		t.Fatalf("RunVisual = %q, %v", b, err)
	}
	for _, name := range []string{"other.png", "diff.png", "reference.png", "../grade.json", "capture.png/.."} {
		if _, err := ix.RunVisual("r_vis", name); !errors.Is(err, schemas.ErrNotFound) {
			t.Errorf("RunVisual(%q) = %v, want not found", name, err)
		}
	}
	if _, err := ix.RunVisual("r_vis_open", "diff.png"); !errors.Is(err, schemas.ErrNotFound) {
		t.Errorf("symlinked diff.png: %v, want not found", err)
	}
	if _, err := ix.RunVisual("r_none", "capture.png"); !errors.Is(err, schemas.ErrNotFound) {
		t.Errorf("unknown run: %v", err)
	}
}
