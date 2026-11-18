package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/canary"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// fakeCanaries serves the fixture list and detail (generated from the
// canary package's scenario: a drifted canary on two providers).
type fakeCanaries struct {
	list    *canary.List
	detail  *canary.Detail
	busy    bool
	started []string
}

func loadCanaryFixture(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "canary", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func (f *fakeCanaries) List(context.Context) (*canary.List, error) { return f.list, nil }

func (f *fakeCanaries) Get(_ context.Context, name string) (*canary.Detail, error) {
	if name != f.detail.Summary.Name {
		return nil, fmt.Errorf("canary %q: %w", name, schemas.ErrNotFound)
	}
	return f.detail, nil
}

func (f *fakeCanaries) Start(_ context.Context, name string) (*canary.RunAccepted, error) {
	if name != f.detail.Summary.Name {
		return nil, fmt.Errorf("canary %q: %w", name, schemas.ErrNotFound)
	}
	if f.busy {
		return nil, fmt.Errorf("canary %s: %w", name, canary.ErrBusy)
	}
	f.started = append(f.started, name)
	return &canary.RunAccepted{Canary: name, Started: time.Date(2026, 11, 18, 6, 0, 0, 0, time.UTC), Message: "started"}, nil
}

func newCanaryClient(t *testing.T, src CanarySource) *testClient {
	t.Helper()
	write := lib.RequireWriteToken(testMode, testToken)
	common := []lib.Middleware{lib.BodyLimit(testMode, lib.MaxBodyBytes)}
	r := router.New()
	NewCanaryHandler(testMode, src, write).RegisterRoutes(r, common...)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestCanaryRoutesMatchContract(t *testing.T) {
	src := &fakeCanaries{list: &canary.List{}, detail: &canary.Detail{}}
	loadCanaryFixture(t, "list.json", src.list)
	loadCanaryFixture(t, "detail.json", src.detail)
	c := newCanaryClient(t, src)
	name := src.detail.Summary.Name
	tests := []struct {
		method, path string
		status       int
		typ          string
		nested       map[string]string
	}{
		{"GET", "/canaries", 200, "CanaryList", map[string]string{
			"canaries[]": "CanarySummary", "canaries[].targets[]": "CanaryTarget", "canaries[].baseline": "CanaryBaseline",
			"canaries[].baseline.served[]": "CanaryServed", "canaries[].last_look": "CanaryRecord", "errors[]": "CanaryDefinitionLoad",
		}},
		{"GET", "/canaries/" + name, 200, "CanaryDetail", map[string]string{
			"summary": "CanarySummary", "definition": "CanaryDefinition", "definition.budget": "CanaryBudget",
			"definition.schedule": "CanaryScheduleSpec", "baselines[]": "CanaryBaseline", "records[]": "CanaryRecord",
			"records[].comparison": "CanaryComparison", "records[].comparison.tests[]": "CanaryMetricTest",
			"records[].comparison.flips[]": "CanaryTaskFlip", "records[].changes": "CanaryChanges",
			"records[].changes.arms[]": "CanaryArmChanges", "records[].changes.new_clusters[]": "CanaryClusterRef",
			"records[].equivalence[]": "CanaryEquivalence", "records[].served[]": "CanaryServed", "records[].points[]": "CanaryArmPoint",
			"history[]": "TrendLineage", "history[].points[]": "TrendPoint", "pairs[]": "CanaryEquivalencePair",
		}},
		{"POST", "/canaries/" + name + "/run", 202, "CanaryRunAccepted", nil},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			var headers map[string]string
			if tt.method == "POST" {
				headers = authed("")
			}
			resp := c.do(t, tt.method, APIPrefix+tt.path, nil, headers)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
			body := decodeBody(t, resp)
			checkKeys(t, tt.typ, body, loadKeys(t, tt.typ), true)
			for path, typ := range tt.nested {
				for i, obj := range objectsAt(t, body, path) {
					checkKeys(t, fmt.Sprintf("%s.%s #%d", tt.typ, path, i), obj, loadKeys(t, typ), false)
				}
			}
		})
	}
	if len(src.started) != 1 {
		t.Errorf("started = %v", src.started)
	}
}

func TestCanaryRouteErrors(t *testing.T) {
	src := &fakeCanaries{list: &canary.List{}, detail: &canary.Detail{}}
	loadCanaryFixture(t, "detail.json", src.detail)
	c := newCanaryClient(t, src)
	name := src.detail.Summary.Name
	cases := []struct {
		method, path string
		headers      map[string]string
		status       int
	}{
		{"GET", "/canaries/missing", nil, 404},
		{"GET", "/canaries/Bad..Name", nil, 404},
		{"POST", "/canaries/" + name + "/run", nil, 401}, // no write token
		{"POST", "/canaries/missing/run", authed(""), 404},
	}
	for _, cc := range cases {
		if resp := c.do(t, cc.method, APIPrefix+cc.path, nil, cc.headers); resp.StatusCode() != cc.status {
			t.Errorf("%s %s = %d, want %d: %s", cc.method, cc.path, resp.StatusCode(), cc.status, resp.Body())
		}
	}
	src.busy = true
	if resp := c.do(t, "POST", APIPrefix+"/canaries/"+name+"/run", nil, authed("")); resp.StatusCode() != 409 {
		t.Errorf("busy run = %d, want 409", resp.StatusCode())
	}
	// No source: 503.
	nc := newCanaryClient(t, nil)
	if resp := nc.do(t, "GET", APIPrefix+"/canaries", nil, nil); resp.StatusCode() != 503 {
		t.Errorf("no source = %d, want 503", resp.StatusCode())
	}
}
