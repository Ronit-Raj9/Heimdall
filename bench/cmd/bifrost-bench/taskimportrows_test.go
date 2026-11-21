package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/scaffold"
)

func TestTaskImportRows(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: "faq", Template: scaffold.TemplateQA})
	if err != nil {
		t.Fatal(err)
	}
	if files, err = scaffold.BlankSuite(files); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.WriteFiles(filepath.Join(dir, "benchmarks", "faq"), files, false); err != nil {
		t.Fatal(err)
	}
	rows := filepath.Join(dir, "rows.csv")
	os.WriteFile(rows, []byte("question,answer\nHow many days is the refund window?,30\n"), 0o644)
	if code := runTaskImportRows([]string{"-suite", "faq", "-file", rows, "-dry-run"}); code != exitOK {
		t.Fatalf("dry run exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(dir, "benchmarks", "faq", "tasks")); err == nil {
		t.Fatal("dry run wrote tasks")
	}
	if code := runTaskImportRows([]string{"-suite", "faq", "-file", rows, "-held-out"}); code != exitOK {
		t.Fatalf("import exit %d", code)
	}
	sy, _ := os.ReadFile(filepath.Join(dir, "benchmarks", "faq", "suite.yaml"))
	ty, _ := os.ReadFile(filepath.Join(dir, "benchmarks", "faq", "tasks", "how-many-days-is-the-refund-window", "task.yaml"))
	if !strings.Contains(string(sy), "how-many-days-is-the-refund-window") || !strings.Contains(string(ty), "split: held_out") {
		t.Fatalf("suite.yaml:\n%s\ntask.yaml:\n%s", sy, ty)
	}
	if code := runTaskImportRows([]string{"-suite", "faq"}); code != exitUsage {
		t.Fatalf("missing -file: %d", code)
	}
}
