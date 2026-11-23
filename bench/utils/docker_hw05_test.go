package utils

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDockerAvailable covers N218: a process reaches Docker only when its
// docker binary is on PATH (or the named file exists and is executable).
func TestDockerAvailable(t *testing.T) {
	if (Docker{Binary: filepath.Join(t.TempDir(), "no-docker")}).Available() {
		t.Fatal("a missing docker binary reported available")
	}
	bin := filepath.Join(t.TempDir(), "docker")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !(Docker{Binary: bin}).Available() {
		t.Fatal("an executable docker binary reported missing")
	}
}
