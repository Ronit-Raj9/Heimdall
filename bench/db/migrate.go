package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// CoreSet is applied before every other set.
const CoreSet = "core"

// migrateLockKey is the Postgres advisory lock every migrator takes.
const migrateLockKey int64 = 0x4865696d64616c6c // "Heimdall"

var (
	registryMu sync.Mutex
	registry   = map[string]fs.FS{}

	setName       = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	migrationName = regexp.MustCompile(`^([0-9]+)_([A-Za-z0-9_\-]+)\.sql$`)

	// ErrChecksum means an applied migration's file changed after it ran.
	ErrChecksum = errors.New("db: applied migration was edited")
)

// Register adds a set of migrations. Call it from init() of the package that
// owns the tables:
//
//	//go:embed migrations/*.sql
//	var migrations embed.FS
//	func init() { db.Register("jobs", migrations) }
//
// Every file named NNNN_name.sql anywhere in the FS is a migration; NNNN is
// its version within the set (unique, applied in ascending order). Other
// files are ignored. Register panics on a bad or duplicate set name.
func Register(set string, migrations fs.FS) {
	if !setName.MatchString(set) {
		panic(fmt.Sprintf("db.Register: bad set name %q", set))
	}
	if migrations == nil {
		panic("db.Register: nil fs for set " + set)
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if _, ok := registry[set]; ok {
		panic("db.Register: set registered twice: " + set)
	}
	registry[set] = migrations
}

// Sets lists the registered sets in apply order: core, then by name.
func Sets() []string {
	registryMu.Lock()
	defer registryMu.Unlock()
	sets := make([]string, 0, len(registry))
	for s := range registry {
		sets = append(sets, s)
	}
	sort.Slice(sets, func(i, j int) bool {
		if (sets[i] == CoreSet) != (sets[j] == CoreSet) {
			return sets[i] == CoreSet
		}
		return sets[i] < sets[j]
	})
	return sets
}

// Migration is one file of a set.
type Migration struct {
	Set      string
	Version  int
	Name     string
	SQL      string
	Checksum string // sha256 hex of the file bytes
}

// loadSet reads and orders the migrations of one set.
func loadSet(set string, fsys fs.FS) ([]Migration, error) {
	var out []Migration
	seen := map[int]string{}
	err := fs.WalkDir(fsys, ".", func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() || !strings.HasSuffix(p, ".sql") {
			return nil
		}
		m := migrationName.FindStringSubmatch(path.Base(p))
		if m == nil {
			return fmt.Errorf("migration %s/%s: name must be NNNN_name.sql", set, p)
		}
		v, err := strconv.Atoi(m[1])
		if err != nil || v <= 0 {
			return fmt.Errorf("migration %s/%s: version must be a positive number", set, p)
		}
		if prev, ok := seen[v]; ok {
			return fmt.Errorf("migration %s: version %d used by %s and %s", set, v, prev, p)
		}
		seen[v] = p
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out = append(out, Migration{Set: set, Version: v, Name: m[2], SQL: string(data), Checksum: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(out, func(a, b Migration) int { return a.Version - b.Version })
	return out, nil
}

// registered loads every registered set in apply order.
func registered() ([]Migration, error) {
	var all []Migration
	for _, set := range Sets() {
		registryMu.Lock()
		fsys := registry[set]
		registryMu.Unlock()
		ms, err := loadSet(set, fsys)
		if err != nil {
			return nil, err
		}
		all = append(all, ms...)
	}
	return all, nil
}

const schemaMigrationsDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
	"set" TEXT NOT NULL,
	version BIGINT NOT NULL,
	name TEXT NOT NULL,
	checksum TEXT NOT NULL,
	applied_at BIGINT NOT NULL,
	PRIMARY KEY ("set", version)
)`

// lockTx serializes migrators: a transaction-scoped advisory lock on
// Postgres; on SQLite the IMMEDIATE transaction already excludes other
// writers.
func (d *DB) lockTx(ctx context.Context, tx *sql.Tx) error {
	if d.driver != Postgres {
		return nil
	}
	_, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", migrateLockKey)
	return err
}

// Migrate applies every registered set: core first, then the others by name,
// each migration in its own transaction together with its schema_migrations
// row. An applied migration whose file changed is an error (ErrChecksum);
// nothing new is applied then. Concurrent migrators (several processes) are
// safe: each migration re-checks under the lock before it runs.
func (d *DB) Migrate(ctx context.Context) error {
	all, err := registered()
	if err != nil {
		return err
	}
	if err := d.Tx(ctx, func(tx *sql.Tx) error {
		if err := d.lockTx(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, schemaMigrationsDDL)
		return err
	}); err != nil {
		return fmt.Errorf("db: create schema_migrations: %w", err)
	}
	applied, err := d.appliedMigrations(ctx)
	if err != nil {
		return err
	}
	for _, m := range all {
		if a, ok := applied[migKey{m.Set, m.Version}]; ok && a.Checksum != m.Checksum {
			return fmt.Errorf("%w: %s/%04d_%s (applied %s, file %s)", ErrChecksum, m.Set, m.Version, m.Name, short(a.Checksum), short(m.Checksum))
		}
	}
	for _, m := range all {
		if _, ok := applied[migKey{m.Set, m.Version}]; ok {
			continue
		}
		if err := d.Tx(ctx, func(tx *sql.Tx) error {
			if err := d.lockTx(ctx, tx); err != nil {
				return err
			}
			var sum string
			err := tx.QueryRowContext(ctx, d.Rebind(`SELECT checksum FROM schema_migrations WHERE "set" = ? AND version = ?`), m.Set, m.Version).Scan(&sum)
			switch {
			case err == nil:
				if sum != m.Checksum {
					return fmt.Errorf("%w: %s/%04d_%s", ErrChecksum, m.Set, m.Version, m.Name)
				}
				return nil // another migrator applied it meanwhile
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
			if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, d.Rebind(`INSERT INTO schema_migrations ("set", version, name, checksum, applied_at) VALUES (?, ?, ?, ?, ?)`),
				m.Set, m.Version, m.Name, m.Checksum, Now())
			return err
		}); err != nil {
			return fmt.Errorf("db: migration %s/%04d_%s: %w", m.Set, m.Version, m.Name, err)
		}
	}
	return nil
}

type migKey struct {
	set     string
	version int
}

type appliedRow struct {
	Name      string
	Checksum  string
	AppliedAt int64
}

func (d *DB) appliedMigrations(ctx context.Context) (map[migKey]appliedRow, error) {
	rows, err := d.sql.QueryContext(ctx, `SELECT "set", version, name, checksum, applied_at FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("db: read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := map[migKey]appliedRow{}
	for rows.Next() {
		var k migKey
		var a appliedRow
		if err := rows.Scan(&k.set, &k.version, &a.Name, &a.Checksum, &a.AppliedAt); err != nil {
			return nil, err
		}
		out[k] = a
	}
	return out, rows.Err()
}

// MigrationState is one line of `db status`.
type MigrationState struct {
	Set       string `json:"set"`
	Version   int    `json:"version"`
	Name      string `json:"name"`
	Applied   bool   `json:"applied"`
	AppliedAt int64  `json:"applied_at,omitempty"` // unix ms
	// Problem is "edited" (checksum differs), "unknown" (applied but no
	// longer registered in this binary) or "".
	Problem string `json:"problem,omitempty"`
}

// MigrationStatus lists registered and applied migrations without changing
// anything. A database never migrated lists every migration as pending.
func (d *DB) MigrationStatus(ctx context.Context) ([]MigrationState, error) {
	all, err := registered()
	if err != nil {
		return nil, err
	}
	applied := map[migKey]appliedRow{}
	if ok, err := d.TableExists(ctx, "schema_migrations"); err != nil {
		return nil, err
	} else if ok {
		if applied, err = d.appliedMigrations(ctx); err != nil {
			return nil, err
		}
	}
	var out []MigrationState
	known := map[migKey]bool{}
	for _, m := range all {
		k := migKey{m.Set, m.Version}
		known[k] = true
		st := MigrationState{Set: m.Set, Version: m.Version, Name: m.Name}
		if a, ok := applied[k]; ok {
			st.Applied, st.AppliedAt = true, a.AppliedAt
			if a.Checksum != m.Checksum {
				st.Problem = "edited"
			}
		}
		out = append(out, st)
	}
	for k, a := range applied {
		if !known[k] {
			out = append(out, MigrationState{Set: k.set, Version: k.version, Name: a.Name, Applied: true, AppliedAt: a.AppliedAt, Problem: "unknown"})
		}
	}
	return out, nil
}

// TableExists reports whether a table exists in the current schema.
func (d *DB) TableExists(ctx context.Context, table string) (bool, error) {
	var q string
	if d.driver == Postgres {
		q = `SELECT count(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = $1`
	} else {
		q = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`
	}
	var n int
	if err := d.sql.QueryRowContext(ctx, q, table).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

func short(sum string) string {
	if len(sum) > 12 {
		return sum[:12]
	}
	return sum
}
