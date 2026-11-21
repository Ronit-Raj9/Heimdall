package handlers

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func actorCtx(p *auth.Principal, body string) *fasthttp.RequestCtx {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetBody([]byte(body))
	if p != nil {
		auth.SetPrincipal(&ctx, p)
	}
	return &ctx
}

func TestReleaseApproverFromPrincipal(t *testing.T) {
	body := `{"exp_id":"e_test","approver":"someone else"}`
	cases := []struct {
		name   string
		p      *auth.Principal
		status int
		want   string
	}{
		{"admin user", &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", Email: "a@x.io", Role: auth.RoleAdmin}, 201, "a@x.io (usr_1)"},
		{"owner token with creator", &auth.Principal{Kind: auth.KindToken, UserID: "usr_2", Email: "o@x.io", TokenID: "tok_9", Role: auth.RoleOwner}, 201, "o@x.io (usr_2) via token tok_9"},
		{"admin token without creator", &auth.Principal{Kind: auth.KindToken, TokenID: "tok_8", Role: auth.RoleAdmin}, 201, "token tok_8"},
		{"member refused", &auth.Principal{Kind: auth.KindUser, UserID: "usr_3", Role: auth.RoleMember}, 403, ""},
		{"anonymous refused", auth.Anonymous(), 403, ""},
		{"local keeps declared name", auth.Local(), 201, "someone else"},
		{"no principal keeps declared name", nil, 201, "someone else"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeReleases{}
			h := NewReleasesHandler(testMode, f, nil)
			ctx := actorCtx(tc.p, body)
			h.createRelease(ctx)
			if ctx.Response.StatusCode() != tc.status {
				t.Fatalf("status = %d %s, want %d", ctx.Response.StatusCode(), ctx.Response.Body(), tc.status)
			}
			if tc.status != 201 {
				if len(f.recs) != 0 {
					t.Fatalf("refused call recorded %+v", f.recs)
				}
				return
			}
			if len(f.recs) != 1 || f.recs[0].Approver != tc.want {
				t.Fatalf("approver = %+v, want %q", f.recs, tc.want)
			}
		})
	}
}

func TestReviewReviewerFromPrincipal(t *testing.T) {
	cases := []struct {
		name, body string
		p          *auth.Principal
		status     int
		want       string
	}{
		{"member user, body ignored", `{"run_id":"r_known","decision":"pass","reason":"x","reviewer":"ana"}`,
			&auth.Principal{Kind: auth.KindUser, UserID: "usr_1", Email: "m@x.io", Role: auth.RoleMember}, 201, "m@x.io (usr_1)"},
		{"member user, no body reviewer", `{"run_id":"r_known","decision":"pass","reason":"x"}`,
			&auth.Principal{Kind: auth.KindUser, UserID: "usr_1", Email: "m@x.io", Role: auth.RoleMember}, 201, "m@x.io (usr_1)"},
		{"anonymous refused", `{"run_id":"r_known","decision":"pass","reason":"x","reviewer":"ana"}`, auth.Anonymous(), 403, ""},
		{"local keeps declared name", `{"run_id":"r_known","decision":"pass","reason":"x","reviewer":"ana"}`, auth.Local(), 201, "ana"},
		{"local still requires a name", `{"run_id":"r_known","decision":"pass","reason":"x"}`, nil, 400, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeReviews{}
			h := NewReviewsHandler(schemas.ModeLive, f, nil)
			ctx := actorCtx(tc.p, tc.body)
			h.postReview(ctx)
			if ctx.Response.StatusCode() != tc.status {
				t.Fatalf("status = %d %s, want %d", ctx.Response.StatusCode(), ctx.Response.Body(), tc.status)
			}
			if tc.status == 201 && (len(f.added) != 1 || f.added[0].Reviewer != tc.want) {
				t.Fatalf("reviewer = %+v, want %q", f.added, tc.want)
			}
			if tc.status != 201 && len(f.added) != 0 && !strings.Contains(tc.name, "keeps") {
				t.Fatalf("refused call recorded %+v", f.added)
			}
		})
	}
}
