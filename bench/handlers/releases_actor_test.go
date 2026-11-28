package handlers

import (
	"net"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

func TestApproverDisplay(t *testing.T) {
	cases := map[string]string{
		"ana@example.com (usr_8f2a)":                   "ana",
		"ana@example.com (usr_8f2a) via token tok_123": "ana (API token)",
		"token tok_123":                                "an API token",
		"usr_8f2a":                                     "a member",
		"Ana Lopez":                                    "Ana Lopez",
		"ana@x.io":                                     "ana",
		"":                                             "",
	}
	for in, want := range cases {
		if got := approverDisplay(in); got != want {
			t.Errorf("approverDisplay(%q) = %q, want %q", in, got, want)
		}
	}
}

// A signed-in owner records a release the way the Releases page does; the
// create, list and get responses show no email and no user id (K3).
func TestReleaseResponsesHideApproverEmailAndID(t *testing.T) {
	f := &fakeReleases{}
	r := router.New()
	signIn := func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			auth.SetPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, UserID: "usr_secret42", OrgID: "org_1", Role: auth.RoleOwner, Email: "owner@example.com"})
			next(ctx)
		}
	}
	NewReleasesHandler(testMode, f, func(h fasthttp.RequestHandler) fasthttp.RequestHandler { return h }).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes), signIn)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Shutdown(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	c := &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
	p := APIPrefix + "/releases"
	resp := c.do(t, "POST", p, []byte(`{"exp_id":"e_test","approver":"owner@example.com"}`), map[string]string{"Content-Type": "application/json"})
	if resp.StatusCode() != 201 {
		t.Fatalf("create status %d: %s", resp.StatusCode(), resp.Body())
	}
	if got := f.recs[0].Approver; got != "owner@example.com (usr_secret42)" {
		t.Fatalf("stored approver = %q, want the full audit value", got)
	}
	bodies := []string{string(resp.Body())}
	for _, path := range []string{p, p + "/rel_1"} {
		r := c.do(t, "GET", path, nil, nil)
		if r.StatusCode() != 200 {
			t.Fatalf("GET %s status %d", path, r.StatusCode())
		}
		bodies = append(bodies, string(r.Body()))
	}
	for _, b := range bodies {
		if strings.Contains(b, "@example.com") || strings.Contains(b, "usr_secret42") {
			t.Errorf("response shows the email or user id: %s", b)
		}
		if !strings.Contains(b, `"approver":"owner"`) {
			t.Errorf("response lacks the display name: %s", b)
		}
	}
}
