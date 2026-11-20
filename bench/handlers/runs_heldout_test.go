package handlers

import (
	"testing"

	"github.com/fasthttp/router"
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
