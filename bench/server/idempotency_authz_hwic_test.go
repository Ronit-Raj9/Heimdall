package server

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// Fix wave 3 security (group C integration): the idempotency middleware runs
// inside the hosted dispatcher, after authentication and the tenancy
// authorization, so a stored answer is replayed only to a caller who still
// has access to the route in that organization.

// replayDir is a one-org directory whose membership the test changes.
type replayDir struct {
	mu      sync.Mutex
	org     tenancy.Org
	members map[string]auth.Role
}

func (d *replayDir) OrgByID(_ context.Context, id string) (tenancy.Org, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if id == d.org.ID {
		return d.org, nil
	}
	return tenancy.Org{}, tenancy.ErrOrgNotFound
}

func (d *replayDir) OrgBySlug(_ context.Context, slug string) (tenancy.Org, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if slug == d.org.Slug {
		return d.org, nil
	}
	return tenancy.Org{}, tenancy.ErrOrgNotFound
}

func (d *replayDir) MemberRole(_ context.Context, orgID, userID string) (auth.Role, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r, ok := d.members[orgID+"/"+userID]; ok {
		return r, nil
	}
	return "", tenancy.ErrNotMember
}

func (d *replayDir) set(userID string, r auth.Role) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r == "" {
		delete(d.members, d.org.ID+"/"+userID)
		return
	}
	d.members[d.org.ID+"/"+userID] = r
}

// replayAuth maps bearer credentials to principals; a revoked credential
// answers 401 like the hosted auth middleware does.
type replayAuth struct {
	mu      sync.Mutex
	creds   map[string]*auth.Principal
	revoked map[string]bool
}

func (a *replayAuth) middleware(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		raw := strings.TrimPrefix(string(ctx.Request.Header.Peek("Authorization")), "Bearer ")
		a.mu.Lock()
		p, ok := a.creds[raw]
		revoked := a.revoked[raw]
		a.mu.Unlock()
		if revoked {
			lib.SendError(ctx, "", fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "invalid or revoked token")
			return
		}
		if !ok {
			p = auth.Anonymous()
		}
		cp := *p
		auth.SetPrincipal(ctx, &cp)
		next(ctx)
	}
}

func (a *replayAuth) revoke(cred string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.revoked[cred] = true
}

type countAudit struct{ n atomic.Int32 }

func (c *countAudit) Audit(context.Context, tenancy.AuditEntry) error {
	c.n.Add(1)
	return nil
}

type replayFixture struct {
	handler fasthttp.RequestHandler
	dir     *replayDir
	auth    *replayAuth
	creates atomic.Int32
	audit   *countAudit
}

// newReplayFixture builds the real hosted chain over org1 (slug acme) whose
// org router answers every request with a new evaluation id. u1 is a member
// signed in with "sess-u1"; "hm_tok1" is a member API token of org1.
func newReplayFixture(t *testing.T, publicRead bool) *replayFixture {
	t.Helper()
	f := &replayFixture{
		dir: &replayDir{org: tenancy.Org{ID: "org1", Slug: "acme", PublicRead: publicRead}, members: map[string]auth.Role{"org1/u1": auth.RoleMember}},
		auth: &replayAuth{revoked: map[string]bool{}, creds: map[string]*auth.Principal{
			"sess-u1": {Kind: auth.KindUser, UserID: "u1", OrgID: "org1", Role: auth.RoleMember},
			"hm_tok1": {Kind: auth.KindToken, UserID: "u2", TokenID: "tok1", OrgID: "org1", Role: auth.RoleMember},
		}},
		audit: &countAudit{},
	}
	cfg := testConfig()
	cfg.Auth = lib.AuthConfig{Mode: lib.AuthModeHosted, PublicURL: "https://heimdall.test"}
	build := func(_ context.Context, org tenancy.Org, _ tenancy.OrgPaths) (*tenancy.OrgRuntime, error) {
		return &tenancy.OrgRuntime{Org: org, Handler: func(ctx *fasthttp.RequestCtx) {
			n := f.creates.Add(1)
			ctx.SetStatusCode(fasthttp.StatusAccepted)
			ctx.SetContentType("application/json")
			ctx.SetBodyString(fmt.Sprintf(`{"id":"ev_%d"}`, n))
		}}, nil
	}
	reg := tenancy.NewRegistry(tenancy.Layout{Hosted: true, StorageRoot: t.TempDir()}, build, tenancy.Options{})
	s, err := NewHosted(cfg, Hosted{Auth: f.auth.middleware, Directory: f.dir, Registry: reg, Audit: f.audit})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.closeHandlers)
	f.handler = s.Handler()
	return f
}

const replayKey = "replay-key-0123456789"

func (f *replayFixture) post(cred, key string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod("POST")
	ctx.Request.SetRequestURI("/api/bench/evaluations")
	ctx.Request.Header.SetHost("heimdall.test")
	ctx.Request.Header.SetContentType("application/json")
	ctx.Request.Header.Set("Authorization", "Bearer "+cred)
	ctx.Request.Header.Set(tenancy.OrgHeader, "acme")
	if key != "" {
		ctx.Request.Header.Set("Idempotency-Key", key)
	}
	ctx.Request.SetBodyString(`{"benchmarks":["longmemeval"]}`)
	f.handler(ctx)
	return ctx
}

