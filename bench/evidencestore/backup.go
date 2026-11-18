package evidencestore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/utils"
)

// backupPrefix holds database backups in the mirror. Organization ids never
// start with an underscore, so it can not meet an experiment key.
const backupPrefix = "_backups/db/"

// DBBackup is one database backup file.
type DBBackup struct {
	Name      string    `json:"name"`   // file name, also the object name under _backups/db/
	Driver    string    `json:"driver"` // sqlite | postgres
	Path      string    `json:"path,omitempty"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	CreatedAt time.Time `json:"created_at"`
	Check     string    `json:"check"` // how the file was checked after it was written
}

// BackupDatabase writes a consistent copy of the operational database into
// outDir while the server keeps running, with the database package's own
// backup (db.DB.Backup: SQLite VACUUM INTO, one read transaction's view;
// Postgres pg_dump --format=custom with the password in the environment),
// then checks the file it wrote:
//
//   - SQLite: the copy is opened read-only and must pass PRAGMA
//     integrity_check;
//   - Postgres: pg_restore --list must read the dump (when pg_restore is
//     installed).
//
// The database holds operational state and the rebuildable index, never
// evidence; this backup is what brings back users, organizations, tokens,
// jobs and runners after a disk loss. It holds password and token hashes:
// keep it private.
func BackupDatabase(ctx context.Context, cfg db.Config, outDir string, now time.Time) (*DBBackup, error) {
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, fmt.Errorf("backup database: %w", err)
	}
	if cfg.Driver == "" {
		cfg.Driver = db.SQLite
	}
	stamp := now.UTC().Format("20060102T150405Z")
	b := &DBBackup{Driver: cfg.Driver, CreatedAt: now.UTC(), Name: "heimdall-" + stamp + ".sqlite"}
	if cfg.Driver == db.Postgres {
		b.Name = "heimdall-" + stamp + ".pgdump"
	}
	b.Path = filepath.Join(outDir, b.Name)
	d, err := db.Open(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("backup database: %w", err)
	}
	err = d.Backup(ctx, b.Path)
	_ = d.Close()
	if err != nil {
		return nil, fmt.Errorf("backup database: %w", err)
	}
	switch cfg.Driver {
	case db.SQLite:
		b.Check, err = checkSQLiteCopy(ctx, b.Path)
	case db.Postgres:
		b.Check, err = checkPgDump(ctx, b.Path)
	}
	if err != nil {
		_ = os.Remove(b.Path)
		return nil, fmt.Errorf("backup database: %w", err)
	}
	st, err := os.Stat(b.Path)
	if err != nil {
		return nil, err
	}
	b.Size = st.Size()
	if b.SHA256, err = utils.SHA256File(b.Path); err != nil {
		return nil, err
	}
	return b, nil
}

// checkSQLiteCopy opens a SQLite backup read-only and runs PRAGMA
// integrity_check.
func checkSQLiteCopy(ctx context.Context, p string) (string, error) {
	if strings.ContainsAny(p, "?#") {
		return "", fmt.Errorf("unsupported path %q", p)
	}
	cp, err := sql.Open("sqlite", "file:"+p+"?mode=ro")
	if err != nil {
		return "", fmt.Errorf("open copy: %w", err)
	}
	defer cp.Close()
	var res string
	if err := cp.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&res); err != nil {
		return "", fmt.Errorf("check copy: %w", err)
	}
	if res != "ok" {
		return "", fmt.Errorf("copy failed integrity_check: %s", res)
	}
	return "PRAGMA integrity_check: ok", nil
}

// checkPgDump lists a custom-format dump with pg_restore.
func checkPgDump(ctx context.Context, p string) (string, error) {
	bin, err := exec.LookPath("pg_restore")
	if err != nil {
		return "pg_dump exit 0 (pg_restore not installed: dump not listed)", nil
	}
	out, err := exec.CommandContext(ctx, bin, "--list", p).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("pg_restore --list rejects the dump: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return fmt.Sprintf("pg_restore --list: %d lines", bytes.Count(out, []byte("\n"))), nil
}

// PutBackup uploads a database backup and its record to the mirror:
// _backups/db/<name> and _backups/db/<name>.json.
func (m *Mirror) PutBackup(ctx context.Context, b *DBBackup) (string, error) {
	f, err := os.Open(b.Path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	key := backupPrefix + b.Name
	if err := m.Blob.Put(ctx, key, PutInput{Body: f, Size: b.Size, SHA256: b.SHA256}); err != nil {
		return "", fmt.Errorf("upload %s: %w", b.Name, err)
	}
	rec := *b
	rec.Path = ""
	meta, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", err
	}
	if err := m.Blob.Put(ctx, key+".json", PutInput{Body: bytes.NewReader(meta), Size: int64(len(meta)), SHA256: utils.SHA256Bytes(meta), ContentType: "application/json"}); err != nil {
		return "", fmt.Errorf("upload %s.json: %w", b.Name, err)
	}
	return key, nil
}

// ListBackups returns the database backups recorded in the mirror, newest
// first.
func (m *Mirror) ListBackups(ctx context.Context) ([]DBBackup, error) {
	objs, err := m.Blob.List(ctx, backupPrefix)
	if err != nil {
		return nil, err
	}
	var out []DBBackup
	for _, o := range objs {
		if !strings.HasSuffix(o.Key, ".json") {
			continue
		}
		rec, err := m.readBackupRecord(ctx, o.Key)
		if err != nil {
			// One damaged record must not hide the others.
			m.logger().Warn("database backup record skipped", "key", o.Key, "error", err.Error())
			continue
		}
		out = append(out, *rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *Mirror) readBackupRecord(ctx context.Context, key string) (*DBBackup, error) {
	rc, _, err := m.Blob.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var rec DBBackup
	if err := json.NewDecoder(io.LimitReader(rc, 1<<20)).Decode(&rec); err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	if rec.Name == "" || strings.Contains(rec.Name, "/") || !isHexSHA256(rec.SHA256) {
		return nil, fmt.Errorf("backup record %s is malformed", key)
	}
	return &rec, nil
}

// FetchBackup downloads one database backup into outDir, checking its size
// and sha256 against its record; a mismatch leaves no file.
func (m *Mirror) FetchBackup(ctx context.Context, name, outDir string) (*DBBackup, error) {
	if name == "" || strings.ContainsAny(name, "/\\") || strings.HasPrefix(name, ".") {
		return nil, fmt.Errorf("invalid backup name %q", name)
	}
	rec, err := m.readBackupRecord(ctx, backupPrefix+name+".json")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(outDir, 0o700); err != nil {
		return nil, err
	}
	dest := filepath.Join(outDir, name)
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("%s exists: %w", dest, fs.ErrExist)
	}
	rc, _, err := m.Blob.Get(ctx, backupPrefix+name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	tmp := dest + ".part"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	_, err = io.Copy(f, VerifyingReader(rc, rec.Size, rec.SHA256))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return nil, fmt.Errorf("fetch backup %s: %w", name, err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		return nil, err
	}
	rec.Path = dest
	return rec, nil
}
