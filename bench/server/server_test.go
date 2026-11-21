package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// testConfig is a recorded-mode config on loopback.
func testConfig() *lib.Config {
	return &lib.Config{RunsRoot: "runs", Server: lib.ServerConfig{Listen: "127.0.0.1:0", Mode: schemas.ModeRecorded}}
}

// testUI is a built UI with an index and one asset.
func testUI() fstest.MapFS {
	return fstest.MapFS{
		"index.html":          {Data: []byte("<!doctype html><title>bench</title>")},
		"assets/app-1.js":     {Data: []byte("console.log(1)")},
		".vite/manifest.json": {Data: []byte("{}")},
	}
}

// startServer serves s on an in-memory listener until the test ends.
func startServer(t *testing.T, s *Server) (*fasthttp.Client, *fasthttputil.InmemoryListener) {
	t.Helper()
	ln := fasthttputil.NewInmemoryListener()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("shutdown timed out")
		}
	})
	return &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}, ln
}

// get sends one request with a loopback Host.
func get(t *testing.T, c *fasthttp.Client, method, path, host string, body []byte, headers map[string]string) *fasthttp.Response {
	t.Helper()
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.SetRequestURI(path)
	req.Header.SetHost(host)
	req.Header.SetMethod(method)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.SetBody(body)
	}
	resp := &fasthttp.Response{}
	if err := c.DoTimeout(req, resp, 5*time.Second); err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func TestServerRoutes(t *testing.T) {
	t.Setenv(lib.WriteTokenEnv, "tok")
	s, err := New(testConfig(), Deps{UI: testUI(), Version: "v-test"})
	if err != nil {
		t.Fatal(err)
	}
	if s.WriteToken() != "tok" {
		t.Fatalf("token = %q", s.WriteToken())
	}
	c, _ := startServer(t, s)
	const host = "127.0.0.1:8090"
	tests := []struct {
		name, method, path, host string
		headers                  map[string]string
		status                   int
		contentType, contains    string
	}{
		{"health", "GET", "/api/bench/health", host, nil, 200, "application/json", `"mode":"recorded"`},
		{"post without token", "POST", "/api/bench/audit", host, nil, 401, "application/json", `"type":"unauthorized"`},
		{"post with token, no auditor", "POST", "/api/bench/audit", host, map[string]string{"Authorization": "Bearer tok"}, 503, "application/json", `"mode":"recorded"`},
		{"unknown api route", "GET", "/api/bench/nope", host, nil, 404, "application/json", `"type":"not_found"`},
		{"wrong method", "DELETE", "/api/bench/health", host, nil, 405, "application/json", `"mode":"recorded"`},
		{"spa root", "GET", "/", host, nil, 200, "text/html", "<title>bench</title>"},
		{"spa client route", "GET", "/experiments/e_1/verdict", host, nil, 200, "text/html", "<title>bench</title>"},
		{"asset", "GET", "/assets/app-1.js", host, nil, 200, "text/javascript", "console.log"},
		{"missing asset", "GET", "/assets/nope.js", host, nil, 404, "application/json", "not found"},
		{"hidden file", "GET", "/.vite/manifest.json", host, nil, 404, "application/json", "not found"},
		{"dns rebinding host", "GET", "/api/bench/health", "evil.example", nil, 403, "application/json", `"type":"forbidden"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := get(t, c, tt.method, tt.path, tt.host, []byte("x"), tt.headers)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
			if ct := string(resp.Header.ContentType()); !strings.HasPrefix(ct, tt.contentType) {
				t.Errorf("content type = %q, want %q", ct, tt.contentType)
			}
			if !strings.Contains(string(resp.Body()), tt.contains) {
				t.Errorf("body %q lacks %q", resp.Body(), tt.contains)
			}
			if csp := string(resp.Header.Peek("Content-Security-Policy")); !strings.HasPrefix(csp, "default-src 'self'") {
				t.Errorf("csp = %q", csp)
			}
		})
	}
}

func TestServerBodyLimit(t *testing.T) {
	t.Setenv(lib.WriteTokenEnv, "tok")
	s, err := New(testConfig(), Deps{})
	if err != nil {
		t.Fatal(err)
	}
	_, ln := startServer(t, s)
	conn, err := ln.Dial()
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// Headers only: the server must refuse from Content-Length alone.
	fmt.Fprintf(conn, "POST /api/bench/audit HTTP/1.1\r\nHost: 127.0.0.1\r\nAuthorization: Bearer tok\r\n"+
		"Content-Type: text/csv\r\nContent-Length: %d\r\n\r\n", lib.MaxBodyBytes+1)
	resp := &fasthttp.Response{}
	if err := resp.Read(bufio.NewReader(conn)); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode() != fasthttp.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", resp.StatusCode(), resp.Body())
	}
	var body lib.ErrorBody
	if err := json.Unmarshal(resp.Body(), &body); err != nil || body.Mode != schemas.ModeRecorded || body.Error.Type != lib.ErrTypeTooLarge {
		t.Fatalf("body = %s (%v)", resp.Body(), err)
	}
}

func TestNewValidatesConfig(t *testing.T) {
	if _, err := New(nil, Deps{}); err == nil {
		t.Error("nil config accepted")
	}
	bad := testConfig()
	bad.Server.Mode = "demo"
	if _, err := New(bad, Deps{}); err == nil {
		t.Error("bad mode accepted")
	}
}

func TestGeneratedTokenAnnouncedOnce(t *testing.T) {
	t.Setenv(lib.WriteTokenEnv, "")
	logger := &countLogger{}
	s, err := New(testConfig(), Deps{Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.WriteToken()) != 64 {
		t.Fatalf("token = %q", s.WriteToken())
	}
	startServer(t, s)
	time.Sleep(50 * time.Millisecond)
	if n := logger.count(s.WriteToken()); n != 1 {
		t.Fatalf("token logged %d times, want 1", n)
	}
}

func TestListenAndServeShutsDown(t *testing.T) {
	s, err := New(testConfig(), Deps{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no graceful shutdown")
	}
}

// countLogger records Info messages.
type countLogger struct {
	schemas.NoopLogger
	mu   sync.Mutex
	msgs []string
}

func (l *countLogger) Info(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, msg)
}

func (l *countLogger) count(sub string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, m := range l.msgs {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

// TestServeAlreadyCancelled covers QC2-20-05: a context that ends before
// the serving goroutine registers its listener still shuts down; Serve
// never waits forever on a listener the library did not know about.
func TestServeAlreadyCancelled(t *testing.T) {
	for i := 0; i < 20; i++ {
		s, err := New(testConfig(), Deps{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		done := make(chan error, 1)
		go func() { done <- s.Serve(ctx, fasthttputil.NewInmemoryListener()) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Serve = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Serve with a cancelled context did not return")
		}
	}
}
