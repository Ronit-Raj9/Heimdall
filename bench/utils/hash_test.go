package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTreeHash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	h1, err := TreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	h2, err := TreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("tree hash did not change after an edit")
	}
}

func TestHashJSONStable(t *testing.T) {
	a, err := HashJSON(map[string]int{"b": 2, "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashJSON(map[string]int{"a": 1, "b": 2})
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatal("map key order changed the hash")
	}
}

func TestCanonicalTreeHashRules(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, mode os.FileMode, data []byte) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, mode); err != nil {
			t.Fatal(err)
		}
	}
	write("b.bin", 0o644, []byte{0, 1, 2, 255})
	write("a.txt", 0o600, []byte("line 1\nline 2"))
	h1, err := CanonicalTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	write(".git/config", 0o644, []byte("generated"))
	write("graphify-out/graph.json", 0o644, []byte("generated"))
	write("credentials.txt", 0o644, []byte("secret"))
	h2, err := CanonicalTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatal("excluded files changed the canonical tree hash")
	}
	if err := os.Chmod(filepath.Join(dir, "a.txt"), 0o640); err != nil {
		t.Fatal(err)
	}
	h3, err := CanonicalTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h3 != h2 {
		t.Fatal("non-executable mode changed the canonical tree hash")
	}
	if err := os.Chmod(filepath.Join(dir, "a.txt"), 0o750); err != nil {
		t.Fatal(err)
	}
	h4, err := CanonicalTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if h4 == h3 {
		t.Fatal("executable mode did not change the canonical tree hash")
	}
}

func TestCanonicalTreeHashPreservesNewlinesAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "text.txt")
	if err := os.WriteFile(path, []byte("a\nb\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hLF, err := CanonicalTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hCRLF, err := CanonicalTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if hLF == hCRLF {
		t.Fatal("newline conversion was hidden by canonical hashing")
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink("text.txt", link); err != nil {
		t.Fatal(err)
	}
	hLink, err := CanonicalTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	hTarget, err := CanonicalTreeHash(dir)
	if err != nil {
		t.Fatal(err)
	}
	if hLink == hTarget {
		t.Fatal("target content change did not change the canonical tree hash")
	}
}
