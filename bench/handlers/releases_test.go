package handlers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// refusedErr stands in for the release package's refusal.
type refusedErr struct{ reason string }

func (e refusedErr) Error() string        { return "release refused: " + e.reason }
func (e refusedErr) ReleaseRefused() bool { return true }

// fakeReleases is an in-memory ReleaseLog: e_hold refuses, e_boom fails.
type fakeReleases struct {
	mu   sync.Mutex
	recs []schemas.ReleaseRecord
}

func (f *fakeReleases) Create(_ context.Context, req *schemas.ReleaseRequest) (*schemas.ReleaseRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch req.ExpID {
	case "e_hold":
		return nil, fmt.Errorf("create: %w", refusedErr{"verdict action is hold, not ship"})
	case "e_none":
		return nil, fmt.Errorf("load manifest e_none: %w", schemas.ErrNotFound)
	case "e_boom":
		return nil, errors.New("disk on fire")
	}
	rec := schemas.ReleaseRecord{
		ReleaseID: fmt.Sprintf("rel_%d", len(f.recs)+1), ExpID: req.ExpID, AnalysisID: "a_1", Arm: "B",
		AgentConfigID: "agent_b", ConfigHash: "sha256:cfg", Decision: schemas.DecisionImproved, Action: schemas.ActionShip,
		PolicyName: "team", PolicyHash: "sha256:pol", PolicyDecision: schemas.PolicyAllow,
		ManifestHash: "sha256:man", SealsDigest: "abc", Approver: req.Approver, Note: req.Note, Override: req.Override,
		EvidencePath: "rel_1/evidence.zip", EvidenceSHA256: "def",
		CreatedAt: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC), Hash: "h1",
	}
	f.recs = append(f.recs, rec)
	return &rec, nil
}

func (f *fakeReleases) List(context.Context) (*schemas.ReleaseList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &schemas.ReleaseList{Releases: append([]schemas.ReleaseRecord(nil), f.recs...), ChainOK: true}, nil
}

func (f *fakeReleases) Get(_ context.Context, id string) (*schemas.ReleaseRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.recs {
		if r.ReleaseID == id {
			return &r, nil
		}
	}
	return nil, fmt.Errorf("release %s: %w", id, schemas.ErrNotFound)
}

func (f *fakeReleases) Evidence(ctx context.Context, id string) ([]byte, error) {
	if _, err := f.Get(ctx, id); err != nil {
		return nil, err
	}
	return []byte("PK\x03\x04zip"), nil
}

