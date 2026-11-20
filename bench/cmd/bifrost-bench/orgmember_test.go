package main

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

// 4-D-04: an ownerless demo org can be given an owner, idempotently.
func TestAddOrgMemberRepairsOwnerlessOrg(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Open(t)
	o, err := d.CreateOrg(ctx, db.NewOrg{Slug: "demo", Name: "demo", PublicRead: true})
	if err != nil {
		t.Fatal(err)
	}
	u, err := d.CreateUser(ctx, db.User{Email: "op@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := addOrgMember(ctx, d, "demo", "op@example.com", "owner")
	if err != nil || !strings.Contains(msg, "added") {
		t.Fatalf("add = %q, %v", msg, err)
	}
	ms, _ := d.Members(ctx, o.ID)
	if len(ms) != 1 || ms[0].UserID != u.ID || ms[0].Role != "owner" {
		t.Fatalf("members = %+v", ms)
	}
	// Rerun (bootstrap rerun): success, role unchanged.
	if msg, err := addOrgMember(ctx, d, o.ID, "op@example.com", "admin"); err != nil || !strings.Contains(msg, "already") {
		t.Fatalf("rerun = %q, %v", msg, err)
	}
	if _, err := addOrgMember(ctx, d, "demo", "nobody@example.com", "owner"); err == nil {
		t.Fatal("unknown user accepted")
	}
	if _, err := addOrgMember(ctx, d, "nope", "op@example.com", "owner"); err == nil {
		t.Fatal("unknown org accepted")
	}
	if _, err := addOrgMember(ctx, d, "demo", "op@example.com", "god"); err == nil {
		t.Fatal("bad role accepted")
	}
}
