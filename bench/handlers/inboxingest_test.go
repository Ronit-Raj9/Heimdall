package handlers

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/inboxtoken"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

type ingestDir map[string]tenancy.Org

func (d ingestDir) OrgByID(_ context.Context, id string) (tenancy.Org, error) {
	if o, ok := d[id]; ok {
		return o, nil
	}
	return tenancy.Org{}, tenancy.ErrOrgNotFound
}

func (d ingestDir) OrgBySlug(_ context.Context, slug string) (tenancy.Org, error) {
	for _, o := range d {
		if o.Slug == slug {
			return o, nil
		}
	}
	return tenancy.Org{}, tenancy.ErrOrgNotFound
}

func (d ingestDir) MemberRole(context.Context, string, string) (auth.Role, error) {
	return "", tenancy.ErrNotMember
}

type ingestFixture struct {
	r     *router.Router
	roots map[string]string
}

func newIngestFixture(t *testing.T) *ingestFixture {
	t.Helper()
	f := &ingestFixture{roots: map[string]string{"org_a": t.TempDir(), "org_b": t.TempDir()}}
	dir := ingestDir{"org_a": {ID: "org_a", Slug: "acme"}, "org_b": {ID: "org_b", Slug: "beta"}}
	h := NewInboxTokensHandler(schemas.ModeLive, inboxtoken.NewMemory(), "", dir, func(_ context.Context, o tenancy.Org) (*traceinbox.Inbox, *traceinbox.Source, error) {
		return f.inbox(o.ID), &traceinbox.Source{Name: "webhook", Kind: traceinbox.KindWebhook}, nil
	})
	f.r = router.New()
	h.RegisterRoutes(f.r)
	return f
}

func (f *ingestFixture) inbox(orgID string) *traceinbox.Inbox {
	root := f.roots[orgID]
	return traceinbox.New(filepath.Join(root, traceinbox.Dir), &traceinbox.Config{}, filepath.Join(root, "benchmarks"))
}

