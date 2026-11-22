package leasebook

import (
	"context"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBookRoundTrip(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		now := time.UnixMilli(1_800_000_000_000).UTC()
		b := &Book{DB: d, Now: func() time.Time { return now }}
		ctx := context.Background()
		rec := engine.LeaseRecord{ID: "l_a", OrgID: "org_1", ExpID: "exp_1", SlotID: "s1", WorkerID: "w1", Owner: "worker:w1:l_a",
			Attempt: 2, ReservedUSD: 0.5, GPUIDs: []string{"GPU-1"}, LastBeat: now,
			Worker: schemas.WorkerInfo{ID: "w1", Slots: 4, Pool: "default"}, Scope: schemas.RunnerScope{OrgID: "org_1", Pool: "default", TokenID: "tok"}}
		if err := b.PutLease(ctx, rec); err != nil {
			t.Fatal(err)
		}
		other := rec
		other.ID, other.OrgID = "l_b", "org_2"
		if err := b.PutLease(ctx, other); err != nil {
			t.Fatal(err)
		}
		later := now.Add(time.Minute)
		if err := b.TouchLease(ctx, "l_a", later); err != nil {
			t.Fatal(err)
		}
		got, err := b.OpenLeases(ctx, "org_1")
		if err != nil || len(got) != 1 {
			t.Fatalf("open %+v %v", got, err)
		}
		if g := got[0]; g.SlotID != "s1" || g.Attempt != 2 || g.ReservedUSD != 0.5 || !g.LastBeat.Equal(later) || g.Scope.TokenID != "tok" || g.Worker.Slots != 4 {
			t.Fatalf("record %+v", g)
		}
		if err := b.EndLease(ctx, "l_a", "completed"); err != nil {
			t.Fatal(err)
		}
		if err := b.TouchLease(ctx, "l_a", later.Add(time.Minute)); err != nil {
			t.Fatal(err)
		}
		if got, _ := b.OpenLeases(ctx, "org_1"); len(got) != 0 {
			t.Fatalf("ended lease still open: %+v", got)
		}
		var state string
		if err := d.QueryRow(ctx, `SELECT state FROM remote_leases WHERE id = ?`, "l_a").Scan(&state); err != nil || state != "ended" {
			t.Fatalf("state %q %v", state, err)
		}
		now = now.Add(8 * 24 * time.Hour)
		if _, err := b.OpenLeases(ctx, "org_1"); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := d.QueryRow(ctx, `SELECT COUNT(*) FROM remote_leases WHERE org_id = ?`, "org_1").Scan(&n); err != nil || n != 0 {
			t.Fatalf("old ended rows kept: %d %v", n, err)
		}
	})
}
