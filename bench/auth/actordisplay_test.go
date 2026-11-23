package auth

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

// Fix wave 3, L3: platform admin pages name the people they show.
func TestActorDisplay(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	ada, err := d.CreateUser(ctx, db.User{Email: "ada@acme.com", Name: "Ada Lovelace"})
	if err != nil {
		t.Fatal(err)
	}
	bo, err := d.CreateUser(ctx, db.User{Email: "bo@acme.com"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct{ actor, name, email string }{
		{ada.ID, "Ada Lovelace", "ada@acme.com"},
		{"ADA@acme.com", "Ada Lovelace", "ada@acme.com"},
		{bo.ID, "bo", "bo@acme.com"},
		{"cy@other.io", "cy", "cy@other.io"},
		{"usr_gone", "a removed user", ""},
		{"schedule:nightly", "schedule:nightly", ""},
		{"system", "system", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		if name, email := ActorDisplay(ctx, d, c.actor); name != c.name || email != c.email {
			t.Errorf("ActorDisplay(%q) = %q, %q; want %q, %q", c.actor, name, email, c.name, c.email)
		}
	}
	if name, _ := ActorDisplay(ctx, nil, ada.ID); name != "a removed user" {
		t.Errorf("no directory: %q", name)
	}
}

// The signup settings answer names the admin who changed them, never by id.
func TestSignupSettingsNameUpdater(t *testing.T) {
	e, _ := newVerifyEnv(t, SignupAllowlist, []string{"acme.com"}, nil)
	bg := context.Background()
	u, err := e.d.CreateUser(bg, db.User{Email: "root@acme.com", Name: "Root Admin"})
	if err != nil {
		t.Fatal(err)
	}
	setAdmins(t, "", u.ID)
	admin := &Principal{Kind: KindUser, UserID: u.ID, EmailVerified: true}
	got, err := e.svc.SetSignupSettings(bg, admin, SignupSettingsRequest{Mode: "invite"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedByName != "Root Admin" || got.UpdatedByEmail != "root@acme.com" {
		t.Fatalf("set answer = %+v", got)
	}
	if s := e.svc.SignupSettings(bg); s.UpdatedByName != "Root Admin" || s.UpdatedByEmail != "root@acme.com" {
		t.Fatalf("get answer = %+v", s)
	}
}
