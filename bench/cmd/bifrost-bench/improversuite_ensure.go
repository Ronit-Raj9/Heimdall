package main

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// improverSuiteFS is the shipped improver suite (benchmarks/heimdall-improver,
// package improversuite), set by the file that embeds it. Nil means this
// build ships no improver suite; ensureImproverSuite then copies nothing.
var improverSuiteFS fs.FS

// improverSuiteDir is where the shipped improver suite lives in a project.
const improverSuiteDir = "benchmarks/heimdall-improver"

// ensureImproverSuite copies the shipped improver suite into the project
// root when the folder is missing or its suite.lock differs from the
// shipped one, so the default improver task needs no setup (A1.6).
func ensureImproverSuite(projectRoot string) error {
	if improverSuiteFS == nil || projectRoot == "" {
		return nil
	}
	dst := filepath.Join(projectRoot, filepath.FromSlash(improverSuiteDir))
	want, err := fs.ReadFile(improverSuiteFS, "suite.lock")
	if err != nil {
		return fmt.Errorf("the shipped improver suite has no suite.lock: %w", err)
	}
	if have, err := os.ReadFile(filepath.Join(dst, "suite.lock")); err == nil && bytes.Equal(have, want) {
		return nil
	}
	return fs.WalkDir(improverSuiteFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dst, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if filepath.Ext(p) == ".go" {
			return nil
		}
		b, err := fs.ReadFile(improverSuiteFS, p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if filepath.Ext(p) == ".sh" {
			mode = 0o755
		}
		return os.WriteFile(target, b, mode)
	})
}
