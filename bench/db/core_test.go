package db_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

func TestOrgsUsersMemberships(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		alice, err := d.CreateUser(ctx, db.User{Email: "  Alice@Example.com ", Name: "Alice", PasswordHash: "h1"})
		if err != nil {
			t.Fatal(err)
		}
		if alice.Email != "alice@example.com" || alice.ID[:4] != "usr_" {
			t.Fatalf("created %+v", alice)
		}
		if _, err := d.CreateUser(ctx, db.User{Email: "ALICE@example.com"}); !errors.Is(err, db.ErrConflict) {
			t.Fatalf("duplicate email: %v", err)
		}
		if _, err := d.CreateUser(ctx, db.User{Email: "nope"}); err == nil {
			t.Fatal("bad email accepted")
		}
		bob, err := d.CreateUser(ctx, db.User{Email: "bob@example.com", GitHubID: "42"})
		if err != nil {
			t.Fatal(err)
		}
		// Two users without a GitHub id: NULLs do not collide.
		carol, err := d.CreateUser(ctx, db.User{Email: "carol@example.com"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.CreateUser(ctx, db.User{Email: "dup@example.com", GitHubID: "42"}); !errors.Is(err, db.ErrConflict) {
			t.Fatalf("duplicate github id: %v", err)
		}
		for name, get := range map[string]func() (*db.User, error){
			"id":     func() (*db.User, error) { return d.UserByID(ctx, bob.ID) },
			"email":  func() (*db.User, error) { return d.UserByEmail(ctx, "BOB@example.com") },
			"github": func() (*db.User, error) { return d.UserByGitHubID(ctx, "42") },
		} {
			u, err := get()
			if err != nil || u.ID != bob.ID || u.GitHubID != "42" {
				t.Fatalf("by %s: %+v %v", name, u, err)
			}
		}
		if _, err := d.UserByEmail(ctx, "zed@example.com"); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("missing user: %v", err)
		}
		if _, err := d.UserByGitHubID(ctx, ""); !errors.Is(err, db.ErrNotFound) {
			t.Fatal("empty github id matched")
		}
		carol.Name, carol.GitHubID, carol.Disabled, carol.PasswordHash = "Carol", "77", true, "h3"
		if err := d.UpdateUser(ctx, *carol); err != nil {
			t.Fatal(err)
		}
		if u, _ := d.UserByID(ctx, carol.ID); u.Name != "Carol" || u.GitHubID != "77" || !u.Disabled || u.PasswordHash != "h3" {
			t.Fatalf("updated %+v", u)
		}
		carol.GitHubID = "42"
		if err := d.UpdateUser(ctx, *carol); !errors.Is(err, db.ErrConflict) {
			t.Fatalf("update to taken github id: %v", err)
		}
		if err := d.UpdateUser(ctx, db.User{ID: "usr_missing", Email: "m@example.com"}); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("update missing: %v", err)
		}
		if n, err := d.CountUsers(ctx); err != nil || n != 3 {
			t.Fatalf("count %d %v", n, err)
		}

		acme, err := d.CreateOrg(ctx, db.NewOrg{Slug: "acme", Name: "Acme", OwnerID: alice.ID})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := d.CreateOrg(ctx, db.NewOrg{Slug: "acme"}); !errors.Is(err, db.ErrConflict) {
			t.Fatalf("duplicate slug: %v", err)
		}
		if _, err := d.CreateOrg(ctx, db.NewOrg{Slug: "Bad Slug"}); err == nil {
			t.Fatal("bad slug accepted")
		}
		if _, err := d.CreateOrg(ctx, db.NewOrg{Slug: "ghost", OwnerID: "usr_missing"}); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("missing owner: %v", err)
		}
		if _, err := d.OrgBySlug(ctx, "ghost"); !errors.Is(err, db.ErrNotFound) {
			t.Fatal("org with a failed owner insert was kept")
		}
		demo, err := d.CreateOrg(ctx, db.NewOrg{Slug: "demo", PublicRead: true})
		if err != nil {
			t.Fatal(err)
		}
		if o, err := d.OrgBySlug(ctx, "demo"); err != nil || !o.PublicRead || o.ID != demo.ID || o.Name != "demo" {
			t.Fatalf("by slug %+v %v", o, err)
		}
		if err := d.SetOrgPublicRead(ctx, demo.ID, false); err != nil {
			t.Fatal(err)
		}
		if o, _ := d.OrgByID(ctx, demo.ID); o.PublicRead {
			t.Fatal("public_read not cleared")
		}
		if err := d.SetOrgPublicRead(ctx, "org_missing", true); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("missing org: %v", err)
		}
		if err := d.RenameOrg(ctx, acme.ID, "Acme Inc"); err != nil {
			t.Fatal(err)
		}
		if orgs, err := d.ListOrgs(ctx); err != nil || len(orgs) != 2 || orgs[0].Slug != "acme" || orgs[0].Name != "Acme Inc" {
			t.Fatalf("list %+v %v", orgs, err)
		}

		if err := d.AddMember(ctx, acme.ID, bob.ID, db.RoleMember); err != nil {
			t.Fatal(err)
		}
		if err := d.AddMember(ctx, acme.ID, bob.ID, db.RoleAdmin); !errors.Is(err, db.ErrConflict) {
			t.Fatalf("duplicate member: %v", err)
		}
		if err := d.AddMember(ctx, acme.ID, "usr_missing", db.RoleMember); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("missing user member: %v", err)
		}
		if err := d.AddMember(ctx, acme.ID, carol.ID, "god"); err == nil {
			t.Fatal("bad role accepted")
		}
		if err := d.AddMember(ctx, demo.ID, bob.ID, db.RoleViewer); err != nil {
			t.Fatal(err)
		}
		if role, err := d.RoleOf(ctx, acme.ID, alice.ID); err != nil || role != db.RoleOwner {
			t.Fatalf("alice role %q %v", role, err)
		}
		if _, err := d.RoleOf(ctx, acme.ID, carol.ID); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("non-member role: %v", err)
		}
		uo, err := d.OrgsForUser(ctx, bob.ID)
		if err != nil || len(uo) != 2 || uo[0].Slug != "acme" || uo[0].Role != db.RoleMember || uo[1].Role != db.RoleViewer {
			t.Fatalf("bob's orgs %+v %v", uo, err)
		}
		ms, err := d.Members(ctx, acme.ID)
		if err != nil || len(ms) != 2 || ms[0].Email != "alice@example.com" || ms[1].Role != db.RoleMember {
			t.Fatalf("members %+v %v", ms, err)
		}

		// The last owner stays.
		if err := d.RemoveMember(ctx, acme.ID, alice.ID); !errors.Is(err, db.ErrLastOwner) {
			t.Fatalf("remove last owner: %v", err)
		}
		if err := d.SetRole(ctx, acme.ID, alice.ID, db.RoleAdmin); !errors.Is(err, db.ErrLastOwner) {
			t.Fatalf("demote last owner: %v", err)
		}
		if err := d.SetRole(ctx, acme.ID, bob.ID, db.RoleOwner); err != nil {
			t.Fatal(err)
		}
		if err := d.SetRole(ctx, acme.ID, alice.ID, db.RoleViewer); err != nil {
			t.Fatalf("demote one of two owners: %v", err)
		}
		if err := d.RemoveMember(ctx, acme.ID, bob.ID); !errors.Is(err, db.ErrLastOwner) {
			t.Fatalf("remove new last owner: %v", err)
		}
		if err := d.RemoveMember(ctx, acme.ID, alice.ID); err != nil {
			t.Fatal(err)
		}
		if err := d.RemoveMember(ctx, acme.ID, alice.ID); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("remove twice: %v", err)
		}
		if err := d.SetRole(ctx, "org_missing", bob.ID, db.RoleAdmin); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("set role missing org: %v", err)
		}
	})
}

