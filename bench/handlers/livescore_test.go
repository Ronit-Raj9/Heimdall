package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livescore"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// fakeLive serves the fixtures livescore's TestWriteFixtures wrote from its
// end-to-end scenario (two versions, the candidate failing every second
// session).
type fakeLive struct {
	status  *livescore.Status
	compare *livescore.Comparison
	list    *livescore.SessionList
	pull    *livescore.PullResult
	pulls   int
	filters []livescore.SessionFilter
}

func loadLiveFixture(t *testing.T, name string, v any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "livescore", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func newFakeLive(t *testing.T) *fakeLive {
	f := &fakeLive{status: &livescore.Status{}, compare: &livescore.Comparison{}, list: &livescore.SessionList{}, pull: &livescore.PullResult{}}
	loadLiveFixture(t, "status.json", f.status)
	loadLiveFixture(t, "compare.json", f.compare)
	loadLiveFixture(t, "sessions.json", f.list)
	loadLiveFixture(t, "pull.json", f.pull)
	return f
}

func (f *fakeLive) Status(context.Context) (*livescore.Status, error) { return f.status, nil }

func (f *fakeLive) Sessions(_ context.Context, fl livescore.SessionFilter) (*livescore.SessionList, error) {
	f.filters = append(f.filters, fl)
	if fl.Version == "nope" {
		return nil, fmt.Errorf("%w: version nope", livescore.ErrBadFilter)
	}
	return f.list, nil
}

func (f *fakeLive) Compare(context.Context) (*livescore.Comparison, error) { return f.compare, nil }

func (f *fakeLive) Pull(context.Context) (*livescore.PullResult, error) {
	f.pulls++
	return f.pull, nil
}

func newLiveClient(t *testing.T, src LivescoreSource, mode schemas.Mode) *testClient {
	t.Helper()
	write := lib.RequireWriteToken(mode, testToken)
	common := []lib.Middleware{lib.BodyLimit(mode, lib.MaxBodyBytes)}
	r := router.New()
	NewLivescoreHandler(mode, src, write).RegisterRoutes(r, common...)
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

func TestLivescoreRoutesMatchContract(t *testing.T) {
	src := newFakeLive(t)
	c := newLiveClient(t, src, schemas.ModeLive)
	session := map[string]string{"signals": "LiveSignals"}
	tests := []struct {
		method, path string
		typ          string
		nested       map[string]string
	}{
		{"GET", "/livescore", "LiveStatus", map[string]string{
			"source": "LiveSourceView", "versions[]": "LiveVersionView", "versions[].last_24h[]": "LiveSignalMean",
			"versions[].series[]": "LiveSeriesBucket", "versions[].series[].means[]": "LiveSignalMean", "signals[]": "LiveSignalDef",
			"grouping": "LiveGroupingView", "compare": "LiveComparison", "compare.signals[]": "LiveSignalResult",
			"worst[]": "LiveSessionRecord", "worst[].signals": "LiveSignals", "events[]": "LiveLogEvent", "privacy": "LivePrivacyView",
			"judge": "LiveJudgeView",
		}},
		{"GET", "/livescore/sessions?version=v2&flag=abandoned&limit=5", "LiveSessionList", map[string]string{
			"sessions[]": "LiveSessionRecord", "sessions[].signals": session["signals"]}},
		{"GET", "/livescore/compare", "LiveComparison", map[string]string{"signals[]": "LiveSignalResult"}},
		{"POST", "/livescore/pull", "LivePullResult", nil},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			var headers map[string]string
			if tt.method == "POST" {
				headers = authed("")
			}
			resp := c.do(t, tt.method, APIPrefix+tt.path, nil, headers)
			if resp.StatusCode() != 200 {
				t.Fatalf("status = %d: %s", resp.StatusCode(), resp.Body())
			}
			body := decodeBody(t, resp)
			checkKeys(t, tt.typ, body, loadKeys(t, tt.typ), true)
			for path, typ := range tt.nested {
				objs := objectsAt(t, body, path)
				if len(objs) == 0 {
					t.Errorf("%s: the fixture has no %s; regenerate it with every field shown", tt.typ, path)
				}
				for i, obj := range objs {
					checkKeys(t, fmt.Sprintf("%s.%s #%d", tt.typ, path, i), obj, loadKeys(t, typ), false)
				}
			}
		})
	}
	if src.pulls != 1 {
		t.Errorf("pulls = %d", src.pulls)
	}
	if f := src.filters[0]; f.Version != "v2" || f.Flag != "abandoned" || f.Limit != 5 {
		t.Errorf("filter %+v", f)
	}
	if src.status.Evidence != "live" || src.compare.Evidence != "live" || src.list.Evidence != "live" {
		t.Error("a live response is not labelled evidence live")
	}
}

func TestLivescoreRouteErrors(t *testing.T) {
	src := newFakeLive(t)
	c := newLiveClient(t, src, schemas.ModeLive)
	cases := []struct {
		method, path string
		headers      map[string]string
		status       int
	}{
		{"POST", "/livescore/pull", nil, 401}, // no write token
		{"GET", "/livescore/sessions?limit=0", nil, 400},
		{"GET", "/livescore/sessions?version=nope", nil, 400},
	}
	for _, cc := range cases {
		if resp := c.do(t, cc.method, APIPrefix+cc.path, nil, cc.headers); resp.StatusCode() != cc.status {
			t.Errorf("%s %s = %d, want %d: %s", cc.method, cc.path, resp.StatusCode(), cc.status, resp.Body())
		}
	}
	src.compare = nil
	if resp := c.do(t, "GET", APIPrefix+"/livescore/compare", nil, nil); resp.StatusCode() != 404 {
		t.Errorf("no comparison = %d, want 404", resp.StatusCode())
	}
	rc := newLiveClient(t, src, schemas.ModeRecorded)
	if resp := rc.do(t, "POST", APIPrefix+"/livescore/pull", nil, authed("")); resp.StatusCode() != 409 {
		t.Errorf("pull on a recorded server = %d, want 409", resp.StatusCode())
	}
	nc := newLiveClient(t, nil, schemas.ModeLive)
	if resp := nc.do(t, "GET", APIPrefix+"/livescore", nil, nil); resp.StatusCode() != 503 {
		t.Errorf("no source = %d, want 503", resp.StatusCode())
	}
}
