package tenancy

import (
	"context"
	"net"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type noDir struct{}

func (noDir) OrgByID(context.Context, string) (Org, error)   { return Org{}, ErrOrgNotFound }
func (noDir) OrgBySlug(context.Context, string) (Org, error) { return Org{}, ErrOrgNotFound }
func (noDir) MemberRole(context.Context, string, string) (auth.Role, error) {
	return "", ErrNotMember
}

// TestDispatcherAuditClientIP: audit rows carry the trusted-proxy client
// address the dispatcher resolved, not the proxy's (4-B-08).
func TestDispatcherAuditClientIP(t *testing.T) {
	var got string
	d, err := NewDispatcher(DispatcherConfig{
		Mode: schemas.ModeLive, Dir: noDir{}, Registry: newTestRegistry(t, &countingBuilder{}, Options{}),
		Platform: func(ctx *fasthttp.RequestCtx) {
			got = EntryFor(ctx, "org_a", "x", "", "", nil).IP
		},
		PlatformRoutes: map[string][]string{"GET": {APIPrefix + "/health"}},
		OrgRoutes:      map[string][]string{},
		ClientIP:       func(*fasthttp.RequestCtx) string { return "203.0.113.7" },
	})
	if err != nil {
		t.Fatal(err)
	}
	var ctx fasthttp.RequestCtx
	ctx.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1}, nil)
	ctx.Request.SetRequestURI(APIPrefix + "/health")
	d.Handle(&ctx)
	if got != "203.0.113.7" {
		t.Fatalf("audit IP %q (status %d %s)", got, ctx.Response.StatusCode(), ctx.Response.Body())
	}
	var bare fasthttp.RequestCtx
	bare.Init(&fasthttp.Request{}, &net.TCPAddr{IP: net.ParseIP("10.0.0.2"), Port: 1}, nil)
	if ip := ClientIP(&bare); ip != "10.0.0.2" {
		t.Fatalf("fallback IP %q", ip)
	}
}

type recordAudit struct{ got []AuditEntry }

func (r *recordAudit) Audit(_ context.Context, e AuditEntry) error {
	r.got = append(r.got, e)
	return nil
}

// TestAuditCreatedID: a create route's generic audit row names the record
// it committed (4-B-09).
func TestAuditCreatedID(t *testing.T) {
	sink := &recordAudit{}
	d := &Dispatcher{c: DispatcherConfig{Audit: sink, Logger: schemas.NoopLogger{}}}
	var ctx fasthttp.RequestCtx
	SetCreated(&ctx, "rel_1")
	d.audit(&ctx, "org_a", "POST", APIPrefix+"/releases")
	var sub fasthttp.RequestCtx
	sub.SetUserValue("id", "e_1")
	SetCreated(&sub, "rv_1")
	d.audit(&sub, "org_a", "POST", APIPrefix+"/experiments/{id}/notes")
	if len(sink.got) != 2 {
		t.Fatalf("rows %+v", sink.got)
	}
	if e := sink.got[0]; e.Action != "releases.create" || e.TargetID != "rel_1" {
		t.Fatalf("collection create row %+v", e)
	}
	if e := sink.got[1]; e.TargetID != "e_1" || e.Details["created_id"] != "rv_1" {
		t.Fatalf("nested create row %+v", e)
	}
}
