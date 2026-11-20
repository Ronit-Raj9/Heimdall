package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestImportHonorsBenchmarksRoot covers 7-C-09: import writes the suite under
// the config's benchmarks_root, where suite lint, plan and run look for it.
func TestImportHonorsBenchmarksRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "deploy", "bench.yaml")
	body := "runs_root: runs\nbenchmarks_root: datasets/benchmarks\nserver:\n  listen: 127.0.0.1:0\n  mode: live\n"
	if err := os.WriteFile(cfg, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	from, err := filepath.Abs("../../importers/longmemeval/testdata/longmemeval_oracle.json")
	if err != nil {
		t.Fatal(err)
	}
	code := runImport([]string{"longmemeval", "-config", cfg, "-from", from, "-suite", "audit-memory", "-set", "grading=match", "-limit", "2"})
	if code != exitOK {
		t.Fatalf("import exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, "datasets", "benchmarks", "audit-memory", "suite.yaml")); err != nil {
		t.Fatalf("suite not under benchmarks_root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "benchmarks", "audit-memory")); !os.IsNotExist(err) {
		t.Fatalf("suite also written under the default root: %v", err)
	}
	// No config yet: the default root.
	if d, err := importBenchmarksDir(filepath.Join(root, "missing", "deploy", "bench.yaml")); err != nil || d != "" {
		t.Fatalf("missing config: %q, %v", d, err)
	}
}
