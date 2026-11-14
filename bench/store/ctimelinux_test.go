//go:build linux

package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestChangeTime(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(p, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	first := changeTime(info)
	if first == 0 {
		t.Fatal("change time is 0")
	}
	time.Sleep(10 * time.Millisecond)
	// Putting the modification time back still moves the change time.
	if err := os.Chtimes(p, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	again, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if changeTime(again) == first {
		t.Fatal("chtimes left the change time unchanged")
	}
}
