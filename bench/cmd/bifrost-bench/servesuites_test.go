package main

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/suite"
)

// copyTree copies a folder of regular files.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestServeSuites(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "..", "suite", "testdata", "valid", "mini"), filepath.Join(root, "mini"))
	for _, dir := range []string{"notes", ".hidden", "pinned@1"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := serveSuites{root: root}

	ids, err := src.SuiteIDs(ctx)
	if err != nil || strings.Join(ids, ",") != "mini" {
		t.Fatalf("SuiteIDs = %v, %v; want only folders with a suite.yaml", ids, err)
	}
	if ids, err := (serveSuites{root: filepath.Join(root, "missing")}).SuiteIDs(ctx); err != nil || len(ids) != 0 {
		t.Errorf("missing root: %v, %v", ids, err)
	}

	s, err := src.LoadSuite(ctx, "mini")
	if err != nil {
		t.Fatalf("LoadSuite: %v", err)
	}
	lock, err := suite.Lock(s, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := suite.WriteLock(filepath.Join(s.Root, suite.LockFile), lock); err != nil {
		t.Fatal(err)
	}

	t.Run("a fresh lock has no drift", func(t *testing.T) {
		s, err := src.LoadSuite(ctx, "mini")
		if err != nil {
			t.Fatal(err)
		}
		drift, err := src.LockDrift(ctx, s)
		if err != nil || len(drift) != 0 {
			t.Errorf("drift = %v, %v", drift, err)
		}
	})
	t.Run("an edited instruction is MAJOR drift on its task", func(t *testing.T) {
		instr := filepath.Join(root, "mini", "tasks", "m01", "instruction.md")
		if err := os.WriteFile(instr, []byte("changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		s, err := src.LoadSuite(ctx, "mini")
		if err != nil {
			t.Fatal(err)
		}
		drift, err := src.LockDrift(ctx, s)
		if err != nil || len(drift) != 1 {
			t.Fatalf("drift = %v, %v", drift, err)
		}
		d := drift[0]
		if d.Scope != "task" || d.ID != "m01" || d.Level != "MAJOR" || d.Action != "rerun" || d.Fields[0] != "instruction_sha256" {
			t.Errorf("drift = %+v", d)
		}
	})
}

func TestServeAgents(t *testing.T) {
	root := t.TempDir()
	good := "id: noop\nadapter: scripted\nimage: bench/x:1\nscripted:\n  kind: noop\nmodel: m\nnotes:\n  project_file: AGENTS.md\n" +
		"budgets:\n  wall_s: 60\n  cpus: 1\n  mem_gb: 1\ngateway:\n  provider: anthropic\n  max_retries: 0\n"
	files := map[string]string{"agents/noop.yaml": good, "agents/bad.yaml": "id: bad\nadapter: [\n", "agents/notes.md": "x"}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := serveAgents{root: root}.AgentConfigs(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("AgentConfigs = %v, %v", got, err)
	}
	if got[0].Path != "agents/bad.yaml" || got[0].Err == nil || got[0].Config != nil {
		t.Errorf("bad = %+v", got[0])
	}
	if got[1].Path != "agents/noop.yaml" || got[1].Err != nil || got[1].Config.Scripted.Kind != "noop" {
		t.Errorf("noop = %+v", got[1])
	}
}
