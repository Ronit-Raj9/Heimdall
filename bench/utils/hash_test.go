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