// newReleasesClient mounts the releases handler the way the server does.
func newReleasesClient(t *testing.T, releases ReleaseLog) *testClient {
	t.Helper()
	r := router.New()
	r.NotFound = func(ctx *fasthttp.RequestCtx) {
		lib.SendError(ctx, testMode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found")
	}
	write := lib.RequireWriteToken(testMode, testToken)
	NewReleasesHandler(testMode, releases, write).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
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

func TestReleaseRoutes(t *testing.T) {
	f := &fakeReleases{}
	c := newReleasesClient(t, f)
	p := APIPrefix + "/releases"
	tests := []struct {
		name, method, path, body string
		headers                  map[string]string
		status                   int
		contains                 string
	}{
		{"no token", "POST", p, `{"exp_id":"e_test","approver":"ana"}`, map[string]string{"Content-Type": "application/json"}, 401, "unauthorized"},
		{"wrong token", "POST", p, `{"exp_id":"e_test","approver":"ana"}`, map[string]string{"Authorization": "Bearer nope"}, 401, "unauthorized"},
		{"created", "POST", p, `{"exp_id":"e_test","approver":"ana","note":"weekly"}`, authed("application/json"), 201, `"release_id":"rel_1"`},
		{"override recorded", "POST", p, `{"exp_id":"e_test","approver":"ana","override":"risk accepted"}`, authed("application/json"), 201, `"override":"risk accepted"`},
		{"refused", "POST", p, `{"exp_id":"e_hold","approver":"ana"}`, authed("application/json"), 409, "action is hold"},
		{"unknown experiment", "POST", p, `{"exp_id":"e_none","approver":"ana"}`, authed("application/json"), 404, "not_found"},
		{"failure", "POST", p, `{"exp_id":"e_boom","approver":"ana"}`, authed("application/json"), 500, "disk on fire"},
		{"missing approver", "POST", p, `{"exp_id":"e_test","approver":" "}`, authed("application/json"), 400, "approver is required"},
		{"missing exp", "POST", p, `{"approver":"ana"}`, authed("application/json"), 400, "exp_id is required"},
		{"bad analysis id", "POST", p, `{"exp_id":"e_test","analysis_id":"../x","approver":"ana"}`, authed("application/json"), 400, "analysis_id"},
		{"unknown field", "POST", p, `{"exp_id":"e_test","approver":"ana","signed":true}`, authed("application/json"), 400, "unknown field"},
		{"list", "GET", p, "", nil, 200, `"chain_ok":true`},
		{"get", "GET", p + "/rel_1", "", nil, 200, `"approver":"ana"`},
		{"get unknown", "GET", p + "/rel_9", "", nil, 404, "not_found"},
		{"get bad id", "GET", p + "/..%2Fx", "", nil, 404, "not_found"},
		{"evidence unknown", "GET", p + "/rel_9/evidence", "", nil, 404, "not_found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body []byte
			if tt.body != "" {
				body = []byte(tt.body)
			}
			resp := c.do(t, tt.method, tt.path, body, tt.headers)
			if resp.StatusCode() != tt.status {
				t.Fatalf("status = %d, want %d: %s", resp.StatusCode(), tt.status, resp.Body())
			}
			if !strings.Contains(string(resp.Body()), tt.contains) {
				t.Errorf("body %s lacks %q", resp.Body(), tt.contains)
			}
		})
	}
	if len(f.recs) != 2 || f.recs[0].Note != "weekly" || f.recs[1].Override != "risk accepted" {
		t.Errorf("requests not passed through: %+v", f.recs)
	}
}

func TestReleaseEvidenceIsZipAttachment(t *testing.T) {
	f := &fakeReleases{}
	c := newReleasesClient(t, f)
	if resp := c.do(t, "POST", APIPrefix+"/releases", []byte(`{"exp_id":"e_test","approver":"ana"}`), authed("application/json")); resp.StatusCode() != 201 {
		t.Fatalf("create: %d %s", resp.StatusCode(), resp.Body())
	}
	resp := c.do(t, "GET", APIPrefix+"/releases/rel_1/evidence", nil, nil)
	if resp.StatusCode() != 200 {
		t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
	}
	if ct := string(resp.Header.ContentType()); ct != "application/zip" {
		t.Errorf("content type %q", ct)
	}
	if cd := string(resp.Header.Peek("Content-Disposition")); cd != `attachment; filename="rel_1-evidence.zip"` {
		t.Errorf("content disposition %q", cd)
	}
	if mode := string(resp.Header.Peek(lib.ModeHeader)); mode != string(testMode) {
		t.Errorf("mode header %q", mode)
	}
	if !strings.HasPrefix(string(resp.Body()), "PK") {
		t.Errorf("body %q", resp.Body())
	}
}

func TestReleasesUnavailable(t *testing.T) {
	c := newReleasesClient(t, nil)
	for _, tt := range []struct{ method, path string }{
		{"GET", "/releases"}, {"GET", "/releases/rel_1"}, {"GET", "/releases/rel_1/evidence"}, {"POST", "/releases"},
	} {
		resp := c.do(t, tt.method, APIPrefix+tt.path, []byte(`{}`), authed("application/json"))
		if resp.StatusCode() != 503 {
			t.Errorf("%s %s = %d, want 503", tt.method, tt.path, resp.StatusCode())
		}
	}
}

func TestReleasesMatchContract(t *testing.T) {
	f := &fakeReleases{}
	c := newReleasesClient(t, f)
	empty := decodeBody(t, c.do(t, "GET", APIPrefix+"/releases", nil, nil))
	if recs, ok := empty["releases"].([]any); !ok || len(recs) != 0 {
		t.Errorf("empty list releases = %v, want []", empty["releases"])
	}
	created := decodeBody(t, c.do(t, "POST", APIPrefix+"/releases", []byte(`{"exp_id":"e_test","approver":"ana","override":"x"}`), authed("application/json")))
	checkKeys(t, "ReleaseRecord", created, loadKeys(t, "ReleaseRecord"), true)
	one := decodeBody(t, c.do(t, "GET", APIPrefix+"/releases/rel_1", nil, nil))
	checkKeys(t, "ReleaseRecord", one, loadKeys(t, "ReleaseRecord"), true)
	list := decodeBody(t, c.do(t, "GET", APIPrefix+"/releases", nil, nil))
	checkKeys(t, "ReleaseList", list, loadKeys(t, "ReleaseList"), true)
	for i, obj := range objectsAt(t, list, "releases[]") {
		checkKeys(t, fmt.Sprintf("ReleaseList.releases #%d", i), obj, loadKeys(t, "ReleaseRecord"), false)
	}
}
