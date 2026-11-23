package idempotency

import (
	"bufio"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

const testKey = "abcdefghijklmnop0123"

// creator is a fake create route: every call that runs makes a new object
// and answers 202 with its id, unless the body asks for a refusal.
type creator struct {
	n     atomic.Int64
	delay time.Duration
}

func (c *creator) handle(ctx *fasthttp.RequestCtx) {
	if c.delay > 0 {
		time.Sleep(c.delay)
	}
	if strings.Contains(string(ctx.PostBody()), "refuse") {
		ctx.SetStatusCode(fasthttp.StatusBadRequest)
		ctx.SetBodyString(`{"error":"refused"}`)
		return
	}
	id := c.n.Add(1)
	ctx.SetStatusCode(fasthttp.StatusAccepted)
	ctx.SetContentType("application/json")
	ctx.SetBodyString(fmt.Sprintf(`{"id":"ev_%d"}`, id))
}

func userP(id, org string) *auth.Principal {
	return &auth.Principal{Kind: auth.KindUser, UserID: id, OrgID: org, Role: auth.RoleOwner}
}

func call(h fasthttp.RequestHandler, p *auth.Principal, method, path, key, body string, headers ...string) *fasthttp.RequestCtx {
	ctx := &fasthttp.RequestCtx{}
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(path)
	ctx.Request.Header.SetContentType("application/json")
	if key != "" {
		ctx.Request.Header.Set(Header, key)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		ctx.Request.Header.Set(headers[i], headers[i+1])
	}
	ctx.Request.SetBodyString(body)
	if p != nil {
		auth.SetPrincipal(ctx, p)
	}
	h(ctx)
	return ctx
}

func newMW(c *creator) (fasthttp.RequestHandler, *Store) {
	st := NewStore()
	return Middleware(schemas.ModeLive, st)(c.handle), st
}

func TestIdempotentCreateReturnsSameEvaluation(t *testing.T) {
	c := &creator{}
	h, _ := newMW(c)
	p := userP("u1", "org1")
	a := call(h, p, "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`)
	b := call(h, p, "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`)
	if c.n.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", c.n.Load())
	}
	if a.Response.StatusCode() != 202 || b.Response.StatusCode() != 202 {
		t.Fatalf("status %d / %d, want 202", a.Response.StatusCode(), b.Response.StatusCode())
	}
	if string(a.Response.Body()) != string(b.Response.Body()) {
		t.Fatalf("replayed body %s, want %s", b.Response.Body(), a.Response.Body())
	}
	if string(b.Response.Header.Peek(ReplayedHeader)) != "true" {
		t.Fatalf("replay has no %s header", ReplayedHeader)
	}
	if string(a.Response.Header.Peek(ReplayedHeader)) != "" {
		t.Fatal("first answer marked as replayed")
	}
	if ct := string(b.Response.Header.ContentType()); ct != "application/json" {
		t.Fatalf("replayed content type %q", ct)
	}
}

func TestIdempotentConcurrentDuplicateWaits(t *testing.T) {
	c := &creator{delay: 50 * time.Millisecond}
	h, _ := newMW(c)
	p := userP("u1", "org1")
	var wg sync.WaitGroup
	bodies := make([]string, 8)
	for i := range bodies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := call(h, p, "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`)
			bodies[i] = fmt.Sprintf("%d %s", ctx.Response.StatusCode(), ctx.Response.Body())
		}(i)
	}
	wg.Wait()
	if c.n.Load() != 1 {
		t.Fatalf("handler ran %d times, want 1", c.n.Load())
	}
	for _, b := range bodies {
		if b != bodies[0] || !strings.HasPrefix(b, "202 ") {
			t.Fatalf("answers differ: %q", bodies)
		}
	}
}

func TestIdempotentPendingTooLongAnswers409(t *testing.T) {
	c := &creator{delay: 300 * time.Millisecond}
	st := NewStore()
	st.wait = 20 * time.Millisecond
	h := Middleware(schemas.ModeLive, st)(c.handle)
	p := userP("u1", "org1")
	done := make(chan struct{})
	go func() {
		call(h, p, "POST", "/api/bench/evaluations", testKey, `{}`)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	ctx := call(h, p, "POST", "/api/bench/evaluations", testKey, `{}`)
	<-done
	if ctx.Response.StatusCode() != fasthttp.StatusConflict || !strings.Contains(string(ctx.Response.Body()), "still being processed") {
		t.Fatalf("got %d %s, want 409 still being processed", ctx.Response.StatusCode(), ctx.Response.Body())
	}
}

func TestIdempotentKeyReusedWithOtherBody(t *testing.T) {
	c := &creator{}
	h, _ := newMW(c)
	p := userP("u1", "org1")
	call(h, p, "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`)
	ctx := call(h, p, "POST", "/api/bench/evaluations", testKey, `{"name":"y"}`)
	if ctx.Response.StatusCode() != fasthttp.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", ctx.Response.StatusCode())
	}
	if !strings.Contains(string(ctx.Response.Body()), "already used for a different request") {
		t.Fatalf("body %s", ctx.Response.Body())
	}
	// The same key on another path of the same caller is another request too.
	ctx = call(h, p, "POST", "/api/bench/quickrun", testKey, `{"name":"x"}`)
	if c.n.Load() != 2 || ctx.Response.StatusCode() != 202 {
		t.Fatalf("other path: ran %d, status %d", c.n.Load(), ctx.Response.StatusCode())
	}
}

func TestIdempotentFailedRequestCanRetry(t *testing.T) {
	c := &creator{}
	h, _ := newMW(c)
	p := userP("u1", "org1")
	a := call(h, p, "POST", "/api/bench/evaluations", testKey, `{"refuse":true}`)
	if a.Response.StatusCode() != 400 {
		t.Fatalf("first status %d", a.Response.StatusCode())
	}
	// A corrected body with the same key runs: the failure was not kept.
	b := call(h, p, "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`)
	if b.Response.StatusCode() != 202 || c.n.Load() != 1 {
		t.Fatalf("retry status %d, ran %d", b.Response.StatusCode(), c.n.Load())
	}
	if string(b.Response.Header.Peek(ReplayedHeader)) != "" {
		t.Fatal("retry after a failure was replayed")
	}
}

func TestIdempotentKeyIsOrgScoped(t *testing.T) {
	c := &creator{}
	h, _ := newMW(c)
	a := call(h, userP("u1", "org1"), "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`)
	// Another user in the same org with the same key and body: never the first user's answer.
	b := call(h, userP("u2", "org1"), "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`)
	// The same user acting in another org (session org).
	d := call(h, userP("u1", "org2"), "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`)
	// The same user naming another org through the header.
	e := call(h, userP("u1", "org1"), "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`, "X-Heimdall-Org", "org3")
	// An API token of the same user is another caller.
	tok := &auth.Principal{Kind: auth.KindToken, UserID: "u1", TokenID: "t1", OrgID: "org1"}
	f := call(h, tok, "POST", "/api/bench/evaluations", testKey, `{"name":"x"}`)
	if c.n.Load() != 5 {
		t.Fatalf("handler ran %d times, want 5", c.n.Load())
	}
	seen := map[string]bool{}
	for _, ctx := range []*fasthttp.RequestCtx{a, b, d, e, f} {
		if string(ctx.Response.Header.Peek(ReplayedHeader)) != "" {
			t.Fatal("an answer was replayed across callers or orgs")
		}
		seen[string(ctx.Response.Body())] = true
	}
	if len(seen) != 5 {
		t.Fatalf("answers shared across scopes: %v", seen)
	}
}

func TestIdempotencyKeyFormat(t *testing.T) {
	c := &creator{}
	h, _ := newMW(c)
	p := userP("u1", "org1")
	for _, k := range []string{"short", strings.Repeat("a", 65), "abcdefghijklmnop!", "abcdefgh ijklmnop"} {
		ctx := call(h, p, "POST", "/api/bench/evaluations", k, `{}`)
		if ctx.Response.StatusCode() != 400 || !strings.Contains(string(ctx.Response.Body()), "Idempotency-Key must be 16 to 64 letters") {
			t.Fatalf("key %q: %d %s", k, ctx.Response.StatusCode(), ctx.Response.Body())
		}
	}
	if c.n.Load() != 0 {
		t.Fatal("a bad key reached the handler")
	}
	for _, k := range []string{strings.Repeat("a", 16), strings.Repeat("Z", 64), "abc-def_ghi-jkl_mno"} {
		if ctx := call(h, p, "POST", "/api/bench/evaluations", k, `{}`); ctx.Response.StatusCode() != 202 {
			t.Fatalf("key %q refused: %d", k, ctx.Response.StatusCode())
		}
	}
}

func TestIdempotencyPassThroughWithoutKey(t *testing.T) {
	c := &creator{}
	h, _ := newMW(c)
	p := userP("u1", "org1")
	call(h, p, "POST", "/api/bench/evaluations", "", `{}`)
	call(h, p, "POST", "/api/bench/evaluations", "", `{}`)
	// Anonymous callers, GETs and non-API paths are never keyed.
	call(h, nil, "POST", "/api/bench/evaluations", testKey, `{}`)
	call(h, auth.Anonymous(), "POST", "/api/bench/evaluations", testKey, `{}`)
	call(h, p, "GET", "/api/bench/evaluations", testKey, ``)
	call(h, p, "GET", "/api/bench/evaluations", testKey, ``)
	call(h, p, "POST", "/login", testKey, `{}`)
	call(h, p, "POST", "/login", testKey, `{}`)
	if c.n.Load() != 8 {
		t.Fatalf("handler ran %d times, want 8", c.n.Load())
	}
}

func TestIdempotencySkipsUploadsAndStreams(t *testing.T) {
	c := &creator{}
	h, st := newMW(c)
	p := userP("u1", "org1")
	big := strings.Repeat("x", maxRequestBytes+1)
	call(h, p, "POST", "/api/bench/uploads", testKey, big)
	call(h, p, "POST", "/api/bench/uploads", testKey, big)
	mp := func() {
		call(h, p, "POST", "/api/bench/files", testKey, "--b\r\n", "Content-Type", "multipart/form-data; boundary=b")
	}
	mp()
	mp()
	if c.n.Load() != 4 {
		t.Fatalf("handler ran %d times, want 4 (uploads pass through)", c.n.Load())
	}
	// A streamed answer is never read or kept.
	var streams atomic.Int64
	sh := Middleware(schemas.ModeLive, st)(func(ctx *fasthttp.RequestCtx) {
		streams.Add(1)
		ctx.SetStatusCode(200)
		ctx.SetBodyStreamWriter(func(w *bufio.Writer) { _, _ = w.WriteString("data: x\n\n") })
	})
	call(sh, p, "POST", "/api/bench/stream", testKey, `{}`)
	call(sh, p, "POST", "/api/bench/stream", testKey, `{}`)
	if streams.Load() != 2 {
		t.Fatalf("streamed route ran %d times, want 2", streams.Load())
	}
	if st.Len() != 0 {
		t.Fatalf("store holds %d entries, want 0", st.Len())
	}
}

func TestIdempotencyStoreBounds(t *testing.T) {
	c := &creator{}
	st := NewStore()
	st.maxEntries = 3
	now := time.Now()
	st.now = func() time.Time { return now }
	h := Middleware(schemas.ModeLive, st)(c.handle)
	p := userP("u1", "org1")
	for i := 0; i < 5; i++ {
		call(h, p, "POST", "/api/bench/evaluations", fmt.Sprintf("key-%016d", i), `{}`)
	}
	if st.Len() != 3 {
		t.Fatalf("store holds %d, want 3", st.Len())
	}
	// The oldest was dropped: key 0 runs again.
	call(h, p, "POST", "/api/bench/evaluations", fmt.Sprintf("key-%016d", 0), `{}`)
	if c.n.Load() != 6 {
		t.Fatalf("ran %d, want 6", c.n.Load())
	}
	// After 24 hours a key runs again.
	now = now.Add(25 * time.Hour)
	call(h, p, "POST", "/api/bench/evaluations", fmt.Sprintf("key-%016d", 4), `{}`)
	if c.n.Load() != 7 {
		t.Fatalf("ran %d, want 7 after expiry", c.n.Load())
	}
}
