package handlers

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/valyala/fasthttp"
)

func TestJobPriorityRoute(t *testing.T) {
	// A runner that is never started: its jobs stay queued.
	r := jobs.New(dbtest.Open(t), jobs.Options{Owner: "test", PollEvery: time.Hour})
	r.Register("demo.wait", func(context.Context, *jobs.Job, jobs.Logger, func(any)) (any, error) { return nil, nil })
	j, err := r.Enqueue(context.Background(), jobs.NewJob{OrgID: "org_a", Kind: "demo.wait"})
	if err != nil {
		t.Fatal(err)
	}
	h := NewJobsHandler(testMode, r, passThrough)
	defer h.Close()
	reg := func(rt *router.Router) { h.RegisterRoutes(rt) }
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_a", OrgID: "org_a", Role: auth.RoleAdmin}
	member := &auth.Principal{Kind: auth.KindUser, UserID: "usr_m", OrgID: "org_a", Role: auth.RoleMember}
	other := &auth.Principal{Kind: auth.KindUser, UserID: "usr_o", OrgID: "org_b", Role: auth.RoleOwner}
	path := "/api/bench/jobs/" + j.ID + "/priority"
	for _, tc := range []struct {
		name string
		p    *auth.Principal
		body string
		code int
		want string
	}{
		{"member may not", member, `{"priority":5}`, 403, "admin"},
		{"other org", other, `{"priority":5}`, 404, "not found"},
		{"out of range", admin, `{"priority":11}`, 400, "-10 to 10"},
		{"unknown field", admin, `{"priority":1,"x":1}`, 400, "unknown field"},
		{"bumped", admin, `{"priority":5}`, 200, `"priority":5`},
	} {
		resp := jobsRequest(t, reg, tc.p, "POST", path, tc.body)
		if resp.StatusCode() != tc.code || !strings.Contains(string(resp.Body()), tc.want) {
			t.Fatalf("%s: %d %s", tc.name, resp.StatusCode(), resp.Body())
		}
	}
	if _, err := r.Cancel(context.Background(), "org_a", j.ID); err != nil {
		t.Fatal(err)
	}
	if resp := jobsRequest(t, reg, admin, "POST", path, `{"priority":1}`); resp.StatusCode() != fasthttp.StatusConflict {
		t.Fatalf("cancelled job: %d %s", resp.StatusCode(), resp.Body())
	}
	deny := func(fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) { ctx.SetStatusCode(fasthttp.StatusUnauthorized) }
	}
	denied := func(rt *router.Router) { NewJobsHandler(testMode, r, deny).RegisterRoutes(rt) }
	if resp := jobsRequest(t, denied, nil, "POST", path, `{"priority":1}`); resp.StatusCode() != 401 {
		t.Fatalf("without the write token: %d", resp.StatusCode())
	}
}
