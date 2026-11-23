package tenancy

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// indexDir is fakeDir with a share index; it counts org lookups.
type indexDir struct {
	fakeDir
	tokens map[string][2]string // token hash -> org id, share id
	mu     *sync.Mutex
	opened *[]string
}

func (d indexDir) OrgByID(ctx context.Context, id string) (Org, error) {
	d.mu.Lock()
	*d.opened = append(*d.opened, id)
	d.mu.Unlock()
	return d.fakeDir.OrgByID(ctx, id)
}

func (d indexDir) ShareByToken(_ context.Context, token string) (string, string, error) {
	if v, ok := d.tokens[db.ShareTokenHash(token)]; ok {
		return v[0], v[1], nil
	}
	return "", "", ErrShareNotFound
}

const v2Token = "s_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"

func indexDispatcher(t *testing.T, sink AuditSink, opened *[]string) *Dispatcher {
	t.Helper()
	dir := indexDir{
		fakeDir: fakeDir{orgs: []Org{{ID: "org_a", Slug: "acme"}}},
		tokens:  map[string][2]string{db.ShareTokenHash(v2Token): {"org_a", "shr_1"}},
		mu:      &sync.Mutex{}, opened: opened,
	}
	d, err := NewDispatcher(DispatcherConfig{
		Mode: schemas.ModeLive, Registry: newTestRegistry(t, &countingBuilder{}, Options{}), Dir: dir,
		Platform:  func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(fasthttp.StatusOK) },
		OrgRoutes: map[string][]string{"GET": {APIPrefix + "/shared/{token}"}},
		Audit:     sink,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	d.now = func() time.Time { return now }
	return d
}

// TestShareV2ResolvesThroughIndex (fix wave 3, N293): an s_ token finds its
// org through the platform index; the audit row names the share id, never
// the token; an unknown s_ token is 404 without opening any org; the old
// org.id.signature form still resolves.
func TestShareV2ResolvesThroughIndex(t *testing.T) {
	var opened []string
	sink := &recordAudit{}
	d := indexDispatcher(t, sink, &opened)
	anon := auth.Anonymous()
	ctx := serveFrom(d, anon, "198.51.100.1", APIPrefix+"/shared/"+v2Token)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("v2 token: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if o, ok := OrgFrom(ctx); !ok || o.ID != "org_a" {
		t.Fatalf("org %+v", o)
	}
	if len(sink.got) != 1 || sink.got[0].TargetID != "shr_1" || strings.Contains(sink.got[0].TargetID, "s_") {
		t.Fatalf("audit %+v", sink.got)
	}
	opened = nil
	ctx = serveFrom(d, anon, "198.51.100.1", APIPrefix+"/shared/s_unknownunknownunknownunknownunknownunknown1")
	if ctx.Response.StatusCode() != fasthttp.StatusNotFound || !strings.Contains(string(ctx.Response.Body()), "this share link does not exist") {
		t.Fatalf("unknown v2: %d %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	if len(opened) != 0 {
		t.Fatalf("unknown v2 token opened orgs %v", opened)
	}
	if c := serveFrom(d, anon, "198.51.100.1", APIPrefix+"/shared/org_a.shr_2.sig").Response.StatusCode(); c != fasthttp.StatusOK {
		t.Fatalf("old token: %d", c)
	}
	if k := shareLinkID(v2Token); k == v2Token || !strings.HasPrefix(k, "s_") || len(k) != 18 {
		t.Fatalf("v2 bucket key %q", k)
	}
}

// TestShareV2WithoutIndex404: a directory without a share index (local
// mode) answers 404 for an s_ token.
func TestShareV2WithoutIndex404(t *testing.T) {
	_, _, err := Resolve(context.Background(), fakeDir{orgs: []Org{{ID: "org_a"}}}, Rule{Access: AccessShare}, nil, v2Token, "")
	if d, ok := err.(*Denied); !ok || d.Status != 404 {
		t.Fatalf("v2 without index: %v", err)
	}
}