func (f *ingestFixture) items(t *testing.T, orgID string) int {
	t.Helper()
	l, err := f.inbox(orgID).List(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	return len(l)
}

// do sends one request; org and role set the dispatcher's resolution (empty
// org: none, as for the public ingest route).
func (f *ingestFixture) do(method, path, org string, role auth.Role, bearer, body string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.SetRequestURI(path)
	ctx.Request.Header.SetMethod(method)
	ctx.Request.Header.SetHost("heimdall.example")
	if bearer != "" {
		ctx.Request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		ctx.Request.SetBodyString(body)
	}
	if org != "" {
		tenancy.SetOrg(ctx, &tenancy.Org{ID: org})
		auth.SetPrincipal(ctx, &auth.Principal{Kind: auth.KindUser, UserID: "u1", OrgID: org, Role: role})
	} else {
		auth.SetPrincipal(ctx, auth.Anonymous())
	}
	f.r.Handler(ctx)
	return ctx
}

func orgPushBody(t *testing.T) string {
	t.Helper()
	conv := map[string]any{"id": "t-1", "model": "gpt-x", "messages": []any{
		map[string]any{"role": "user", "content": "Refund order A-1"},
		map[string]any{"role": "assistant", "content": "Your refund was issued."},
	}}
	b, _ := json.Marshal(map[string]any{"trace": conv, "format": "chat", "reason": "thumbs down"})
	return string(b)
}

func TestInboxTokensHostedIngest(t *testing.T) {
	f := newIngestFixture(t)

	// A viewer reads the list but cannot create.
	if c := f.do("POST", InboxTokensPath, "org_a", auth.RoleViewer, "", `{"label":"ci"}`); c.Response.StatusCode() != 403 {
		t.Fatalf("viewer create: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	c := f.do("POST", InboxTokensPath, "org_a", auth.RoleAdmin, "", `{"label":"ci"}`)
	if c.Response.StatusCode() != 201 {
		t.Fatalf("admin create: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	var created InboxTokenCreated
	if err := json.Unmarshal(c.Response.Body(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Secret, inboxtoken.Prefix) || created.URL != "http://heimdall.example/api/bench/orgs/org_a/inbox/ingest" {
		t.Fatalf("created %+v", created)
	}
	c = f.do("GET", InboxTokensPath, "org_a", auth.RoleViewer, "", "")
	if c.Response.StatusCode() != 200 || strings.Contains(string(c.Response.Body()), created.Secret) {
		t.Fatalf("viewer list: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	var list InboxTokenList
	_ = json.Unmarshal(c.Response.Body(), &list)
	if len(list.Tokens) != 1 || list.Tokens[0].Last4 != created.Secret[len(created.Secret)-4:] {
		t.Fatalf("list %+v", list)
	}
	if c = f.do("GET", InboxTokensPath, "org_b", auth.RoleAdmin, "", ""); strings.Contains(string(c.Response.Body()), created.Token.ID) {
		t.Fatal("org_b lists org_a's token")
	}

	// The token ingests into its own org only.
	body := orgPushBody(t)
	if c = f.do("POST", InboxOrgIngestURL("org_a"), "", "", created.Secret, body); c.Response.StatusCode() != 201 {
		t.Fatalf("ingest: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	if f.items(t, "org_a") != 1 || f.items(t, "org_b") != 0 {
		t.Fatalf("items a=%d b=%d", f.items(t, "org_a"), f.items(t, "org_b"))
	}
	// By slug too.
	if c = f.do("POST", InboxOrgIngestURL("acme"), "", "", created.Secret, body); c.Response.StatusCode() >= 300 {
		t.Fatalf("ingest by slug: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	for name, tc := range map[string]struct{ org, tok string }{
		"wrong org":   {"org_b", created.Secret},
		"unknown org": {"nope", created.Secret},
		"bad token":   {"org_a", created.Secret + "x"},
		"no token":    {"org_a", ""},
	} {
		if c = f.do("POST", InboxOrgIngestURL(tc.org), "", "", tc.tok, body); c.Response.StatusCode() != 401 {
			t.Fatalf("%s: %d %s", name, c.Response.StatusCode(), c.Response.Body())
		}
	}
	if f.items(t, "org_b") != 0 {
		t.Fatal("org_b got an item")
	}
	// Oversized body: 413.
	if c = f.do("POST", InboxOrgIngestURL("org_a"), "", "", created.Secret, strings.Repeat("x", int(traceinbox.DefaultMaxBody)+1)); c.Response.StatusCode() != 413 {
		t.Fatalf("oversize: %d", c.Response.StatusCode())
	}

	// A viewer cannot revoke; an admin can, and the token stops working.
	if c = f.do("DELETE", InboxTokensPath+"/"+created.Token.ID, "org_a", auth.RoleViewer, "", ""); c.Response.StatusCode() != 403 {
		t.Fatalf("viewer revoke: %d", c.Response.StatusCode())
	}
	if c = f.do("DELETE", InboxTokensPath+"/"+created.Token.ID, "org_b", auth.RoleAdmin, "", ""); c.Response.StatusCode() != 404 {
		t.Fatalf("other org revoke: %d", c.Response.StatusCode())
	}
	if c = f.do("DELETE", InboxTokensPath+"/"+created.Token.ID, "org_a", auth.RoleAdmin, "", ""); c.Response.StatusCode() != 204 {
		t.Fatalf("revoke: %d %s", c.Response.StatusCode(), c.Response.Body())
	}
	if c = f.do("POST", InboxOrgIngestURL("org_a"), "", "", created.Secret, body); c.Response.StatusCode() != 401 {
		t.Fatalf("revoked: %d", c.Response.StatusCode())
	}
}

func TestInboxTokensRateLimit(t *testing.T) {
	f := newIngestFixture(t)
	c := f.do("POST", InboxTokensPath, "org_a", auth.RoleAdmin, "", "")
	var created InboxTokenCreated
	_ = json.Unmarshal(c.Response.Body(), &created)
	h := NewInboxTokensHandler(schemas.ModeLive, nil, "", nil, nil)
	if h.RatePerMinute != traceinbox.DefaultRate {
		t.Fatal("default rate")
	}
	g := h.gate("org_a")
	for i := 0; i < traceinbox.DefaultRate; i++ {
		g.Allow(h.now())
	}
	if g.Allow(h.now()) {
		t.Fatal("rate limit not applied")
	}
	if !h.gate("org_b").Allow(h.now()) {
		t.Fatal("org_b limited by org_a")
	}
}
