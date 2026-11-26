package handlers

import (
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// TestShareOpenFetchesPartsConcurrently: an evaluation share has two parts.
// Each part takes 200 ms here, so fetching them one after another takes
// 400 ms; fetched together the page answers in well under 350 ms. When the
// first part is gone the link still answers 404.
func TestShareOpenFetchesPartsConcurrently(t *testing.T) {
	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}
	r := router.New()
	registerShareLinks(env, r)
	var gone atomic.Bool
	slow := func(ctx *fasthttp.RequestCtx) {
		time.Sleep(200 * time.Millisecond)
		if gone.Load() {
			ctx.SetStatusCode(fasthttp.StatusNotFound)
			ctx.SetBodyString(`{"error":{"message":"not found"}}`)
			return
		}
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json")
		ctx.SetBodyString(`{"id":"ev_1"}`)
	}
	r.GET(APIPrefix+"/evaluations/{id}", slow)
	r.GET(APIPrefix+"/evaluations/{id}/report", slow)

	res := shareCall(r, "POST", APIPrefix+"/shares", map[string]any{"kind": "evaluation", "target": "ev_1"})
	if res.StatusCode() != 201 {
		t.Fatalf("create: %d %s", res.StatusCode(), res.Body())
	}
	var sv ShareView
	if err := json.Unmarshal(res.Body(), &sv); err != nil || sv.Token == "" {
		t.Fatalf("view %s %v", res.Body(), err)
	}

	start := time.Now()
	res = shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token, nil)
	took := time.Since(start)
	if res.StatusCode() != 200 {
		t.Fatalf("open: %d %s", res.StatusCode(), res.Body())
	}
	var item SharedItem
	if err := json.Unmarshal(res.Body(), &item); err != nil || item.Data["evaluation"] == nil || item.Data["report"] == nil {
		t.Fatalf("item %s %v", res.Body(), err)
	}
	if took >= 350*time.Millisecond {
		t.Fatalf("open took %s; the two parts were not fetched together", took)
	}

	gone.Store(true)
	if res := shareCall(r, "GET", APIPrefix+"/shared/"+sv.Token, nil); res.StatusCode() != 404 {
		t.Fatalf("gone item: %d %s", res.StatusCode(), res.Body())
	}
}
