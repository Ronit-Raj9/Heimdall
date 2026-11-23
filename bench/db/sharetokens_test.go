package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

// TestShareTokensMigrationAdditive (fix wave 3, N293): migration 0003 adds
// the share index next to the existing tables (orgs keep their rows), the
// index stores only the token's hash, a second write of the same token is a
// no-op, and deleting the org removes its rows through the foreign key.
// It runs on SQLite and, when HEIMDALL_TEST_PG_DSN is set, on Postgres.
func TestShareTokensMigrationAdditive(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		o, err := d.CreateOrg(ctx, db.NewOrg{Slug: "share-co", Name: "Share Co"})
		if err != nil {
			t.Fatal(err)
		}
		tok := "s_" + "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"
		for i := 0; i < 2; i++ {
			if err := d.PutShareToken(ctx, tok, o.ID, "shr_00000000000000000001"); err != nil {
				t.Fatalf("put %d: %v", i, err)
			}
		}
		org, share, err := d.ShareToken(ctx, tok)
		if err != nil || org != o.ID || share != "shr_00000000000000000001" {
			t.Fatalf("lookup: %q %q %v", org, share, err)
		}
		var n int
		var stored string
		if err := d.QueryRow(ctx, `SELECT count(*), max(token_hash) FROM share_tokens`).Scan(&n, &stored); err != nil || n != 1 {
			t.Fatalf("rows %d %v", n, err)
		}
		if stored == tok || stored != db.ShareTokenHash(tok) || len(stored) != 64 {
			t.Fatalf("token stored as %q, want its sha256", stored)
		}
		if _, _, err := d.ShareToken(ctx, "s_unknown"); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("unknown token: %v", err)
		}
		if err := d.DeleteOrg(ctx, o.ID); err != nil {
			t.Fatalf("delete org with share rows: %v", err)
		}
		if err := d.QueryRow(ctx, `SELECT count(*) FROM share_tokens`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("rows after org delete %d %v", n, err)
		}
	})
}
