package main

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/maximhq/bifrost/bench/lib"
)

func TestRunInit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	cfg, err := lib.LoadConfig(filepath.Join(dir, "deploy", "bench.yaml"))
	if err != nil {
		t.Fatalf("project config: %v", err)
	}
	if cfg.Root != dir || cfg.Path(cfg.BenchmarksRoot) != filepath.Join(dir, "benchmarks") {
		t.Fatalf("config root %s benchmarks %s", cfg.Root, cfg.BenchmarksRoot)
	}
	if code := runInit([]string{dir}); code != exitInfra {
		t.Fatalf("init into a non-empty folder: exit %d, want %d", code, exitInfra)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runInit([]string{"-force", dir, "-name", "acme"}); code != exitOK {
		t.Fatalf("init -force exit %d", code)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "README.md")); err != nil || string(b) == "mine" {
		t.Errorf("-force did not rewrite README.md: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, "keep.txt")); err != nil || string(b) != "keep" {
		t.Errorf("-force touched a file that is not the scaffold's: %v", err)
	}
	t.Chdir(t.TempDir()) // First workflow: with no folder, init detects the repository it runs in; outside one it is a usage error
	if code := runInit(nil); code != exitUsage {
		t.Fatalf("init without a folder outside a repository: exit %d", code)
	}
}

func TestParseArgs(t *testing.T) {
	fs := flag.NewFlagSet("x", flag.ContinueOnError)
	tmpl := fs.String("template", "", "")
	force := fs.Bool("force", false, "")
	pos, err := parseArgs(fs, []string{"one", "-template", "go-test", "two", "-force"})
	if err != nil || !reflect.DeepEqual(pos, []string{"one", "two"}) || *tmpl != "go-test" || !*force {
		t.Fatalf("pos %v err %v template %q force %v", pos, err, *tmpl, *force)
	}
	if _, err := parseArgs(flag.NewFlagSet("y", flag.ContinueOnError), []string{"-nope"}); err == nil {
		t.Fatal("want error for an unknown flag")
	}
}

func TestBenchmarksRoot(t *testing.T) {
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	config := filepath.Join(dir, "deploy", "bench.yaml")
	tests := []struct {
		name    string
		args    []string
		root    string
		want    string
		wantErr bool
	}{
		{"explicit root wins", []string{"-config", config}, "elsewhere", "elsewhere", false},
		{"from the config", []string{"-config", config}, "", filepath.Join(dir, "benchmarks"), false},
		{"missing explicit config", []string{"-config", filepath.Join(dir, "nope.yaml")}, "", "", true},
		{"missing default config falls back", nil, "", "benchmarks", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			cfgPath := fs.String("config", filepath.Join(dir, "absent", "bench.yaml"), "")
			if err := fs.Parse(tc.args); err != nil {
				t.Fatal(err)
			}
			got, _, err := benchmarksRoot(fs, tc.root, *cfgPath)
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("root %q err %v, want %q (error %v)", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
