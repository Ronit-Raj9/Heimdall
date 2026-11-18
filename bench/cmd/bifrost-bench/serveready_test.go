package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/handlers"
)

func TestMigrationsCurrent(t *testing.T) {
	ok := []db.MigrationState{{Set: "core", Version: 1, Name: "init", Applied: true}}
	if err := migrationsCurrent(ok); err != nil {
		t.Fatalf("applied: %v", err)
	}
	newer := append(ok, db.MigrationState{Set: "core", Version: 9, Name: "later", Applied: true, Problem: "unknown"})
	if err := migrationsCurrent(newer); err != nil {
		t.Fatalf("a migration from a newer binary must not fail readiness: %v", err)
	}
	pending := append(ok, db.MigrationState{Set: "jobs", Version: 2, Name: "leases"})
	if err := migrationsCurrent(pending); err == nil || !strings.Contains(err.Error(), "jobs/0002_leases") {
		t.Fatalf("pending: %v", err)
	}
	edited := append(ok, db.MigrationState{Set: "auth", Version: 1, Name: "sessions", Applied: true, Problem: "edited"})
	if err := migrationsCurrent(edited); err == nil || !strings.Contains(err.Error(), "edited") {
		t.Fatalf("edited: %v", err)
	}
}

// TestDBReadyChecks runs the database checks on a SQLite file: not ready
// before migrations, ready after.
func TestDBReadyChecks(t *testing.T) {
	ctx := context.Background()
	cfg := db.Config{Driver: "sqlite", Path: filepath.Join(t.TempDir(), "ready.db")}
	checks := dbReadyChecks(&readyDB{cfg: cfg})
	res := handlers.RunReadyChecks(ctx, checks)
	if !res[0].OK || res[1].OK {
		t.Fatalf("before migrate: %+v", res)
	}
	d, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	res = handlers.RunReadyChecks(ctx, checks)
	if !res[0].OK || !res[1].OK {
		t.Fatalf("after migrate: %+v", res)
	}
}