// first sends the original request and checks it ran once.
func (f *replayFixture) first(t *testing.T, cred string) string {
	t.Helper()
	ctx := f.post(cred, replayKey)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted || f.creates.Load() != 1 {
		t.Fatalf("first request: %d %s (creates %d)", ctx.Response.StatusCode(), ctx.Response.Body(), f.creates.Load())
	}
	return string(ctx.Response.Body())
}

// refused checks a replay was refused with want and carried no stored body.
func (f *replayFixture) refused(t *testing.T, ctx *fasthttp.RequestCtx, want int, stored string) {
	t.Helper()
	if ctx.Response.StatusCode() != want {
		t.Fatalf("replay status %d, want %d: %s", ctx.Response.StatusCode(), want, ctx.Response.Body())
	}
	if b := string(ctx.Response.Body()); strings.Contains(b, stored) || strings.Contains(b, `"ev_`) {
		t.Fatalf("refused replay leaked the stored answer: %s", b)
	}
	if v := ctx.Response.Header.Peek("Idempotent-Replayed"); len(v) != 0 {
		t.Fatalf("refused replay marked replayed")
	}
	if n := f.creates.Load(); n != 1 {
		t.Fatalf("creates %d after a refused replay, want 1", n)
	}
}

func TestIdempotentReplayServedToMemberWithAccess(t *testing.T) {
	f := newReplayFixture(t, false)
	stored := f.first(t, "sess-u1")
	ctx := f.post("sess-u1", replayKey)
	if ctx.Response.StatusCode() != fasthttp.StatusAccepted || string(ctx.Response.Body()) != stored {
		t.Fatalf("replay: %d %s, want 202 %s", ctx.Response.StatusCode(), ctx.Response.Body(), stored)
	}
	if string(ctx.Response.Header.Peek("Idempotent-Replayed")) != "true" {
		t.Fatalf("replay not marked")
	}
	if n := f.creates.Load(); n != 1 {
		t.Fatalf("creates %d, want 1", n)
	}
	// The replay did no work, so it writes no second audit row.
	if n := f.audit.n.Load(); n != 1 {
		t.Fatalf("audit rows %d, want 1", n)
	}
}

func TestIdempotentReplayRefusedAfterMemberRemoved(t *testing.T) {
	// A public_read org: the removed member still resolves the org as a
	// reader, and the write is refused with 403.
	f := newReplayFixture(t, true)
	stored := f.first(t, "sess-u1")
	f.dir.set("u1", "")
	f.refused(t, f.post("sess-u1", replayKey), fasthttp.StatusForbidden, stored)

	// A private org hides itself from a non-member (404, the dispatcher's
	// answer for an org you cannot see); the stored body is never sent.
	g := newReplayFixture(t, false)
	stored = g.first(t, "sess-u1")
	g.dir.set("u1", "")
	g.refused(t, g.post("sess-u1", replayKey), fasthttp.StatusNotFound, stored)
}

func TestIdempotentReplayRefusedAfterRoleLowered(t *testing.T) {
	f := newReplayFixture(t, false)
	stored := f.first(t, "sess-u1")
	f.dir.set("u1", auth.RoleViewer) // POST /evaluations needs member
	f.refused(t, f.post("sess-u1", replayKey), fasthttp.StatusForbidden, stored)
}

func TestIdempotentReplayRefusedAfterTokenRevoked(t *testing.T) {
	f := newReplayFixture(t, false)
	stored := f.first(t, "hm_tok1")
	f.auth.revoke("hm_tok1")
	f.refused(t, f.post("hm_tok1", replayKey), fasthttp.StatusUnauthorized, stored)
}

func TestIdempotentReplayRefusedAfterSessionEnded(t *testing.T) {
	f := newReplayFixture(t, false)
	stored := f.first(t, "sess-u1")
	f.auth.mu.Lock()
	delete(f.auth.creds, "sess-u1") // signed out: the request is anonymous
	f.auth.mu.Unlock()
	f.refused(t, f.post("sess-u1", replayKey), fasthttp.StatusUnauthorized, stored)
}

// Eight identical requests at once through the hosted chain create one
// object; every caller gets the same answer.
func TestIdempotentHostedConcurrentDuplicatesCreateOne(t *testing.T) {
	f := newReplayFixture(t, false)
	var wg sync.WaitGroup
	bodies := make([]string, 8)
	codes := make([]int, 8)
	for i := range bodies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := f.post("sess-u1", replayKey)
			codes[i], bodies[i] = ctx.Response.StatusCode(), string(ctx.Response.Body())
		}(i)
	}
	wg.Wait()
	if n := f.creates.Load(); n != 1 {
		t.Fatalf("creates %d, want 1", n)
	}
	for i := range bodies {
		if codes[i] != fasthttp.StatusAccepted || bodies[i] != bodies[0] {
			t.Fatalf("request %d: %d %s, want 202 %s", i, codes[i], bodies[i], bodies[0])
		}
	}
}
