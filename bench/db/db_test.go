package db_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

func TestRebind(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT * FROM t WHERE a = ? AND b = ?", "SELECT * FROM t WHERE a = $1 AND b = $2"},
		{"SELECT '?' , ? FROM t", "SELECT '?' , $1 FROM t"},
		{"SELECT 'it''s ?', ?", "SELECT 'it''s ?', $1"},
		{`SELECT "we?ird", ?`, `SELECT "we?ird", $1`},
		{"SELECT E'a\\'?', ?", "SELECT E'a\\'?', $1"},
		{"SELECT ? -- what?\n, ?", "SELECT $1 -- what?\n, $2"},
		{"SELECT /* ? */ ?", "SELECT /* ? */ $1"},
		{"SELECT $$ ? $$, ?", "SELECT $$ ? $$, $1"},
		{"SELECT $tag$ ? $tag$, ?", "SELECT $tag$ ? $tag$, $1"},
		{"SELECT $1, ?", "SELECT $1, $1"},
		{"no placeholders", "no placeholders"},
		{"SELECT 'unterminated ?", "SELECT 'unterminated ?"},
	}
	for _, c := range cases {
		d := &pgOnly{}
		if got := d.rebind(c.in); got != c.want {
			t.Errorf("rebind(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// pgOnly reaches the Postgres rebind without a server.
type pgOnly struct{}

func (pgOnly) rebind(q string) string { return db.RebindDollar(q) }

func TestRebindSQLiteIdentity(t *testing.T) {
	d := dbtest.OpenSQLite(t)
	if q := "SELECT ? FROM t"; d.Rebind(q) != q {
		t.Fatal("sqlite rebind changed the query")
	}
}

func TestNewID(t *testing.T) {
	seen := map[string]bool{}
	var prev string
	for i := 0; i < 2000; i++ {
		id := db.NewID("org")
		if !strings.HasPrefix(id, "org_") || len(id) != len("org_")+26 {
			t.Fatalf("bad id %q", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
		prev = id
	}
	if db.NewID("usr_")[:4] != "usr_" {
		t.Fatal("trailing underscore prefix")
	}
	_ = prev
	// Time-sortable across milliseconds.
	a := db.NewID("x")
	b := waitNextMilliID("x", a)
	if !(a < b) {
		t.Fatalf("ids not time ordered: %s >= %s", a, b)
	}
}

func waitNextMilliID(prefix, after string) string {
	for {
		id := db.NewID(prefix)
		if id[:len(prefix)+11] != after[:len(prefix)+11] { // first 10 chars of the body carry the time
			return id
		}
	}
}

func TestMigrateIdempotentAndCoreTables(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		if err := d.Migrate(ctx); err != nil {
			t.Fatalf("second migrate: %v", err)
		}
		for _, table := range []string{"orgs", "users", "memberships", "audit_log", "settings",
			"idx_experiments", "idx_runs", "idx_analyses", "schema_migrations"} {
			ok, err := d.TableExists(ctx, table)
			if err != nil || !ok {
				t.Errorf("table %s: exists=%v err=%v", table, ok, err)
			}
		}
		st, err := d.MigrationStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(st) < 2 || st[0].Set != db.CoreSet {
			t.Fatalf("status %+v", st)
		}
		for _, s := range st {
			if !s.Applied || s.Problem != "" {
				t.Errorf("migration %+v not cleanly applied", s)
			}
		}
	})
}

func TestTxRollback(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		boom := errors.New("boom")
		err := d.Tx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, d.Rebind("INSERT INTO settings (org_id, key, value, updated_at) VALUES (?, ?, ?, ?)"), "", "k", "v", 1); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
		var n int
		if err := d.QueryRow(ctx, "SELECT count(*) FROM settings").Scan(&n); err != nil || n != 0 {
			t.Fatalf("rolled back row visible: n=%d err=%v", n, err)
		}
		func() {
			defer func() { _ = recover() }()
			_ = d.Tx(ctx, func(tx *sql.Tx) error {
				_, _ = tx.ExecContext(ctx, d.Rebind("INSERT INTO settings (org_id, key, value, updated_at) VALUES (?, ?, ?, ?)"), "", "k", "v", 1)
				panic("x")
			})
		}()
		if err := d.QueryRow(ctx, "SELECT count(*) FROM settings").Scan(&n); err != nil || n != 0 {
			t.Fatalf("panicked tx committed: n=%d err=%v", n, err)
		}
		if _, err := d.Exec(ctx, "INSERT INTO settings (org_id, key, value, updated_at) VALUES (?, ?, ?, ?)", "", "k", "v", 1); err != nil {
			t.Fatal(err)
		}
		_, err = d.Exec(ctx, "INSERT INTO settings (org_id, key, value, updated_at) VALUES (?, ?, ?, ?)", "", "k", "v", 1)
		if !db.IsUnique(err) {
			t.Fatalf("IsUnique(%v) = false", err)
		}
	})
}

