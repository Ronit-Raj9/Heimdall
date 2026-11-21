package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMirrorTreeCopiesUpdatesAndPrunes(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(src, "benchmarks", "s", "tasks", "t1", "repo"), 0o755))
	must(os.WriteFile(filepath.Join(src, "benchmarks", "s", "notes.md"), []byte("a"), 0o644))
	must(os.MkdirAll(filepath.Join(src, ".heimdall"), 0o755))
	must(os.WriteFile(filepath.Join(src, ".heimdall", "x"), []byte("skip"), 0o644))
	old := syscall.Umask(0o077) // the runner container's umask must not make files owner-only
	must(mirrorTree(src, dst))
	syscall.Umask(old)
	if fi, err := os.Stat(filepath.Join(dst, "benchmarks", "s", "notes.md")); err != nil || fi.Mode().Perm()&0o444 != 0o444 {
		t.Fatalf("mirrored file not world-readable: %v %v", fi.Mode(), err)
	}
	if b, err := os.ReadFile(filepath.Join(dst, "benchmarks", "s", "notes.md")); err != nil || string(b) != "a" {
		t.Fatalf("copy: %q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(dst, "benchmarks", "s", "tasks", "t1", "repo")); err != nil || !fi.IsDir() {
		t.Fatal("empty folder not mirrored")
	}
	if _, err := os.Stat(filepath.Join(dst, ".heimdall")); !os.IsNotExist(err) {
		t.Fatal(".heimdall mirrored")
	}
	must(os.WriteFile(filepath.Join(src, "benchmarks", "s", "notes.md"), []byte("bb"), 0o644))
	must(os.Remove(filepath.Join(src, "benchmarks", "s", "tasks", "t1", "repo")))
	must(mirrorTree(src, dst))
	if b, _ := os.ReadFile(filepath.Join(dst, "benchmarks", "s", "notes.md")); string(b) != "bb" {
		t.Fatalf("update: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dst, "benchmarks", "s", "tasks", "t1", "repo")); !os.IsNotExist(err) {
		t.Fatal("stale folder kept")
	}
}
