package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/valyala/fasthttp"
)

// serveReadiness answers one request through a router holding only the
// readiness routes.
func serveReadiness(t *testing.T, h *ReadinessHandler, method, path string) (int, Readiness) {
	t.Helper()
	r := router.New()
	h.RegisterRoutes(r)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(path)
	r.Handler(&ctx)
	var out Readiness
	if method == fasthttp.MethodGet {
		if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, ctx.Response.Body())
		}
	}
	if cc := string(ctx.Response.Header.Peek("Cache-Control")); cc != "no-store" {
		t.Errorf("%s Cache-Control = %q, want no-store", path, cc)
	}
	return ctx.Response.StatusCode(), out
}

func TestHealthzAlwaysOK(t *testing.T) {
	failing := []ReadyCheck{{Name: "database", Check: func(context.Context) error { return errors.New("down") }}}
	h := NewReadinessHandler(testMode, "test", failing)
	if code, out := serveReadiness(t, h, "GET", HealthzPath); code != 200 || out.Status != "ok" || out.Version != "test" {
		t.Fatalf("healthz = %d %+v", code, out)
	}
	if code, _ := serveReadiness(t, h, "HEAD", HealthzPath); code != 200 {
		t.Fatalf("HEAD healthz = %d", code)
	}
}

func TestReadyz(t *testing.T) {
	ok := func(context.Context) error { return nil }
	tests := []struct {
		name   string
		checks []ReadyCheck
		code   int
		status string
	}{
		{"no checks", nil, 200, "ready"},
		{"all pass", []ReadyCheck{{Name: "database", Check: ok}, RunsRootWritable(t.TempDir())}, 200, "ready"},
		{"one fails", []ReadyCheck{{Name: "database", Check: ok}, {Name: "migrations", Check: func(context.Context) error { return errors.New("2 pending") }}}, 503, "not_ready"},
		{"nil check", []ReadyCheck{{Name: "database"}}, 503, "not_ready"},
		{"panic", []ReadyCheck{{Name: "database", Check: func(context.Context) error { panic("boom") }}}, 503, "not_ready"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out := serveReadiness(t, NewReadinessHandler(testMode, "test", tt.checks), "GET", ReadyzPath)
			if code != tt.code || out.Status != tt.status || len(out.Checks) != len(tt.checks) {
				t.Fatalf("readyz = %d %+v, want %d %s", code, out, tt.code, tt.status)
			}
			for i, c := range out.Checks {
				if c.Name != tt.checks[i].Name || c.OK != (c.Error == "") {
					t.Errorf("check %d = %+v", i, c)
				}
			}
		})
	}
}

func TestRunReadyChecksDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	slow := ReadyCheck{Name: "slow", Check: func(context.Context) error { time.Sleep(time.Second); return nil }}
	start := time.Now()
	out := RunReadyChecks(ctx, []ReadyCheck{slow})
	if out[0].OK || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("slow check = %+v after %s", out[0], time.Since(start))
	}
}

func TestRunsRootWritable(t *testing.T) {
	dir := t.TempDir()
	if err := RunsRootWritable(dir).Check(context.Background()); err != nil {
		t.Fatalf("writable dir: %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, ".readyz-*")); len(left) != 0 {
		t.Fatalf("probe files left behind: %v", left)
	}
	if err := RunsRootWritable(filepath.Join(dir, "missing")).Check(context.Background()); err == nil {
		t.Fatal("missing dir passed")
	}
	if err := RunsRootWritable("").Check(context.Background()); err == nil {
		t.Fatal("empty dir passed")
	}
	if os.Geteuid() != 0 {
		ro := filepath.Join(dir, "ro")
		if err := os.Mkdir(ro, 0o500); err != nil {
			t.Fatal(err)
		}
		if err := RunsRootWritable(ro).Check(context.Background()); err == nil {
			t.Fatal("read-only dir passed")
		}
	}
}
