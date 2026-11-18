package handlers

import (
	"context"
	"fmt"
	"net"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// fakeCompliance builds a fixed pack for e_test and nothing else.
type fakeCompliance struct{}

func (fakeCompliance) CompliancePack(_ context.Context, expID string) (*schemas.CompliancePack, []byte, error) {
	if expID != "e_test" {
		return nil, nil, fmt.Errorf("experiment %s: %w", expID, schemas.ErrNotFound)
	}
	p := &schemas.CompliancePack{Version: "1.0.0", Experiments: []schemas.ComplianceExperiment{{ExpID: "e_test"}}, Tests: []schemas.ComplianceTest{}}
	return p, []byte("# Compliance evidence pack\n"), nil
}

func newComplianceClient(t *testing.T, src ComplianceSource) *testClient {
	t.Helper()
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, testMode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found")
	}
	NewComplianceHandler(testMode, src).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Shutdown(); err != nil {
			t.Errorf("shutdown: %v", err)
		}
	})
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestComplianceRoute(t *testing.T) {
	c := newComplianceClient(t, fakeCompliance{})
	p := APIPrefix + "/experiments/"
	cases := []struct {
		name, path, ctype, disposition, contains string
		status                                   int
	}{
		{"json default", p + "e_test/compliance", "application/json", `attachment; filename="compliance-e_test.json"`, `"exp_id":"e_test"`, 200},
		{"markdown", p + "e_test/compliance?format=md", "text/markdown", `attachment; filename="compliance-e_test.md"`, "# Compliance evidence pack", 200},
		{"bad format", p + "e_test/compliance?format=pdf", "", "", "format must be", 400},
		{"unknown experiment", p + "e_none/compliance", "", "", "not_found", 404},
		{"bad id", p + "..%2Fx/compliance", "", "", "not_found", 404},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := c.do(t, "GET", tc.path, nil, nil)
			if resp.StatusCode() != tc.status {
				t.Fatalf("status %d, want %d: %s", resp.StatusCode(), tc.status, resp.Body())
			}
			if !strings.Contains(string(resp.Body()), tc.contains) {
				t.Errorf("body %s lacks %q", resp.Body(), tc.contains)
			}
			if tc.ctype != "" && !strings.HasPrefix(string(resp.Header.ContentType()), tc.ctype) {
				t.Errorf("content type %q, want %q", resp.Header.ContentType(), tc.ctype)
			}
			if tc.disposition != "" && string(resp.Header.Peek("Content-Disposition")) != tc.disposition {
				t.Errorf("content disposition %q", resp.Header.Peek("Content-Disposition"))
			}
		})
	}
}

func TestComplianceUnavailable(t *testing.T) {
	c := newComplianceClient(t, nil)
	if resp := c.do(t, "GET", APIPrefix+"/experiments/e_test/compliance", nil, nil); resp.StatusCode() != 503 {
		t.Fatalf("status %d, want 503", resp.StatusCode())
	}
}