func TestLastOwnerConcurrent(t *testing.T) {
	// Two owners removing each other at once: one wins, one owner remains.
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		a, _ := d.CreateUser(ctx, db.User{Email: "a@x.io"})
		b, _ := d.CreateUser(ctx, db.User{Email: "b@x.io"})
		o, err := d.CreateOrg(ctx, db.NewOrg{Slug: "two", OwnerID: a.ID})
		if err != nil {
			t.Fatal(err)
		}
		if err := d.AddMember(ctx, o.ID, b.ID, db.RoleOwner); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 2)
		go func() { errs <- d.RemoveMember(ctx, o.ID, a.ID) }()
		go func() { errs <- d.RemoveMember(ctx, o.ID, b.ID) }()
		e1, e2 := <-errs, <-errs
		if (e1 == nil) == (e2 == nil) {
			t.Fatalf("want exactly one success: %v / %v", e1, e2)
		}
		ms, _ := d.Members(ctx, o.ID)
		if len(ms) != 1 || ms[0].Role != db.RoleOwner {
			t.Fatalf("members left %+v", ms)
		}
	})
}

func TestAudit(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		base := time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC)
		for i := 0; i < 7; i++ {
			e := db.AuditEntry{OrgID: "org_a", ActorType: db.ActorUser, ActorID: "usr_1", Action: "member.add",
				TargetType: "user", TargetID: fmt.Sprintf("usr_%d", i), IP: "10.0.0.1", At: base.Add(time.Duration(i/2) * time.Minute),
				Details: db.AuditDetails(map[string]int{"i": i})}
			if i%3 == 0 {
				e.Action = "token.create"
			}
			if _, err := d.AppendAudit(ctx, e); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := d.AppendAudit(ctx, db.AuditEntry{OrgID: "org_b", ActorType: db.ActorRunner, ActorID: "rnr_1", Action: "runner.heartbeat"}); err != nil {
			t.Fatal(err)
		}
		if _, err := d.AppendAudit(ctx, db.AuditEntry{ActorType: "alien", Action: "x"}); err == nil {
			t.Fatal("bad actor type accepted")
		}
		if _, err := d.AppendAudit(ctx, db.AuditEntry{ActorType: db.ActorSystem, Action: "x", Details: json.RawMessage("{")}); err == nil {
			t.Fatal("bad details accepted")
		}
		// Paging walks every row of org_a once, newest first.
		var all []db.AuditEntry
		cursor := ""
		for pages := 0; ; pages++ {
			p, err := d.ListAudit(ctx, db.AuditFilter{OrgID: "org_a", Limit: 3, Cursor: cursor})
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, p.Entries...)
			if p.Next == "" {
				break
			}
			cursor = p.Next
			if pages > 5 {
				t.Fatal("paging does not end")
			}
		}
		if len(all) != 7 {
			t.Fatalf("paged %d rows", len(all))
		}
		seen := map[string]bool{}
		for i, e := range all {
			if seen[e.ID] {
				t.Fatalf("row %s twice", e.ID)
			}
			seen[e.ID] = true
			if i > 0 && e.At.After(all[i-1].At) {
				t.Fatal("not newest first")
			}
		}
		if string(all[0].Details) == "" || all[0].IP != "10.0.0.1" {
			t.Fatalf("row %+v", all[0])
		}
		p, err := d.ListAudit(ctx, db.AuditFilter{OrgID: "org_a", Action: "token.*"})
		if err != nil || len(p.Entries) != 3 {
			t.Fatalf("prefix filter: %d %v", len(p.Entries), err)
		}
		p, err = d.ListAudit(ctx, db.AuditFilter{OrgID: "org_a", Action: "member.add", TargetID: "usr_4"})
		if err != nil || len(p.Entries) != 1 {
			t.Fatalf("exact filter: %d %v", len(p.Entries), err)
		}
		p, err = d.ListAudit(ctx, db.AuditFilter{OrgID: "org_a", Since: base.Add(time.Minute), Until: base.Add(3 * time.Minute)})
		if err != nil || len(p.Entries) != 4 {
			t.Fatalf("time window: %d %v", len(p.Entries), err)
		}
		p, err = d.ListAudit(ctx, db.AuditFilter{AllOrgs: true, ActorType: db.ActorRunner})
		if err != nil || len(p.Entries) != 1 || p.Entries[0].OrgID != "org_b" || string(p.Entries[0].Details) != "{}" {
			t.Fatalf("all orgs: %+v %v", p.Entries, err)
		}
		if _, err := d.ListAudit(ctx, db.AuditFilter{Cursor: "%%%"}); err == nil {
			t.Fatal("bad cursor accepted")
		}
	})
}

