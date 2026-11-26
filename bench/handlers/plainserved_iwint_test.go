package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/issuesync"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/shadowreplay"
	"github.com/maximhq/bifrost/bench/validity"
	"github.com/valyala/fasthttp"
)

// Wave 4 final integration (lead's rule 13): the routes added in this wave
// that can carry engine, planner or worker text clean it where it leaves for
// a browser, on a hosted server only, like the routes of
// plainserved_w4b04b_test.go:
//
//	GET  /investigations, /investigations/{id}    why an investigation stopped
//	GET  /replays, /replays/{id}                  why a replay stopped
//	GET  /issuesync                               a weekly sync's run reason and pause reason
//	GET  /validity                                a result's invalid reasons
//	GET  /improvements, /improvements/{id}        an improvement run's reason and error
//	GET  /experiments/{id}/gateway-effect         a feature test's unknown reason
//
// The image stage message ("Preparing the container image") is a live event
// and is cleaned with every live event (plainEvents, engine/remote.go).

type plainImproveSource struct{ ImproveSource }

func (plainImproveSource) List(context.Context) (*improve.List, error) {
	return &improve.List{Improvements: []improve.Summary{{ID: "imp_1", Status: "failed", Reason: w4b04bText}}}, nil
}

func (plainImproveSource) Get(context.Context, string) (*improve.Detail, error) {
	return &improve.Detail{Summary: improve.Summary{ID: "imp_1", Status: "failed", Reason: w4b04bText}, Error: w4b04bText}, nil
}

func plainServeArea(t *testing.T, area string, env OrgEnv) *router.Router {
	t.Helper()
	orgAreasMu.Lock()
	a := orgAreas[area]
	orgAreasMu.Unlock()
	if a == nil {
		t.Fatalf("org area %s not registered", area)
	}
	r := router.New()
	a(env, r)
	return r
}

func plainGet(t *testing.T, h fasthttp.RequestHandler, uri string) string {
	t.Helper()
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod("GET")
	ctx.Request.SetRequestURI(uri)
	h(&ctx)
	if ctx.Response.StatusCode() != fasthttp.StatusOK {
		t.Fatalf("%s: %d %s", uri, ctx.Response.StatusCode(), ctx.Response.Body())
	}
	return string(ctx.Response.Body())
}

func plainFixtures(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	rep := investigator.Report{ID: "inv1", ExpID: "e_test", Stopped: w4b04bText, CreatedAt: time.Now().UTC()}
	b, _ := json.Marshal(rep)
	if err := os.MkdirAll(filepath.Join(root, "investigations"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "investigations", "inv1.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := shadowreplay.SaveReport(root, &shadowreplay.RunReport{ID: "rp1", Mode: "fixed", Stopped: w4b04bText, Counts: map[string]int{}, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	err := issuesync.Open(root).Update(func([]issuesync.Repo) ([]issuesync.Repo, error) {
		return []issuesync.Repo{{ID: "sync-00000001", Repo: "a/b", Benchmark: "issues-a-b", Paused: true, PausedReason: w4b04bText,
			Runs: []issuesync.RunRef{{At: time.Now().UTC(), Status: "failed", Reason: w4b04bText}}}}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := validity.Open(root).Add([]validity.Entry{{ID: "e_test:rg1", ExpID: "e_test", RegradeID: "rg1", Flipped: true, At: time.Now().UTC(),
		After: validity.Side{Source: "rg1", Decision: "invalid", InvalidReasons: []string{w4b04bText}}}}); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestServedEngineTextPlainNewRoutes walks the routes listed above.
func TestServedEngineTextPlainNewRoutes(t *testing.T) {
	w4b04bHosted(t)
	root := plainFixtures(t)
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: testIndex()}}
	inv := plainServeArea(t, "investigations", env).Handler
	rp := router.New()
	registerReplays(env, rp)
	is := router.New()
	registerIssueSync(env, is)
	vh := &validityHandler{mode: testMode, index: testIndex(), analyzer: &w449Analyzer{}, store: validity.Open(root)}
	vr := router.New()
	vh.register(vr)
	val := vr.Handler
	imp := newImproveClient(t, plainImproveSource{})
	gw := gatewayTestsRouter(env, gwTestSource{manifests: map[string]*schemas.Manifest{
		"e_gw": {Experiment: schemas.Experiment{ID: "e_gw", GatewayTest: &schemas.GatewayTest{Feature: w4b04bText, Control: "A"}}},
	}, runs: map[string][]leaderboard.Run{}})

	routes := []struct {
		name string
		get  func() string
	}{
		{"GET /investigations", func() string { return plainGet(t, inv, APIPrefix+"/investigations") }},
		{"GET /investigations/{id}", func() string { return plainGet(t, inv, APIPrefix+"/investigations/inv1") }},
		{"GET /replays/{id}", func() string { return plainGet(t, rp.Handler, APIPrefix+"/replays/rp1") }},
		{"GET /issuesync", func() string { return plainGet(t, is.Handler, APIPrefix+"/issuesync") }},
		{"GET /validity", func() string { return plainGet(t, val, APIPrefix+"/validity") }},
		{"GET /improvements", func() string { return string(imp.do(t, "GET", APIPrefix+"/improvements", nil, nil).Body()) }},
		{"GET /improvements/{id}", func() string { return string(imp.do(t, "GET", APIPrefix+"/improvements/imp_1", nil, nil).Body()) }},
		{"GET /experiments/{id}/gateway-effect", func() string {
			// The fixture's text sits in the feature id too, which is an id and served as it is: check the display text.
			var e struct{ Reason, Headline string }
			_ = json.Unmarshal([]byte(plainGet(t, gw, APIPrefix+"/experiments/e_gw/gateway-effect")), &e)
			return e.Reason + " | " + e.Headline
		}},
	}
	for _, r := range routes {
		w4b04bClean(t, r.name, r.get())
	}
}

// TestServedEngineTextLocalNewRoutes: the local server serves the stored text as it is.
func TestServedEngineTextLocalNewRoutes(t *testing.T) {
	root := plainFixtures(t)
	env := OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Index: testIndex()}}
	body := plainGet(t, plainServeArea(t, "investigations", env).Handler, APIPrefix+"/investigations/inv1")
	var rep investigator.Report
	if err := json.Unmarshal([]byte(body), &rep); err != nil || rep.Stopped != w4b04bText {
		t.Fatalf("local investigation stopped %q, want the stored text: %s", rep.Stopped, body)
	}
}
