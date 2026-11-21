package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func searchServe(t *testing.T, env OrgEnv, uri string) (int, resultsindex.SearchResult) {
	t.Helper()
	h := &searchHandler{env: env, ix: resultsindex.NewSearchIndex()}
	r := router.New()
	r.GET(APIPrefix+"/search", env.R(h.search))
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(uri)
	r.Handler(&ctx)
	var out resultsindex.SearchResult
	if ctx.Response.StatusCode() == fasthttp.StatusOK {
		if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil {
			t.Fatalf("%s: %v", uri, err)
		}
	}
	return ctx.Response.StatusCode(), out
}

func TestSearchRoute(t *testing.T) {
	answer := func(s string) []byte {
		b, _ := json.Marshal(map[string]any{"answer": schemas.Answer{Text: s}})
		return b
	}
	run := func(id, task string, sealed bool) RunView {
		return RunView{Slot: schemas.Slot{ExpID: "e1", Task: task, Arm: "A"}, Sealed: sealed, SealHash: "h" + id,
			Run: schemas.Run{RunID: id, Outcome: schemas.OutcomeFail, Agent: schemas.RunAgentInfo{ConfigID: "bot", AgentVersion: "2"},
				Timing: schemas.RunTiming{Started: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)}}}
	}
	ix := &fakeIndex{
		manifests: map[string]*schemas.Manifest{"e1": {ExpID: "e1", SuiteID: "support", HeldOutTasks: []string{"secret"},
			Arms: map[string]schemas.ArmManifest{"A": {Config: schemas.AgentConfig{ID: "bot", Model: "m1"}}}}},
		runs: []RunView{run("r1", "t1", true), run("r2", "secret", true), run("r3", "t1", false)},
		files: map[string]map[string][]byte{
			"r1": {"evidence.json": answer("refund issued, all tests pass")},
			"r2": {"evidence.json": answer("refund issued in the held out task")},
			"r3": {"evidence.json": answer("refund issued while running")},
		},
	}
	env := OrgEnv{Mode: testMode, Services: Services{Index: ix}}
	code, res := searchServe(t, env, APIPrefix+"/search?q=refund&agent=bot&version=2&benchmark=support@1&outcome=fail")
	if code != fasthttp.StatusOK || res.Total != 1 || len(res.Hits) != 1 || res.Hits[0].RunID != "r1" || res.Hits[0].Model != "m1" {
		t.Fatalf("search %d %+v", code, res)
	}
	if _, res = searchServe(t, env, APIPrefix+"/search?q=refund&version=3"); res.Total != 0 {
		t.Fatalf("version filter %+v", res)
	}
	for _, bad := range []string{"?q=%22open", "?q=x&limit=0", "?q=x&limit=abc"} {
		if code, _ := searchServe(t, env, APIPrefix+"/search"+bad); code != fasthttp.StatusBadRequest {
			t.Fatalf("%s: %d", bad, code)
		}
	}
	if code, _ := searchServe(t, OrgEnv{Mode: testMode}, APIPrefix+"/search?q=x"); code != fasthttp.StatusServiceUnavailable {
		t.Fatalf("no index: %d", code)
	}
}
