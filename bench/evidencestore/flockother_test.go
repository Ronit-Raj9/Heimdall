//go:build !unix

package evidencestore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFlockNoop(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "x.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := flock(f); err != nil {
		t.Fatal(err)
	}
	if err := funlock(f); err != nil {
		t.Fatal(err)
	}
}