func TestSettings(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		if _, found, err := d.GetSetting(ctx, "org_a", "retention_days"); err != nil || found {
			t.Fatalf("unset: %v %v", found, err)
		}
		for _, v := range []string{"30", "90"} {
			if err := d.SetSetting(ctx, "org_a", "retention_days", v); err != nil {
				t.Fatal(err)
			}
		}
		if err := d.SetSetting(ctx, "", "retention_days", "7"); err != nil {
			t.Fatal(err)
		}
		if v, found, err := d.GetSetting(ctx, "org_a", "retention_days"); err != nil || !found || v != "90" {
			t.Fatalf("get %q %v %v", v, found, err)
		}
		all, err := d.Settings(ctx, "")
		if err != nil || all["retention_days"] != "7" || len(all) != 1 {
			t.Fatalf("instance settings %v %v", all, err)
		}
		if err := d.DeleteSetting(ctx, "org_a", "retention_days"); err != nil {
			t.Fatal(err)
		}
		if _, found, _ := d.GetSetting(ctx, "org_a", "retention_days"); found {
			t.Fatal("deleted setting found")
		}
	})
}

func TestUserHelpersAndDeleteOrg(t *testing.T) {
	dbtest.Each(t, func(t *testing.T, d *db.DB) {
		ctx := context.Background()
		if err := d.Ping(ctx); err != nil {
			t.Fatal(err)
		}
		u, _ := d.CreateUser(ctx, db.User{Email: "u@x.io"})
		v, _ := d.CreateUser(ctx, db.User{Email: "v@x.io", GitHubID: "9"})
		if err := d.SetUserPassword(ctx, u.ID, "argon"); err != nil {
			t.Fatal(err)
		}
		if err := d.SetUserDisabled(ctx, u.ID, true); err != nil {
			t.Fatal(err)
		}
		if err := d.LinkGitHub(ctx, u.ID, "9"); !errors.Is(err, db.ErrConflict) {
			t.Fatalf("link taken github id: %v", err)
		}
		if err := d.LinkGitHub(ctx, u.ID, "10"); err != nil {
			t.Fatal(err)
		}
		if err := d.LinkGitHub(ctx, v.ID, ""); err != nil {
			t.Fatal(err)
		}
		got, _ := d.UserByID(ctx, u.ID)
		if got.PasswordHash != "argon" || !got.Disabled || got.GitHubID != "10" {
			t.Fatalf("user %+v", got)
		}
		if err := d.SetUserPassword(ctx, "usr_missing", "x"); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("missing user: %v", err)
		}
		o, err := d.CreateOrg(ctx, db.NewOrg{Slug: "gone", OwnerID: u.ID})
		if err != nil {
			t.Fatal(err)
		}
		_ = d.SetSetting(ctx, o.ID, "k", "v")
		if _, err := d.AppendAudit(ctx, db.AuditEntry{OrgID: o.ID, ActorType: db.ActorSystem, Action: "org.create"}); err != nil {
			t.Fatal(err)
		}
		if err := d.DeleteOrg(ctx, o.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := d.OrgByID(ctx, o.ID); !errors.Is(err, db.ErrNotFound) {
			t.Fatal("org kept")
		}
		if orgs, _ := d.OrgsForUser(ctx, u.ID); len(orgs) != 0 {
			t.Fatal("membership kept")
		}
		if s, _ := d.Settings(ctx, o.ID); len(s) != 0 {
			t.Fatal("settings kept")
		}
		if p, _ := d.ListAudit(ctx, db.AuditFilter{OrgID: o.ID}); len(p.Entries) != 1 {
			t.Fatal("audit not kept")
		}
		if err := d.DeleteOrg(ctx, o.ID); !errors.Is(err, db.ErrNotFound) {
			t.Fatalf("delete twice: %v", err)
		}
	})
}
