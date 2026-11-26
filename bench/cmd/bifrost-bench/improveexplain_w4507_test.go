package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/lib"
)

func TestOrgImproveExplainSourceReadsTheRunsRoot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "runs", improve.DefaultDir, "imp_x-abc123")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"id":"imp_x-abc123","base":{"ref":"agents/a.yaml@v1","number":1},"rounds":[{"n":1,"parent":{"ref":"agents/a.yaml@v1","number":1,"bundle":{"/n.md":"a\n"}},"candidate":{"ref":"agents/a.yaml@v2","number":2,"bundle":{"/n.md":"a\nb\n"}},"decision":"committed","why":"Kept."}]}`
	if err := os.WriteFile(filepath.Join(dir, improve.RecordFile), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
	src := orgImproveExplainSource(handlers.OrgEnv{Cfg: &lib.Config{Root: root, RunsRoot: "runs"}})
	x, ok := src.(handlers.ImproveExplainer)
	if !ok {
		t.Fatal("the org source does not explain versions")
	}
	out, err := x.Explain(context.Background(), "imp_x-abc123", 2)
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != "committed" || len(out.Files) != 1 || out.Files[0].Path != "/n.md" {
		t.Fatalf("explain = %+v", out)
	}
	if orgImproveExplainSource(handlers.OrgEnv{}) != nil {
		t.Fatal("no config must give no source")
	}
}
