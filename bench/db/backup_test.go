package db_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

func TestBackupSQLite(t *testing.T) {
	ctx := context.Background()
	d := dbtest.OpenSQLite(t)
	if _, err := d.CreateOrg(ctx, db.NewOrg{Slug: "kept"}); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "sub", "copy.db")
	if err := d.Backup(ctx, out); err != nil {
		t.Fatal(err)
	}
	if err := d.Backup(ctx, out); err == nil {
		t.Fatal("backup overwrote an existing file")
	}
	c, err := db.Open(ctx, db.Config{Driver: db.SQLite, Path: out})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if o, err := c.OrgBySlug(ctx, "kept"); err != nil || o.Slug != "kept" {
		t.Fatalf("backup lacks the org: %v", err)
	}
	if err := c.Migrate(ctx); err != nil {
		t.Fatalf("backup does not migrate cleanly: %v", err)
	}
}

func TestSplitPassword(t *testing.T) {
	cases := []struct{ in, dsn, pw string }{
		{"postgres://u:secret@h:5432/db?sslmode=disable", "postgres://u@h:5432/db?sslmode=disable", "secret"},
		{"postgres://u@h/db", "postgres://u@h/db", ""},
		{"host=h user=u password=secret dbname=db", "host=h user=u dbname=db", "secret"},
	}
	for _, c := range cases {
		dsn, pw := db.SplitPassword(c.in)
		if dsn != c.dsn || pw != c.pw {
			t.Errorf("splitPassword(%q) = %q, %q", c.in, dsn, pw)
		}
	}
}
