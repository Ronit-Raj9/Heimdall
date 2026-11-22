package main

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// TestHostedAnonymousMeButBenchRoutes401 covers the G1.console change on the
// real hosted server with the real auth middleware: a caller with no
// credential gets 200 and kind anonymous from GET /auth/me (no org, no CSRF
// value), while the org routes under /api/bench still answer 401, with no
// credential and with a session cookie that names no live session. The stale
// cookie also keeps its 401 on /auth/me, which the UI reads as signed out.
func TestHostedAnonymousMeButBenchRoutes401(t *testing.T) {
	f := newHostedFixture(t)
	f.env.authMW = nil // the shipped auth.Middleware, not the fixture's X-Test-As stand-in
	srv, err := newHostedServer(f.env)
	if err != nil {
		t.Fatal(err)
	}
	ln := fasthttputil.NewInmemoryListener()
	sctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(sctx, ln) }()
	t.Cleanup(func() {
		cancel()
		_ = ln.Close()
		<-done
	})
	client := &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}
	get := func(path, cookie string) (int, string) {
		t.Helper()
		req := fasthttp.AcquireRequest()
		defer fasthttp.ReleaseRequest(req)
		req.Header.SetMethod("GET")
		req.SetRequestURI("http://127.0.0.1" + path)
		req.Header.SetHost("127.0.0.1")
		if cookie != "" {
			req.Header.SetCookie(auth.SessionCookie, cookie)
		}
		res := &fasthttp.Response{}
		if err := client.DoTimeout(req, res, 30*time.Second); err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		return res.StatusCode(), string(res.Body())
	}

	status, body := get("/api/bench/auth/me", "")
	if status != 200 {
		t.Fatalf("anonymous GET /auth/me = %d %s, want 200", status, trim(body))
	}
	var me auth.Me
	if err := json.Unmarshal([]byte(body), &me); err != nil {
		t.Fatal(err)
	}
	if me.AuthMode != "hosted" || me.Kind != "anonymous" || me.CurrentOrg != "" || me.CSRF != "" || me.Role != "" || len(me.Orgs) != 0 || me.User != nil {
		t.Fatalf("anonymous me carries more than kind anonymous: %s", trim(body))
	}
	if status, body := get("/api/bench/auth/me", "not-a-session"); status != 401 {
		t.Fatalf("stale cookie GET /auth/me = %d %s, want 401", status, trim(body))
	}
	for _, path := range []string{"/api/bench/experiments", "/api/bench/suites", "/api/bench/canaries", "/api/bench/schedules", "/api/bench/runs"} {
		for _, cookie := range []string{"", "not-a-session"} {
			if status, body := get(path, cookie); status != 401 {
				t.Errorf("anonymous GET %s (cookie %q) = %d %s, want 401", path, cookie, status, trim(body))
			}
		}
	}
}
