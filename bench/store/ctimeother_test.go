//go:build !linux

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestChangeTimeOther(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if changeTime(info) != 0 {
		t.Fatal("change time is read where it is not supported")
	}
}
