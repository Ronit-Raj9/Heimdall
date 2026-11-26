package handlers

import (
	"net"
	"slices"
	"testing"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"

	"github.com/maximhq/bifrost/bench/lib"
)

// newHiddenSuitesClient serves GET /suites and GET /suites/{id} with the
// suites hide reports left out, the way a hosted server hides coding-core.
func newHiddenSuitesClient(t *testing.T, suites SuiteSource, hide func(id string) bool) *testClient {
	t.Helper()
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, testMode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found")
	}
	NewSuitesHandler(testMode, suites).WithHiddenSuites(hide).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

// TestSuitesListReservedIDs: a hidden practice suite is named in
// reserved_ids, so the Add benchmark form can refuse it before sending (N46).
func TestSuitesListReservedIDs(t *testing.T) {
	c := newHiddenSuitesClient(t, testSuites(t), func(id string) bool { return id == "coding-core" })
	for _, q := range []string{"", "?fields=light"} {
		resp := c.do(t, "GET", APIPrefix+"/suites"+q, nil, nil)
		if resp.StatusCode() != 200 {
			t.Fatalf("list%s status %d: %s", q, resp.StatusCode(), resp.Body())
		}
		got, _ := decodeBody(t, resp)["reserved_ids"].([]any)
		if len(got) != 1 || got[0] != "coding-core" {
			t.Fatalf("list%s reserved_ids = %v, want [coding-core]", q, got)
		}
	}
	// Nothing hidden: no reserved_ids key at all (omitempty).
	plain := decodeBody(t, newSuitesClient(t, testSuites(t), nil, nil).do(t, "GET", APIPrefix+"/suites", nil, nil))
	if _, ok := plain["reserved_ids"]; ok {
		t.Fatalf("reserved_ids sent with nothing hidden: %v", plain["reserved_ids"])
	}
}

// TestGetOneSuiteHidden: a suite the list hides is 404 on the single suite
// route too, and never listed by the light list.
func TestGetOneSuiteHidden(t *testing.T) {
	c := newHiddenSuitesClient(t, testSuites(t), func(id string) bool { return id == "canary" })
	if resp := c.do(t, "GET", APIPrefix+"/suites/canary", nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("hidden suite status %d: %s", resp.StatusCode(), resp.Body())
	}
	var ids []string
	for _, s := range objectsAt(t, decodeBody(t, c.do(t, "GET", APIPrefix+"/suites?fields=light", nil, nil)), "suites[]") {
		ids = append(ids, s["id"].(string))
	}
	if slices.Contains(ids, "canary") {
		t.Fatalf("light list carries the hidden suite: %v", ids)
	}
}
