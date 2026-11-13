//go:build !unix

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLockFileNoop(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "x.lock"))
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := lockFile(f)
	if err != nil {
		t.Fatalf("lockFile: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("unlock: %v", err)
	}
}
