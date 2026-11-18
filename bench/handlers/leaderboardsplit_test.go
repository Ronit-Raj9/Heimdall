package handlers

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// heldOutIndex is leaderboardIndex with t01 held out by the plan, so the
// public board counts t02 only and the held-out board t01 only.
func heldOutIndex() *fakeIndex {
	idx := leaderboardIndex()
	idx.manifests[testExp].HeldOutTasks = []string{"t01"}
	return idx
}

// serveBoard sends one GET to path on a router holding the leaderboard and
// cost explain handlers with the given held-out visibility.
func serveBoard(t *testing.T, visible bool, path string) (int, map[string]any) {
	t.Helper()
	now := func() time.Time { return time.Date(2026, 11, 15, 9, 30, 0, 0, time.UTC) }
	board := NewLeaderboardHandler(testMode, heldOutIndex()).WithHeldOutVisible(visible)
	board.now = now
	plane := NewCostExplainHandler(testMode, nil, heldOutIndex()).WithHeldOutVisible(visible)
	plane.now = now
	r := router.New()
	mw := lib.BodyLimit(testMode, lib.MaxBodyBytes)
	board.RegisterRoutes(r, mw)
	plane.RegisterRoutes(r, mw)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(fasthttp.MethodGet)
	ctx.Request.SetRequestURI(APIPrefix + path)
	r.Handler(&ctx)
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil {
		t.Fatalf("decode %q: %v", ctx.Response.Body(), err)
	}
	return ctx.Response.StatusCode(), body
}

// The held-out board and its Pareto plane are refused unless held_out.visible
// is set; the public board is served either way and never counts a held-out
// task.
func TestHeldOutBoardNeedsVisibility(t *testing.T) {
	for _, path := range []string{"/leaderboard?split=held_out", "/leaderboard/pareto?split=held_out"} {
		status, body := serveBoard(t, false, path)
		if status != fasthttp.StatusNotFound {
			t.Fatalf("%s hidden: status = %d, want 404: %v", path, status, body)
		}
		if errBody, _ := body["error"].(map[string]any); errBody["type"] != lib.ErrTypeNotFound {
			t.Errorf("%s hidden: error = %v", path, body["error"])
		}
		if status, body := serveBoard(t, true, path); status != fasthttp.StatusOK {
			t.Fatalf("%s visible: status = %d: %v", path, status, body)
		}
	}

	status, body := serveBoard(t, true, "/leaderboard?split=held_out")
	if status != fasthttp.StatusOK || body["split"] != "held_out" {
		t.Fatalf("visible held-out board: status %d, split %v", status, body["split"])
	}
	if got := body["common_tasks"]; !sameStrings(got, "t01") {
		t.Errorf("held-out board common_tasks = %v, want [t01]", got)
	}

	for _, visible := range []bool{false, true} {
		for _, query := range []string{"", "?split=public"} {
			status, body := serveBoard(t, visible, "/leaderboard"+query)
			if status != fasthttp.StatusOK {
				t.Fatalf("public board (visible %v, %q): status = %d: %v", visible, query, status, body)
			}
			if _, ok := body["split"]; ok {
				t.Errorf("public board (visible %v, %q) carries split %v", visible, query, body["split"])
			}
			if got := body["common_tasks"]; !sameStrings(got, "t02") {
				t.Errorf("public board (visible %v, %q) common_tasks = %v, want [t02]", visible, query, got)
			}
		}
	}
}

// sameStrings reports whether a decoded JSON array holds exactly want, in order.
func sameStrings(got any, want ...string) bool {
	list, ok := got.([]any)
	if !ok || len(list) != len(want) {
		return false
	}
	for i, v := range list {
		if v != want[i] {
			return false
		}
	}
	return true
}
