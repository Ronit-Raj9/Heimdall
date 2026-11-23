package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
)

// Fix wave 3, L3: the server admin's image request list names who asked,
// with the email beside the name, never only a user id.
func TestAdminImageRequestsNameRequester(t *testing.T) {
	d := dbtest.OpenSQLite(t)
	u, err := d.CreateUser(context.Background(), db.User{Email: "bo@acme.com", Name: "Bo Builder"})
	if err != nil {
		t.Fatal(err)
	}
	def := quota.DefaultLimits
	def.SharedPool = true
	def.Runtime = "runc"
	q := quota.Open(d, quota.Options{Defaults: &def})
	images := &sharedpool.ImageStore{DB: d}
	if _, err := images.Request(context.Background(), "org1", "python@sha256:"+strings.Repeat("b", 64), 100, "", u.ID); err != nil {
		t.Fatal(err)
	}
	h := NewSharedPoolHandler(schemas.ModeLive, q, images, nil, nil).WithAdminCheck(func(p *auth.Principal) bool { return p != nil && p.Email == "admin@x.io" })
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "a1", OrgID: "org1", Role: auth.RoleMember, Email: "admin@x.io"}
	code, body := spServe(t, h, admin, "GET", "/api/bench/admin/images", "", "")
	if code != 200 || !strings.Contains(body, `"requested_by_name":"Bo Builder"`) || !strings.Contains(body, `"requested_by_email":"bo@acme.com"`) {
		t.Fatalf("admin list: %d %s", code, body)
	}
}
