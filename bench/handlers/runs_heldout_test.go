package handlers

import (
	"encoding/json"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// A held-out task's run serves no content unless held_out.visible is set:
// the record, diff and events all answer 404 (finding 1-D-01).
func TestRunsHandlerHeldOutRun(t *testing.T) {
	idx := testIndex()
	idx.manifests[testExp].HeldOutTasks = []string{"t01"}
	var held, public string
	for _, rv := range idx.runs {
		if rv.Slot.Task == "t01" {
			held = rv.Run.RunID
		} else {
			public = rv.Run.RunID
		}
	}
	for _, visible := range []bool{false, true} {
		r := router.New()
		NewRunsHandler(testMode, idx).WithHeldOutVisible(visible).RegisterRoutes(r)
		get := func(path string) int {
			ctx := &fasthttp.RequestCtx{}
			ctx.Request.SetRequestURI(path)
			ctx.Request.Header.SetMethod(fasthttp.MethodGet)
			r.Handler(ctx)
			return ctx.Response.StatusCode()
		}
		for _, sub := range []string{"", "/events", "/diff", "/receipts"} {
			code := get(APIPrefix + "/runs/" + held + sub)
			if !visible && code != fasthttp.StatusNotFound {
				t.Fatalf("held-out run %s%s answered %d, want 404", held, sub, code)
			}
			if visible && code == fasthttp.StatusNotFound && sub == "" {
				t.Fatalf("held-out run %s answered 404 with held_out.visible", held)
			}
		}
		if code := get(APIPrefix + "/runs/" + public); code != fasthttp.StatusOK {
			t.Fatalf("public run %s answered %d", public, code)
		}
	}
}

// A held-out task's pair serves only ids and outcomes, with no metrics and
// no alignment, unless held_out.visible is set (audit qc2-05-07). The test
// client registers the pairs route with the default, not visible.
func TestPairsHandlerHeldOutPair(t *testing.T) {
	get := func(held bool) schemas.Pair {
		d := fullDeps()
		idx := testIndex()
		if held {
			idx.manifests[testExp].HeldOutTasks = []string{"t01"}
		}
		d.index = idx
		resp := newTestClient(t, d).do(t, fasthttp.MethodGet, APIPrefix+"/pairs/p_t01", nil, nil)
		if resp.StatusCode() != fasthttp.StatusOK {
			t.Fatalf("held %v: status %d %s", held, resp.StatusCode(), resp.Body())
		}
		var p schemas.Pair
		if err := json.Unmarshal(resp.Body(), &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	hidden := get(true)
	if len(hidden.Runs) == 0 || len(hidden.Alignment) != 0 {
		t.Fatalf("held-out pair %+v", hidden)
	}
	for _, r := range hidden.Runs {
		if len(r.Metrics) != 0 || r.Run.RunID == "" || r.Run.Agent.ConfigID != "" {
			t.Fatalf("held-out pair run served details: %+v", r)
		}
	}
	if public := get(false); len(public.Runs) != len(hidden.Runs) {
		t.Fatalf("public pair has %d runs, held-out %d", len(public.Runs), len(hidden.Runs))
	}
}
