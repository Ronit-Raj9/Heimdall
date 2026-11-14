package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSuiteInit(t *testing.T) {
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"init", "web", "-template", "node-test", "-config", config}); code != exitOK {
		t.Fatalf("suite init exit %d", code)
	}
	for _, rel := range []string{"suite.yaml", "base/Dockerfile", "README.md", "probes/model-outage.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, "benchmarks", "web", filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v", rel, err)
		}
	}
	if code := runSuite([]string{"init", "web", "-template", "node-test", "-config", config}); code != exitInfra {
		t.Fatalf("suite init over an existing suite: exit %d", code)
	}
	if code := runSuite([]string{"init", "py", "-config", config, "-template", "python-pytest", "-image", "bench/coding-core-base:1"}); code != exitOK {
		t.Fatalf("suite init -image exit %d", code)
	}
	b, err := os.ReadFile(filepath.Join(dir, "benchmarks", "py", "suite.yaml"))
	if err != nil || !strings.Contains(string(b), "tag: bench/coding-core-base:1") || strings.Contains(string(b), "dockerfile:") {
		t.Fatalf("suite.yaml with -image: %v\n%s", err, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "benchmarks", "py", "base")); !os.IsNotExist(err) {
		t.Error("-image still wrote base/")
	}
	for _, args := range [][]string{
		{"init", "x", "-config", config},
		{"init", "x", "-template", "cobol", "-config", config},
		{"init", "Bad Id", "-template", "go-test", "-config", config},
	} {
		if code := runSuite(args); code == exitOK {
			t.Errorf("suite %v: exit 0", args)
		}
	}
}

// TestSuiteLintLockWithConfig runs lint and lock on a copy of coding-core
// inside a fresh project, resolved through -config.
func TestSuiteLintLockWithConfig(t *testing.T) {
	src := filepath.Join("..", "..", "benchmarks", "coding-core")
	if _, err := os.Stat(filepath.Join(src, "suite.yaml")); err != nil {
		t.Skipf("coding-core not readable: %v", err)
	}
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	dst := filepath.Join(dir, "benchmarks", "coding-core")
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dst, "suite.lock"))
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	if code := runSuite([]string{"lint", "-config", config, "coding-core"}); code != exitOK {
		t.Fatalf("lint exit %d", code)
	}
	if code := runSuite([]string{"lock", "coding-core", "-config", config}); code != exitOK {
		t.Fatalf("lock exit %d", code)
	}
	after, err := os.ReadFile(filepath.Join(dst, "suite.lock"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("re-locking an unchanged coding-core copy changed suite.lock")
	}
}
