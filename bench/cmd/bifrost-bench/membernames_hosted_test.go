package main

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

// N33, N168: the members resolver finds a member by user id and by
// lower-case email, and names a member without a display name by the part
// of the email before "@", never by the full email.
func TestHostedMemberNamesKeyedByEmail(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Open(t)
	o, err := d.CreateOrg(ctx, db.NewOrg{Slug: "acme", Name: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	named, err := d.CreateUser(ctx, db.User{Email: "Ada@Example.com", Name: "Ada Lovelace"})
	if err != nil {
		t.Fatal(err)
	}
	plain, err := d.CreateUser(ctx, db.User{Email: "ronit@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{named.ID, plain.ID} {
		if err := d.AddMember(ctx, o.ID, u, db.RoleMember); err != nil {
			t.Fatal(err)
		}
	}
	names, err := hostedMemberNames(&orgService{env: &hostedEnv{db: d}})(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		named.ID: "Ada Lovelace", "ada@example.com": "Ada Lovelace",
		plain.ID: "ronit", "ronit@example.com": "ronit",
	}
	if len(names) != len(want) {
		t.Fatalf("names %v, want %v", names, want)
	}
	for k, v := range want {
		if names[k] != v {
			t.Fatalf("names[%q] = %q, want %q (all %v)", k, names[k], v, names)
		}
	}
}
