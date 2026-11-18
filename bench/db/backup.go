package db

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNoPgDump means pg_dump is not on PATH; back the database up with it
// from a machine that has it.
var ErrNoPgDump = errors.New("db: pg_dump not found on PATH; run `pg_dump --format=custom --file <out> \"$HEIMDALL_DATABASE_URL\"` where it is installed")

// Backup writes a consistent copy of the database to out, which must not
// exist. SQLite: VACUUM INTO, an online copy taken in one read transaction
// while writers keep going. Postgres: pg_dump --format=custom when it is on
// PATH (the password goes through PGPASSWORD, never argv), else ErrNoPgDump.
// The file holds password hashes and token hashes: keep it private.
func (d *DB) Backup(ctx context.Context, out string) error {
	if out == "" {
		return errors.New("db: backup needs an output file")
	}
	if _, err := os.Stat(out); err == nil {
		return fmt.Errorf("db: backup target %s exists", out)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o700); err != nil {
		return err
	}
	switch d.driver {
	case SQLite:
		abs, err := filepath.Abs(out)
		if err != nil {
			return err
		}
		if _, err := d.sql.ExecContext(ctx, "VACUUM INTO ?", abs); err != nil {
			return fmt.Errorf("db: backup to %s: %w", out, err)
		}
		return os.Chmod(abs, 0o600)
	case Postgres:
		bin, err := exec.LookPath("pg_dump")
		if err != nil {
			return ErrNoPgDump
		}
		dsn, password := splitPassword(d.dsn)
		cmd := exec.CommandContext(ctx, bin, "--format=custom", "--no-owner", "--file", out, "--dbname", dsn)
		cmd.Env = os.Environ()
		if password != "" {
			cmd.Env = append(cmd.Env, "PGPASSWORD="+password)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			_ = os.Remove(out)
			return fmt.Errorf("db: pg_dump: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return os.Chmod(out, 0o600)
	}
	return fmt.Errorf("db: backup: unknown driver %q", d.driver)
}

// splitPassword takes the password out of a Postgres DSN (URL or key=value
// form) so it can travel in the environment instead of argv.
func splitPassword(dsn string) (string, string) {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil || u.User == nil {
			return dsn, ""
		}
		pw, ok := u.User.Password()
		if !ok {
			return dsn, ""
		}
		u.User = url.User(u.User.Username())
		return u.String(), pw
	}
	var kept []string
	password := ""
	for _, f := range strings.Fields(dsn) {
		if v, ok := strings.CutPrefix(f, "password="); ok {
			password = strings.Trim(v, "'")
			continue
		}
		kept = append(kept, f)
	}
	return strings.Join(kept, " "), password
}