const queueDDL = `CREATE TABLE work (
	id TEXT PRIMARY KEY,
	status TEXT NOT NULL,
	owner TEXT NOT NULL DEFAULT '',
	created_at BIGINT NOT NULL,
	claims INTEGER NOT NULL DEFAULT 0
)`

func TestClaimOneConcurrent(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		if _, err := d.Exec(ctx, queueDDL); err != nil {
			t.Fatal(err)
		}
		const rows, workers = 60, 12
		for i := 0; i < rows; i++ {
			if _, err := d.Exec(ctx, "INSERT INTO work (id, status, created_at) VALUES (?, ?, ?)", fmt.Sprintf("w%03d", i), "queued", i); err != nil {
				t.Fatal(err)
			}
		}
		var (
			mu      sync.Mutex
			claimed = map[string]string{}
			wg      sync.WaitGroup
			errs    = make(chan error, workers)
		)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(owner string) {
				defer wg.Done()
				for {
					var id string
					var claims int
					ok, err := d.ClaimOneScan(ctx, db.ClaimQuery{
						Table:     "work",
						Where:     "status = ?",
						Args:      []any{"queued"},
						OrderBy:   "created_at, id",
						Set:       "status = ?, owner = ?, claims = claims + 1",
						SetArgs:   []any{"running", owner},
						Returning: "id, claims",
					}, &id, &claims)
					if err != nil {
						errs <- err
						return
					}
					if !ok {
						return
					}
					mu.Lock()
					if prev, dup := claimed[id]; dup {
						errs <- fmt.Errorf("row %s claimed by %s and %s", id, prev, owner)
					}
					claimed[id] = owner
					mu.Unlock()
				}
			}(fmt.Sprintf("worker%d", w))
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Error(err)
		}
		if len(claimed) != rows {
			t.Fatalf("claimed %d rows, want %d", len(claimed), rows)
		}
		var bad int
		if err := d.QueryRow(ctx, "SELECT count(*) FROM work WHERE claims <> 1 OR status <> ?", "running").Scan(&bad); err != nil || bad != 0 {
			t.Fatalf("rows claimed other than once: %d (err %v)", bad, err)
		}
		// Nothing left: ErrNoRows through the raw row.
		row, err := d.ClaimOne(ctx, db.ClaimQuery{Table: "work", Where: "status = ?", Args: []any{"queued"},
			Set: "status = ?", SetArgs: []any{"running"}, Returning: "id"})
		if err != nil {
			t.Fatal(err)
		}
		var id string
		if err := row.Scan(&id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("empty claim: %v", err)
		}
		// Inside a caller's transaction.
		if _, err := d.Exec(ctx, "INSERT INTO work (id, status, created_at) VALUES (?, ?, ?)", "late", "queued", 1000); err != nil {
			t.Fatal(err)
		}
		err = d.Tx(ctx, func(tx *sql.Tx) error {
			row, err := d.ClaimOneTx(ctx, tx, db.ClaimQuery{Table: "work", Where: "status = ?", Args: []any{"queued"},
				Set: "status = ?", SetArgs: []any{"running"}, Returning: "id"})
			if err != nil {
				return err
			}
			return row.Scan(&id)
		})
		if err != nil || id != "late" {
			t.Fatalf("claim in tx: id=%q err=%v", id, err)
		}
		if _, err := d.ClaimOne(ctx, db.ClaimQuery{Table: "work"}); err == nil {
			t.Fatal("malformed claim accepted")
		}
	})
}

