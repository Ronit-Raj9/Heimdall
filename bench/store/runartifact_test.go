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

func TestIndexRunArtifact(t *testing.T) {
	s := newTestStore(t, nil)
	m := mustCreateExp(t, s, "e_art")
	sealed := mustNewRun(t, s, m, "s_a1", "r_art", 1, schemas.OutcomePass)
	payload := "a,b\n1,\x002\n"
	mustWrite(t, s, sealed, "artifacts/report/report.csv", payload)
	mustWrite(t, s, sealed, "secret.txt", "not an artifact")
	mustSeal(t, s, sealed)

	// An unsealed run whose artifacts folder holds symlinks, which are never followed.
	open := mustNewRun(t, s, m, "s_b1", "r_open", 1, schemas.OutcomeUnknown)
	mustWrite(t, s, open, "artifacts/real/ok.txt", "ok")
	dir := s.runDir(open)
	if err := os.Symlink(filepath.Join(dir, "artifacts", "real", "ok.txt"), filepath.Join(dir, "artifacts", "real", "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "artifacts", "real"), filepath.Join(dir, "artifacts", "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "artifacts", "nested", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}

	ix := NewIndex(s)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	b, err := ix.RunArtifact("r_art", "report", "report.csv")
	if err != nil || !bytes.Equal(b, []byte(payload)) {
		t.Fatalf("RunArtifact = %q, %v", b, err)
	}
	if b, err := ix.RunArtifact("r_open", "real", "ok.txt"); err != nil || string(b) != "ok" {
		t.Fatalf("unsealed RunArtifact = %q, %v", b, err)
	}

	refused := []struct {
		name, runID, id, file string
	}{
		{"unknown run", "r_none", "report", "report.csv"},
		{"unknown id", "r_art", "chart", "report.csv"},
		{"unknown name", "r_art", "report", "other.csv"},
		{"dot dot name", "r_art", "report", ".."},
		{"dot dot id", "r_art", "..", "secret.txt"},
		{"traversal in name", "r_art", "report", "../../secret.txt"},
		{"slash in name", "r_art", "report", "a/b"},
		{"backslash in name", "r_art", "report", `..\secret.txt`},
		{"encoded name", "r_art", "report", "..%2Fsecret.txt"},
		{"absolute name", "r_art", "report", "/etc/passwd"},
		{"upper case id", "r_art", "Report", "report.csv"},
		{"empty id", "r_art", "", "report.csv"},
		{"empty name", "r_art", "report", ""},
		{"file symlink", "r_open", "real", "link.txt"},
		{"folder symlink", "r_open", "linked", "ok.txt"},
		{"folder, not file", "r_open", "nested", "deep"},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ix.RunArtifact(tc.runID, tc.id, tc.file)
			if !errors.Is(err, schemas.ErrNotFound) {
				t.Fatalf("RunArtifact = %q, %v; want ErrNotFound", b, err)
			}
		})
	}
}
