// Package dbtest opens migrated throwaway databases for tests.
//
// SQLite always; Postgres when HEIMDALL_TEST_PG_DSN holds a DSN of a database
// the test may create schemas in. Each Postgres database is a fresh schema
// (dropped at cleanup), so tests run in parallel against one server.
//
//	d := dbtest.Open(t)                 // SQLite (Postgres with HEIMDALL_TEST_DB=postgres)
//	dbtest.Each(t, func(t *testing.T, d *db.DB) { ... }) // SQLite, then Postgres when configured
//
// Migrations registered by every package linked into the test binary are
// applied, so import the packages whose tables the test needs.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db"
)

// Environment variables.
const (
	EnvPostgresDSN = "HEIMDALL_TEST_PG_DSN" // Postgres for tests; unset skips Postgres
	EnvDriver      = "HEIMDALL_TEST_DB"     // "postgres" makes Open use Postgres
)

// Open returns a migrated database closed at cleanup: SQLite in t.TempDir(),
// or Postgres when HEIMDALL_TEST_DB=postgres and HEIMDALL_TEST_PG_DSN is set.
func Open(t *testing.T) *db.DB {
	t.Helper()
	if os.Getenv(EnvDriver) == db.Postgres {
		return OpenPostgres(t)
	}
	return OpenSQLite(t)
}

// OpenSQLite returns a migrated SQLite database in t.TempDir().
func OpenSQLite(t *testing.T) *db.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	d, err := db.Open(ctx, db.Config{Driver: db.SQLite, Path: filepath.Join(t.TempDir(), "heimdall.db")})
	if err != nil {
		t.Fatalf("dbtest: open sqlite: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("dbtest: migrate sqlite: %v", err)
	}
	return d
}

// PostgresDSN is HEIMDALL_TEST_PG_DSN, "" when unset.
func PostgresDSN() string { return strings.TrimSpace(os.Getenv(EnvPostgresDSN)) }

// OpenPostgres returns a migrated database in a fresh Postgres schema, dropped
// at cleanup. The test is skipped when HEIMDALL_TEST_PG_DSN is unset.
func OpenPostgres(t *testing.T) *db.DB {
	t.Helper()
	d := OpenPostgresUnmigrated(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("dbtest: migrate postgres: %v", err)
	}
	return d
}

// OpenPostgresUnmigrated is OpenPostgres without Migrate.
func OpenPostgresUnmigrated(t *testing.T) *db.DB {
	t.Helper()
	base := PostgresDSN()
	if base == "" {
		t.Skip("dbtest: " + EnvPostgresDSN + " not set; skipping Postgres")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := db.Open(ctx, db.Config{Driver: db.Postgres, DSN: base, MaxOpenConns: 2})
	if err != nil {
		t.Fatalf("dbtest: connect postgres: %v", err)
	}
	schema := "t_" + randHex(8)
	if _, err := admin.SQL().ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("dbtest: create schema: %v", err)
	}
	d, err := db.Open(ctx, db.Config{Driver: db.Postgres, DSN: WithSearchPath(base, schema), MaxOpenConns: 10})
	if err != nil {
		admin.Close()
		t.Fatalf("dbtest: open postgres schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		d.Close()
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		_, _ = admin.SQL().ExecContext(cctx, "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return d
}

// Each runs fn as subtest "sqlite" and, when HEIMDALL_TEST_PG_DSN is set,
// as subtest "postgres", each on a fresh migrated database.
func Each(t *testing.T, fn func(t *testing.T, d *db.DB)) {
	t.Helper()
	t.Run(db.SQLite, func(t *testing.T) { fn(t, OpenSQLite(t)) })
	if PostgresDSN() != "" {
		t.Run(db.Postgres, func(t *testing.T) { fn(t, OpenPostgres(t)) })
	}
}

// WithSearchPath returns dsn (URL or key=value form) with search_path set.
func WithSearchPath(dsn, schema string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err == nil {
			q := u.Query()
			q.Set("search_path", schema)
			u.RawQuery = q.Encode()
			return u.String()
		}
	}
	return dsn + " search_path=" + schema
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