func TestMigrationChecksumAndOrder(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		set := "ztest_" + db.NewID("") // unique per run: the registry is process-wide
		v1 := fstest.MapFS{
			"migrations/0002_second.sql": {Data: []byte("INSERT INTO zt (v) VALUES ('second');")},
			"migrations/0001_first.sql":  {Data: []byte("CREATE TABLE zt (v TEXT NOT NULL); INSERT INTO zt (v) VALUES ('first');")},
			"migrations/README.md":       {Data: []byte("ignored")},
		}
		fsys := &swapFS{fs: v1}
		db.Register(set, fsys)
		if err := d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		rows, err := d.Query(ctx, "SELECT v FROM zt")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for rows.Next() {
			var v string
			_ = rows.Scan(&v)
			got = append(got, v)
		}
		rows.Close()
		if strings.Join(got, ",") != "first,second" {
			t.Fatalf("applied order: %v", got)
		}
		// A new migration applies; applied ones do not run again.
		fsys.fs = fstest.MapFS{
			"migrations/0001_first.sql":  v1["migrations/0001_first.sql"],
			"migrations/0002_second.sql": v1["migrations/0002_second.sql"],
			"migrations/0003_third.sql":  {Data: []byte("INSERT INTO zt (v) VALUES ('third');")},
		}
		if err := d.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := d.QueryRow(ctx, "SELECT count(*) FROM zt").Scan(&n); err != nil || n != 3 {
			t.Fatalf("rows after third: %d %v", n, err)
		}
		// Editing an applied migration is refused, and nothing new runs.
		fsys.fs = fstest.MapFS{
			"migrations/0001_first.sql":  {Data: []byte("CREATE TABLE zt (v TEXT); -- edited")},
			"migrations/0002_second.sql": v1["migrations/0002_second.sql"],
			"migrations/0003_third.sql":  {Data: []byte("INSERT INTO zt (v) VALUES ('third');")},
			"migrations/0004_fourth.sql": {Data: []byte("INSERT INTO zt (v) VALUES ('fourth');")},
		}
		if err := d.Migrate(ctx); !errors.Is(err, db.ErrChecksum) {
			t.Fatalf("edited migration: err = %v", err)
		}
		st, err := d.MigrationStatus(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var edited bool
		for _, s := range st {
			if s.Set == set && s.Version == 1 && s.Problem == "edited" {
				edited = true
			}
		}
		if !edited {
			t.Fatalf("status does not flag the edit: %+v", st)
		}
		if err := d.QueryRow(ctx, "SELECT count(*) FROM zt").Scan(&n); err != nil || n != 3 {
			t.Fatalf("fourth ran despite the checksum error: %d %v", n, err)
		}
		// A failing migration rolls back with its row.
		fsys.fs = fstest.MapFS{
			"migrations/0001_first.sql":  v1["migrations/0001_first.sql"],
			"migrations/0002_second.sql": v1["migrations/0002_second.sql"],
			"migrations/0003_third.sql":  {Data: []byte("INSERT INTO zt (v) VALUES ('third');")},
			"migrations/0004_bad.sql":    {Data: []byte("INSERT INTO zt (v) VALUES ('x'); INSERT INTO nope VALUES (1);")},
		}
		if err := d.Migrate(ctx); err == nil {
			t.Fatal("bad migration applied")
		}
		if err := d.QueryRow(ctx, "SELECT count(*) FROM zt").Scan(&n); err != nil || n != 3 {
			t.Fatalf("failed migration left rows: %d %v", n, err)
		}
		fsys.fs = fstest.MapFS{} // later tests share the registry: leave the set empty
	})
}

