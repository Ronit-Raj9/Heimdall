// Package db is Heimdall's operational database: users, organizations,
// memberships, audit, settings, the results index, and the tables each
// platform package registers (sessions, tokens, jobs, runners ...).
//
// Two drivers sit behind database/sql: SQLite (modernc.org/sqlite, pure Go)
// and Postgres (github.com/jackc/pgx/v5/stdlib). SQL is written once in a
// portable subset (docs/platform-design.md, "Database"):
//
//   - ids TEXT (NewID), times BIGINT unix milliseconds UTC (Millis, Time),
//     booleans INTEGER 0/1, JSON TEXT, floats DOUBLE PRECISION
//   - placeholders `?`; Rebind turns them into $1..$n on Postgres
//   - upsert INSERT ... ON CONFLICT (...) DO UPDATE; RETURNING allowed
//   - no `FOR UPDATE` in shared SQL; use ForUpdate() which is empty on SQLite
//
// The database never holds evidence: verdicts, leaderboards and releases read
// sealed run folders. The idx_* tables are a rebuildable index of them.
//
// SQLite runs in WAL mode with busy_timeout, foreign_keys ON and every
// transaction opened with BEGIN IMMEDIATE, so writers queue on the database
// lock instead of failing on a lock upgrade (single-writer discipline).
// Inside Tx use only the *sql.Tx you are given: a second write through SQL()
// from inside fn waits on the lock fn holds and fails with SQLITE_BUSY.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx"
	"github.com/maximhq/bifrost/bench/db/dbconfig"
	sqlite "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Config is the `database:` block of deploy/bench.yaml (lib.Config.Database).
type Config = dbconfig.Config

// Driver names.
const (
	SQLite   = dbconfig.SQLite
	Postgres = dbconfig.Postgres
)

// Errors shared by every package that writes SQL against DB.
var (
	ErrNotFound = errors.New("db: not found")
	ErrConflict = errors.New("db: already exists")
)

// DB is an open database with its dialect.
type DB struct {
	sql    *sql.DB
	driver string
	path   string     // sqlite file
	dsn    string     // postgres, for Backup
	ixMu   sync.Mutex // one index writer per process (index.go)
}

// Open connects and pings. It does not migrate; call Migrate once at startup.
// A SQLite path is created (with its parent folder, mode 0700) when missing;
// a relative path is used as given, so resolve it first (Config.Resolve).
func Open(ctx context.Context, cfg Config) (*DB, error) {
	if cfg.Driver == "" {
		cfg.Driver = SQLite
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	var (
		sdb *sql.DB
		err error
		d   = &DB{driver: cfg.Driver}
	)
	switch cfg.Driver {
	case SQLite:
		if cfg.Path == "" {
			return nil, fmt.Errorf("db: sqlite path is empty")
		}
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o700); err != nil {
			return nil, fmt.Errorf("db: create folder for %s: %w", cfg.Path, err)
		}
		d.path = cfg.Path
		sdb, err = sql.Open("sqlite", sqliteDSN(cfg.Path))
		if err != nil {
			return nil, fmt.Errorf("db: open sqlite %s: %w", cfg.Path, err)
		}
		n := cfg.MaxOpenConns
		if n == 0 {
			n = 8
		}
		sdb.SetMaxOpenConns(n)
		sdb.SetMaxIdleConns(n)
	case Postgres:
		dsn, derr := cfg.PostgresDSN()
		if derr != nil {
			return nil, derr
		}
		d.dsn = dsn
		sdb, err = sql.Open("pgx", dsn)
		if err != nil {
			return nil, fmt.Errorf("db: open postgres: %w", err)
		}
		n := cfg.MaxOpenConns
		if n == 0 {
			n = 20
		}
		sdb.SetMaxOpenConns(n)
		sdb.SetMaxIdleConns(min(n, 10))
		sdb.SetConnMaxIdleTime(5 * time.Minute)
	}
	if err := sdb.PingContext(ctx); err != nil {
		sdb.Close()
		return nil, fmt.Errorf("db: connect %s: %w", cfg.Driver, err)
	}
	d.sql = sdb
	return d, nil
}

// sqliteDSN builds the modernc DSN: WAL, busy timeout, foreign keys,
// BEGIN IMMEDIATE for every transaction.
func sqliteDSN(path string) string {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(15000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "synchronous(NORMAL)")
	q.Set("_txlock", "immediate")
	return path + "?" + q.Encode()
}

// Close closes the pool.
func (d *DB) Close() error { return d.sql.Close() }

// Ping checks the connection (readiness).
func (d *DB) Ping(ctx context.Context) error { return d.sql.PingContext(ctx) }

// Driver is "sqlite" or "postgres".
func (d *DB) Driver() string { return d.driver }

// SQL is the underlying pool.
func (d *DB) SQL() *sql.DB { return d.sql }

// Path is the SQLite file, "" on Postgres.
func (d *DB) Path() string { return d.path }

// Rebind turns `?` placeholders into $1..$n on Postgres; SQLite queries are
// returned unchanged. Quoted strings, identifiers and comments are skipped.
func (d *DB) Rebind(query string) string {
	if d.driver == Postgres {
		return rebindDollar(query)
	}
	return query
}

// ForUpdate is " FOR UPDATE" on Postgres and "" on SQLite (whose
// transactions already hold the write lock). Append it to a SELECT inside Tx
// that must lock the rows it reads.
func (d *DB) ForUpdate() string {
	if d.driver == Postgres {
		return " FOR UPDATE"
	}
	return ""
}

// Exec, Query and QueryRow rebind and run on the pool.
func (d *DB) Exec(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.sql.ExecContext(ctx, d.Rebind(query), args...)
}

// Query rebinds and runs a query on the pool.
func (d *DB) Query(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.sql.QueryContext(ctx, d.Rebind(query), args...)
}

// QueryRow rebinds and runs a single-row query on the pool.
func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *sql.Row {
	return d.sql.QueryRowContext(ctx, d.Rebind(query), args...)
}

// Tx runs fn in one transaction: committed when fn returns nil, rolled back
// on an error or a panic (re-raised). On SQLite the transaction begins
// IMMEDIATE (holds the write lock from the start); on Postgres it is READ
// COMMITTED, so lock what you read with ForUpdate() when you decide on it.
// Do not nest Tx calls and do not use SQL() inside fn.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) (err error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	done := false
	defer func() {
		if !done {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		done = true
		return err
	}
	done = true
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// IsUnique reports whether err is a unique or primary key violation.
func IsUnique(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "23505"
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		c := se.Code()
		return c == sqlite3.SQLITE_CONSTRAINT_UNIQUE || c == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
	}
	return false
}

// IsForeignKey reports whether err is a foreign key violation.
func IsForeignKey(err error) bool {
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		return pg.Code == "23503"
	}
	var se *sqlite.Error
	if errors.As(err, &se) {
		return se.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
	}
	return false
}

// Now is the current time in unix milliseconds UTC.
func Now() int64 { return time.Now().UnixMilli() }

// Millis converts t to unix milliseconds; the zero time is 0.
func Millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// Time converts unix milliseconds to a UTC time; 0 is the zero time.
func Time(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}

// NullMillis is Millis as a nullable column value: the zero time is NULL.
func NullMillis(t time.Time) sql.NullInt64 {
	if t.IsZero() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMilli(), Valid: true}
}

// Bool is a boolean as the portable INTEGER 0/1.
func Bool(b bool) int {
	if b {
		return 1
	}
	return 0
}
