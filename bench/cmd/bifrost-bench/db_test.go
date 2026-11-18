package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
)

// Database area (P1): db migrate, status and backup on SQLite.
func TestDBCommand(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "deploy", "bench.yaml")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("runs_root: runs\nbenchmarks_root: benchmarks\nserver:\n  listen: 127.0.0.1:1\n  mode: live\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"drop"}, {"backup", "-config", cfgPath}, {"status", "extra"}} {
		if got := runDB(args); got != exitUsage {
			t.Fatalf("runDB(%v) = %d, want usage", args, got)
		}
	}
	if got := runDB([]string{"migrate", "-config", cfgPath}); got != exitOK {
		t.Fatalf("migrate exit %d", got)
	}
	dbPath := filepath.Join(root, "runs", ".heimdall", "heimdall.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Fatalf("database not at the default path: %v", err)
	}
	if got := runDB([]string{"status", "-config", cfgPath, "-json"}); got != exitOK {
		t.Fatalf("status exit %d", got)
	}
	out := filepath.Join(root, "backup", "copy.db")
	if got := runDB([]string{"backup", "-config", cfgPath, "-out", out}); got != exitOK {
		t.Fatalf("backup exit %d", got)
	}
	if got := runDB([]string{"backup", "-config", cfgPath, "-out", out}); got != exitInfra {
		t.Fatalf("backup over an existing file exit %d", got)
	}
	ctx := context.Background()
	copyDB, err := db.Open(ctx, db.Config{Driver: db.SQLite, Path: out})
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	if ok, err := copyDB.TableExists(ctx, "orgs"); err != nil || !ok {
		t.Fatalf("backup has no orgs table: %v %v", ok, err)
	}
	if info, err := os.Stat(out); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v %v", info.Mode(), err)
	}
}
