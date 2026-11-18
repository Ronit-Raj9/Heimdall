package evidencestore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/utils"
)

func TestBackupSQLiteAndMirror(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	cfg := db.Config{Driver: db.SQLite, Path: filepath.Join(dir, "heimdall.db")}
	d, err := db.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().ExecContext(ctx, "CREATE TABLE t (v TEXT)"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().ExecContext(ctx, "INSERT INTO t VALUES ('kept')"); err != nil {
		t.Fatal(err)
	}
	// The source stays open and writable during the backup.
	defer d.Close()
	b, err := BackupDatabase(ctx, cfg, filepath.Join(dir, "out"), time.Date(2026, 11, 18, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "heimdall-20261118T010203Z.sqlite" || b.Size == 0 || len(b.SHA256) != 64 || b.Check != "PRAGMA integrity_check: ok" {
		t.Fatalf("backup: %+v", b)
	}
	cp, err := sql.Open("sqlite", b.Path)
	if err != nil {
		t.Fatal(err)
	}
	var v string
	if err := cp.QueryRow("SELECT v FROM t").Scan(&v); err != nil || v != "kept" {
		t.Fatalf("copy row: %q %v", v, err)
	}
	cp.Close()
	if st, _ := os.Stat(b.Path); st.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode %v, want 0600", st.Mode().Perm())
	}

	mir, mdir := newDirMirror(t)
	key, err := mir.PutBackup(ctx, b)
	if err != nil || key != backupPrefix+b.Name {
		t.Fatalf("put backup: %q %v", key, err)
	}
	list, err := mir.ListBackups(ctx)
	if err != nil || len(list) != 1 || list[0].SHA256 != b.SHA256 || list[0].Path != "" {
		t.Fatalf("list backups: %+v %v", list, err)
	}
	got, err := mir.FetchBackup(ctx, b.Name, filepath.Join(dir, "fetched"))
	if err != nil || got.SHA256 != b.SHA256 {
		t.Fatalf("fetch: %+v %v", got, err)
	}
	if _, err := mir.FetchBackup(ctx, b.Name, filepath.Join(dir, "fetched")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("fetch over an existing file: %v", err)
	}
	// A changed object in the mirror is refused and leaves no file.
	if err := os.WriteFile(filepath.Join(mdir.Root(), "_backups", "db", b.Name), []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := mir.FetchBackup(ctx, b.Name, filepath.Join(dir, "again")); !errors.Is(err, ErrChecksum) {
		t.Fatalf("tampered backup: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "again")); len(entries) != 0 {
		t.Fatalf("tampered fetch left %v", entries)
	}
	for _, bad := range []string{"", "../x", "a/b", ".hidden"} {
		if _, err := mir.FetchBackup(ctx, bad, dir); err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
}

// TestBackupPostgres runs pg_dump against HEIMDALL_TEST_PG_DSN, the same
// variable the db package's tests use.
func TestBackupPostgres(t *testing.T) {
	dsn := os.Getenv("HEIMDALL_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("HEIMDALL_TEST_PG_DSN not set")
	}
	b, err := BackupDatabase(context.Background(), db.Config{Driver: db.Postgres, DSN: dsn}, t.TempDir(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if b.Size == 0 || b.Driver != db.Postgres {
		t.Fatalf("backup: %+v", b)
	}
}

func TestListBackupsSkipsDamagedRecord(t *testing.T) {
	ctx := context.Background()
	mir, d := newDirMirror(t)
	good := []byte(`{"name":"heimdall-1.sqlite","driver":"sqlite","size":1,"sha256":"` + strings.Repeat("a", 64) + `","created_at":"2026-11-18T00:00:00Z","check":"ok"}`)
	for k, b := range map[string][]byte{backupPrefix + "heimdall-1.sqlite.json": good, backupPrefix + "broken.json": []byte("{")} {
		if err := d.Put(ctx, k, PutInput{Body: bytes.NewReader(b), Size: int64(len(b)), SHA256: utils.SHA256Bytes(b)}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := mir.ListBackups(ctx)
	if err != nil || len(list) != 1 || list[0].Name != "heimdall-1.sqlite" {
		t.Fatalf("ListBackups: %+v %v", list, err)
	}
}
