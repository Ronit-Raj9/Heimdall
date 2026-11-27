package monitorcert

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestCertStoreDelete: a certificate folder is removed; a missing one, a
// bad id and a file in place of a folder are ErrNotFound; nothing outside
// the root is touched.
func TestCertStoreDelete(t *testing.T) {
	root := t.TempDir()
	s, err := NewCertStore(filepath.Join(root, "certs"), filepath.Join(root, "runs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := s.Write(testCert("mc_a", time.Now()), []byte("# a\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "mc_a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, "mc_a"); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "certs", "mc_file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"mc_a", "../keep", "..", "mc_file"} {
		if err := s.Delete(ctx, id); !errors.Is(err, schemas.ErrNotFound) {
			t.Fatalf("%s: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "keep")); err != nil {
		t.Fatalf("outside file: %v", err)
	}
}
