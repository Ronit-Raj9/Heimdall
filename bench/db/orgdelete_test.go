package db_test

import (
	"context"
	"errors"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

func TestOrgDeletionScheduleAndPurge(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		owner, err := d.CreateUser(ctx, db.User{Email: "owner@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		o, err := d.CreateOrg(ctx, db.NewOrg{Slug: "gone", Name: "Gone", OwnerID: owner.ID, PublicRead: true})
		if err != nil {
			t.Fatal(err)
		}
		keep, err := d.CreateOrg(ctx, db.NewOrg{Slug: "stays", Name: "Stays", OwnerID: owner.ID})
		if err != nil {
			t.Fatal(err)
		}
		for _, org := range []string{o.ID, keep.ID} {
			if err := d.SetSetting(ctx, org, "k", "v"); err != nil {
				t.Fatal(err)
			}
		}
		before, err := d.AppendAudit(ctx, db.AuditEntry{OrgID: o.ID, ActorType: db.ActorUser, ActorID: owner.ID, Action: "member.add"})
		if err != nil {
			t.Fatal(err)
		}
		audit := &db.AuditEntry{OrgID: o.ID, ActorType: db.ActorUser, ActorID: owner.ID, Action: "org.delete.requested", TargetType: "org", TargetID: o.ID}
		od, err := d.ScheduleOrgDeletion(ctx, o.ID, owner.ID, audit)
		if err != nil || od.Status != db.OrgDeletionScheduled || od.Slug != "gone" {
			t.Fatalf("schedule %+v %v", od, err)
		}
		if _, err := d.ScheduleOrgDeletion(ctx, o.ID, owner.ID, nil); !errors.Is(err, db.ErrConflict) {
			t.Fatalf("second schedule: %v", err)
		}
		// Access is gone at once; the org row stays until the purge.
		if _, err := d.RoleOf(ctx, o.ID, owner.ID); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("membership kept: %v", err)
		}
		if got, err := d.OrgByID(ctx, o.ID); err != nil || got.PublicRead {
			t.Fatalf("org after schedule %+v %v", got, err)
		}
		pending, err := d.PendingOrgDeletions(ctx)
		if err != nil || len(pending) != 1 || pending[0].OrgID != o.ID {
			t.Fatalf("pending %+v %v", pending, err)
		}

		counts, err := d.PurgeOrgRows(ctx, o.ID)
		if err != nil || counts["orgs"] != 1 || counts["settings"] != 1 {
			t.Fatalf("purge %v %v", counts, err)
		}
		if _, err := d.OrgByID(ctx, o.ID); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("org row kept: %v", err)
		}
		if _, found, _ := d.GetSetting(ctx, keep.ID, "k"); !found {
			t.Fatal("other org's settings purged")
		}
		// Again is a no-op.
		if counts, err := d.PurgeOrgRows(ctx, o.ID); err != nil || len(counts) != 0 {
			t.Fatalf("second purge %v %v", counts, err)
		}
		if err := d.MarkOrgDeletion(ctx, o.ID, db.OrgDeletionDone, "", map[string]any{"rows": counts}); err != nil {
			t.Fatal(err)
		}
		got, err := d.OrgDeletionOf(ctx, o.ID)
		if err != nil || got.Status != db.OrgDeletionDone || got.FinishedAt == nil {
			t.Fatalf("receipt %+v %v", got, err)
		}
		if pending, _ := d.PendingOrgDeletions(ctx); len(pending) != 0 {
			t.Fatalf("pending after done %+v", pending)
		}
		// The audit trail outlives the org and still verifies.
		rep, err := d.VerifyAuditChain(ctx, o.ID)
		if err != nil || !rep.OK || rep.Chained < 2 || rep.HeadSeq <= before.Seq {
			t.Fatalf("audit after delete %+v %v", rep, err)
		}
	})
}
