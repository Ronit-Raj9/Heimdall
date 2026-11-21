package handlers

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/improve/improvetest"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

// runnerSource drives a real improve.Runner over the scripted executor,
// synchronously, so each request sees the finished state.
type runnerSource struct{ r *improve.Runner }

func (s runnerSource) List(context.Context) (*improve.List, error) {
	recs, err := s.r.List()
	if err != nil {
		return nil, err
	}
	l := &improve.List{Improvements: []improve.Summary{}}
	for _, rec := range recs {
		l.Improvements = append(l.Improvements, improve.Summarize(rec, false))
	}
	return l, nil
}

func (s runnerSource) Get(_ context.Context, id string) (*improve.Detail, error) {
	rec, err := s.r.Get(id)
	if err != nil {
		return nil, err
	}
	return s.r.View(rec), nil
}

func (s runnerSource) Lineage(_ context.Context, id string) (*improve.LineageView, error) {
	lv, err := s.r.Lineage(id)
	return &lv, err
}

func (s runnerSource) Create(ctx context.Context, spec *improve.Spec, actor string) (*improve.Summary, error) {
	rec, err := s.r.Create(ctx, spec, actor)
	if err != nil {
		return nil, err
	}
	if rec, err = s.r.Run(ctx, rec.ID); err != nil {
		return nil, err
	}
	sum := improve.Summarize(rec, false)
	return &sum, nil
}

func (s runnerSource) Approve(ctx context.Context, id string, round int, actor, reason string) (*improve.Summary, error) {
	rec, err := s.r.Approve(id, round, actor, reason)
	if err != nil {
		return nil, err
	}
	if rec, err = s.r.Run(ctx, id); err != nil && rec == nil {
		return nil, err
	}
	sum := improve.Summarize(rec, false)
	return &sum, nil
}

func (s runnerSource) Stop(_ context.Context, id, actor string) (*improve.Summary, error) {
	rec, err := s.r.Stop(id, actor)
	if err != nil {
		return nil, err
	}
	sum := improve.Summarize(rec, false)
	return &sum, nil
}

func newImproveClient(t *testing.T, src ImproveSource) *testClient {
	t.Helper()
	r := router.New()
	NewImproveHandler(testMode, src, lib.RequireWriteToken(testMode, testToken)).RegisterRoutes(r, lib.BodyLimit(testMode, lib.MaxBodyBytes))
	ln := fasthttputil.NewInmemoryListener()
	srv := &fasthttp.Server{Handler: r.Handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return &testClient{client: &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}}
}

const improveSpecYAML = `name: notes-loop
agent: agents/scripted-improver.yaml
suite: scripted@1.0.0
seed: 7
editable:
  files: [/home/agent/AGENTS.md]
commit: {trials: 5}
`

func TestImproveRoutes(t *testing.T) {
	x := &improvetest.Exec{Behavior: improvetest.Quiet, Base: 1, NTasks: 40}
	src := runnerSource{r: &improve.Runner{Root: filepath.Join(t.TempDir(), improve.DefaultDir), Exec: x}}
	c := newImproveClient(t, src)

	if resp := c.do(t, "POST", APIPrefix+"/improvements", []byte(improveSpecYAML), nil); resp.StatusCode() != 401 {
		t.Fatalf("create without token: %d", resp.StatusCode())
	}
	if resp := c.do(t, "POST", APIPrefix+"/improvements", []byte("name: x\nbogus: 1\n"), authed("")); resp.StatusCode() != 422 {
		t.Fatalf("bad spec: %d %s", resp.StatusCode(), resp.Body())
	}
	resp := c.do(t, "POST", APIPrefix+"/improvements", []byte(improveSpecYAML), authed(""))
	if resp.StatusCode() != 202 {
		t.Fatalf("create: %d %s", resp.StatusCode(), resp.Body())
	}
	var sum improve.Summary
	if err := json.Unmarshal(resp.Body(), &sum); err != nil || sum.Status != improve.StatusAwaiting {
		t.Fatalf("summary %+v %v", sum, err)
	}
	id := sum.ID

	resp = c.do(t, "GET", APIPrefix+"/improvements", nil, nil)
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), id) {
		t.Fatalf("list: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = c.do(t, "GET", APIPrefix+"/improvements/"+id, nil, nil)
	var d improve.Detail
	if resp.StatusCode() != 200 || json.Unmarshal(resp.Body(), &d) != nil || len(d.Rounds) != 1 {
		t.Fatalf("detail: %d %s", resp.StatusCode(), resp.Body())
	}
	if strings.Contains(string(resp.Body()), `"doc"`) || d.Splits.Sealed == 0 {
		t.Error("detail serves the agent doc or misses the sealed count")
	}
	if resp = c.do(t, "GET", APIPrefix+"/improvements/"+id+"/rounds/1", nil, nil); resp.StatusCode() != 200 {
		t.Fatalf("round: %d", resp.StatusCode())
	}
	if resp = c.do(t, "GET", APIPrefix+"/improvements/"+id+"/rounds/9", nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("missing round: %d", resp.StatusCode())
	}
	if resp = c.do(t, "GET", APIPrefix+"/improvements/"+id+"/lineage", nil, nil); resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"chain_ok":true`) {
		t.Fatalf("lineage: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp = c.do(t, "POST", APIPrefix+"/improvements/"+id+"/rounds/1/approve", []byte(`{}`), authed("")); resp.StatusCode() != 422 {
		t.Fatalf("approve without reason: %d", resp.StatusCode())
	}
	if resp = c.do(t, "POST", APIPrefix+"/improvements/"+id+"/rounds/1/approve", []byte(`{"reason":"logging change reviewed"}`), authed("")); resp.StatusCode() != 202 {
		t.Fatalf("approve: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp = c.do(t, "POST", APIPrefix+"/improvements/"+id+"/stop", nil, authed("")); resp.StatusCode() != 200 && resp.StatusCode() != 409 {
		t.Fatalf("stop: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp = c.do(t, "GET", APIPrefix+"/improvements/imp_missing-1", nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("missing: %d", resp.StatusCode())
	}
	if resp = c.do(t, "GET", APIPrefix+"/improvements/..", nil, nil); resp.StatusCode() != 404 {
		t.Fatalf("bad id: %d", resp.StatusCode())
	}
}

func TestImproveUnavailable(t *testing.T) {
	c := newImproveClient(t, nil)
	if resp := c.do(t, "GET", APIPrefix+"/improvements", nil, nil); resp.StatusCode() != 503 {
		t.Fatalf("nil source: %d", resp.StatusCode())
	}
}
