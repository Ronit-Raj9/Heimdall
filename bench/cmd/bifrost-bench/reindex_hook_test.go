package main

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/tenancy"
)

// Gap G13: after a hosted sealed-run import (tenancy.ImportRuns, as POST
// /uploads/runs does), the Reindex service makes the experiments visible in
// the in-memory index and the database results index without a restart.
func TestOrgReindexAfterImport(t *testing.T) {
	ctx := context.Background()
	d := dbtest.OpenSQLite(t)
	root := t.TempDir()
	st, err := store.NewFSStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	mem := store.NewIndex(st)
	if err := mem.Rebuild(ctx); err != nil {
		t.Fatal(err)
	}
	results := d.OrgIndex("org_g13", st, nil)
	if _, err := results.Sync(ctx); err != nil {
		t.Fatal(err)
	}
	rep, err := tenancy.ImportRuns(ctx, recordedRoot, root, nil)
	if err != nil || rep.Imported == 0 {
		t.Fatalf("import %+v %v", rep, err)
	}
	if n := len(mem.Experiments()); n != 0 {
		t.Fatalf("index already has %d experiments before reindex", n)
	}
	if err := orgReindex(indexAdapter{ix: mem, root: root}, results)(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(mem.Experiments()); n != rep.Imported {
		t.Fatalf("in-memory index has %d experiments, imported %d", n, rep.Imported)
	}
	snap, err := results.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Status().Experiments; got != rep.Imported {
		t.Fatalf("database index has %d experiments, imported %d", got, rep.Imported)
	}
}