func TestMigrateConcurrent(t *testing.T) {
	// Several migrators over one fresh database: all succeed, each migration once.
	t.Run("sqlite", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "c.db")
		runConcurrentMigrate(t, func() (*db.DB, error) {
			return db.Open(context.Background(), db.Config{Driver: db.SQLite, Path: path})
		})
	})
	if dbtest.PostgresDSN() != "" {
		t.Run("postgres", func(t *testing.T) {
			base := dbtest.OpenPostgresUnmigrated(t) // fresh schema; reuse its DSN via search_path
			schema := currentSchema(t, base)
			runConcurrentMigrate(t, func() (*db.DB, error) {
				return db.Open(context.Background(), db.Config{Driver: db.Postgres, DSN: dbtest.WithSearchPath(dbtest.PostgresDSN(), schema)})
			})
		})
	}
}

func currentSchema(t *testing.T, d *db.DB) string {
	var s string
	if err := d.QueryRow(context.Background(), "SELECT current_schema()").Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func runConcurrentMigrate(t *testing.T, open func() (*db.DB, error)) {
	const n = 5
	var wg sync.WaitGroup
	errs := make([]error, n)
	dbs := make([]*db.DB, n)
	for i := range dbs {
		d, err := open()
		if err != nil {
			t.Fatal(err)
		}
		dbs[i] = d
		defer d.Close()
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = dbs[i].Migrate(context.Background())
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("migrator %d: %v", i, err)
		}
	}
	var dup int
	if err := dbs[0].QueryRow(context.Background(), `SELECT count(*) FROM (SELECT "set", version FROM schema_migrations GROUP BY "set", version HAVING count(*) > 1) x`).Scan(&dup); err != nil || dup != 0 {
		t.Fatalf("duplicate migration rows: %d %v", dup, err)
	}
}

func TestSetsOrder(t *testing.T) {
	sets := db.Sets()
	if len(sets) == 0 || sets[0] != db.CoreSet {
		t.Fatalf("core not first: %v", sets)
	}
	rest := sets[1:]
	if !sort.StringsAreSorted(rest) {
		t.Fatalf("sets not in name order: %v", rest)
	}
}

// Several pools on one SQLite file (as several processes would be): each row
// claimed exactly once, no SQLITE_BUSY surfaced.
func TestClaimOneAcrossHandlesSQLite(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "q.db")
	var handles []*db.DB
	for i := 0; i < 4; i++ {
		d, err := db.Open(ctx, db.Config{Driver: db.SQLite, Path: path, MaxOpenConns: 3})
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
		handles = append(handles, d)
	}
	if _, err := handles[0].Exec(ctx, queueDDL); err != nil {
		t.Fatal(err)
	}
	const rows = 200
	for i := 0; i < rows; i++ {
		if _, err := handles[0].Exec(ctx, "INSERT INTO work (id, status, created_at) VALUES (?, ?, ?)", fmt.Sprintf("w%03d", i), "queued", i); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]bool{}
	errs := make(chan error, 64)
	for h, d := range handles {
		for g := 0; g < 4; g++ {
			wg.Add(1)
			go func(d *db.DB, owner string) {
				defer wg.Done()
				for {
					var id string
					ok, err := d.ClaimOneScan(ctx, db.ClaimQuery{Table: "work", Where: "status = ?", Args: []any{"queued"},
						OrderBy: "created_at", Set: "status = ?, owner = ?, claims = claims + 1", SetArgs: []any{"running", owner}, Returning: "id"}, &id)
					if err != nil {
						errs <- err
						return
					}
					if !ok {
						return
					}
					mu.Lock()
					if seen[id] {
						errs <- fmt.Errorf("%s claimed twice", id)
					}
					seen[id] = true
					mu.Unlock()
				}
			}(d, fmt.Sprintf("h%d-g%d", h, g))
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(seen) != rows {
		t.Fatalf("claimed %d of %d", len(seen), rows)
	}
}
