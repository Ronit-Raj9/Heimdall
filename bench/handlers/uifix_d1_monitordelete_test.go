package handlers

import (
	"context"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// fakeCerts is a certificate source that can delete.
type fakeCerts struct{ deleted []string }

func (f *fakeCerts) List(context.Context) (*schemas.MonitorCertList, error) {
	return &schemas.MonitorCertList{}, nil
}
func (f *fakeCerts) Get(context.Context, string) (*schemas.MonitorCertificate, error) {
	return nil, schemas.ErrNotFound
}
func (f *fakeCerts) Report(context.Context, string) ([]byte, error) { return nil, schemas.ErrNotFound }
func (f *fakeCerts) Delete(_ context.Context, id string) error {
	if id != "mc_a" {
		return schemas.ErrNotFound
	}
	f.deleted = append(f.deleted, id)
	return nil
}

func callAs(r *router.Router, method, uri string, p *auth.Principal) int {
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	if p != nil {
		auth.SetPrincipal(&ctx, p)
	}
	r.Handler(&ctx)
	return ctx.Response.StatusCode()
}

// TestMonitorCertDelete: an admin deletes a certificate; a member or a
// viewer is refused by the handler too; a missing one is 404.
func TestMonitorCertDelete(t *testing.T) {
	src := &fakeCerts{}
	r := router.New()
	NewMonitorCertHandler(testMode, src).RegisterRoutes(r)
	p := APIPrefix + "/monitors/certifications/"
	for _, role := range []auth.Role{auth.RoleViewer, auth.RoleMember} {
		if s := callAs(r, "DELETE", p+"mc_a", &auth.Principal{Kind: auth.KindUser, UserID: "usr_x", OrgID: "org_a", Role: role}); s != 403 {
			t.Fatalf("%s: %d", role, s)
		}
	}
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_a", OrgID: "org_a", Role: auth.RoleAdmin}
	if s := callAs(r, "DELETE", p+"mc_a", admin); s != 200 || len(src.deleted) != 1 {
		t.Fatalf("admin: %d %v", s, src.deleted)
	}
	if s := callAs(r, "DELETE", p+"mc_gone", admin); s != 404 {
		t.Fatalf("missing: %d", s)
	}
}
