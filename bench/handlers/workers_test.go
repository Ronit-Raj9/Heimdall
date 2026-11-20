package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

const testWorkerToken = "worker-secret"

// fakeWorkerAPI records calls and answers with err when set.
type fakeWorkerAPI struct {
	err      error
	lease    *schemas.WorkerLease
	offset   int64
	data     []byte
	complete *schemas.WorkerComplete
}

func (f *fakeWorkerAPI) CheckToken(tok string) bool { return tok == testWorkerToken }

func (f *fakeWorkerAPI) Register(_ context.Context, req *schemas.WorkerRegister) (*schemas.WorkerRegistered, error) {
	return &schemas.WorkerRegistered{WorkerID: req.WorkerID, ChunkBytes: 8}, f.err
}

func (f *fakeWorkerAPI) Lease(context.Context, string) (*schemas.WorkerLease, error) {
	return f.lease, f.err
}

func (f *fakeWorkerAPI) Manifest(_ context.Context, _, expID string) (*schemas.Manifest, error) {
	return &schemas.Manifest{ExpID: expID}, f.err
}

func (f *fakeWorkerAPI) Heartbeat(context.Context, string, string) error { return f.err }

func (f *fakeWorkerAPI) Upload(_ context.Context, _, _ string, offset int64, data []byte) (int64, error) {
	f.offset, f.data = offset, data
	return offset + int64(len(data)), f.err
}

func (f *fakeWorkerAPI) Complete(_ context.Context, leaseID string, req *schemas.WorkerComplete) (*schemas.WorkerResult, error) {
	f.complete = req
	return &schemas.WorkerResult{LeaseID: leaseID, RunID: req.RunID, Accepted: true}, f.err
}

func (f *fakeWorkerAPI) Release(context.Context, string, *schemas.WorkerRelease) error { return f.err }

func (f *fakeWorkerAPI) Workers() []schemas.WorkerInfo {
	return []schemas.WorkerInfo{{ID: "w1", Host: "box", State: schemas.WorkerIdle, Slots: 2, Running: []string{}}}
}

func (f *fakeWorkerAPI) Whoami(context.Context) (*schemas.RunnerWhoami, error) {
	return &schemas.RunnerWhoami{Mode: "local", Workers: f.Workers()}, f.err
}

// workerCall sends one request through the worker routes.
func workerCall(t *testing.T, api WorkerAPI, method, uri, token, body string) *fasthttp.Response {
	t.Helper()
	r := router.New()
	NewWorkersHandler(schemas.ModeLive, api).RegisterRoutes(r)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	if token != "" {
		ctx.Request.Header.Set("Authorization", "Bearer "+token)
	}
	ctx.Request.SetBodyString(body)
	r.Handler(&ctx)
	resp := &fasthttp.Response{}
	ctx.Response.CopyTo(resp)
	return resp
}

func TestWorkersAuth(t *testing.T) {
	api := &fakeWorkerAPI{}
	routes := []struct{ method, uri, body string }{
		{"POST", "/api/bench/workers/register", `{"worker_id":"w1","slots":1}`},
		{"POST", "/api/bench/workers/lease", `{"worker_id":"w1"}`},
		{"GET", "/api/bench/workers/manifests/e_1?worker_id=w1", ""},
		{"POST", "/api/bench/workers/leases/l_1/heartbeat", `{"worker_id":"w1"}`},
		{"POST", "/api/bench/workers/leases/l_1/upload?worker_id=w1&offset=0", "abc"},
		{"POST", "/api/bench/workers/leases/l_1/complete", `{"worker_id":"w1","run_id":"r_1"}`},
		{"POST", "/api/bench/workers/leases/l_1/release", `{"worker_id":"w1","reason":"x"}`},
	}
	for _, rt := range routes {
		t.Run(rt.uri, func(t *testing.T) {
			for _, tok := range []string{"", "wrong", testWorkerToken + "x", "worker"} {
				if resp := workerCall(t, api, rt.method, rt.uri, tok, rt.body); resp.StatusCode() != fasthttp.StatusUnauthorized {
					t.Fatalf("token %q: status %d", tok, resp.StatusCode())
				}
			}
			resp := workerCall(t, api, rt.method, rt.uri, testWorkerToken, rt.body)
			if s := resp.StatusCode(); s != fasthttp.StatusOK && s != fasthttp.StatusNoContent {
				t.Fatalf("right token: status %d: %s", s, resp.Body())
			}
			if resp := workerCall(t, nil, rt.method, rt.uri, testWorkerToken, rt.body); resp.StatusCode() != fasthttp.StatusServiceUnavailable {
				t.Fatalf("worker mode off: status %d", resp.StatusCode())
			}
		})
	}
	if strings.Contains(string(workerCall(t, api, "POST", "/api/bench/workers/lease", "wrong", `{}`).Body()), "wrong") {
		t.Fatal("the 401 answer echoes the token")
	}
}

func TestWorkersList(t *testing.T) {
	for _, tc := range []struct {
		api     WorkerAPI
		enabled bool
		n       int
	}{{&fakeWorkerAPI{}, true, 1}, {nil, false, 0}} {
		resp := workerCall(t, tc.api, "GET", "/api/bench/workers", "", "")
		var out WorkersList
		if err := json.Unmarshal(resp.Body(), &out); err != nil || resp.StatusCode() != 200 {
			t.Fatalf("status %d %v: %s", resp.StatusCode(), err, resp.Body())
		}
		if out.Enabled != tc.enabled || len(out.Workers) != tc.n || out.Workers == nil {
			t.Fatalf("list %+v", out)
		}
	}
}

func TestWorkersErrorStatus(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		{schemas.ErrWorkerUnknown, 404},
		{schemas.ErrLeaseGone, 410},
		{schemas.ErrUploadOffset, 409},
		{schemas.ErrUploadTooLarge, 413},
		{schemas.ErrWorkerRequest, 400},
		{fmt.Errorf("disk full"), 500},
	}
	for _, tc := range cases {
		api := &fakeWorkerAPI{err: fmt.Errorf("wrapped: %w", tc.err)}
		resp := workerCall(t, api, "POST", "/api/bench/workers/leases/l_1/heartbeat", testWorkerToken, `{"worker_id":"w1"}`)
		if resp.StatusCode() != tc.status {
			t.Fatalf("%v: status %d, want %d", tc.err, resp.StatusCode(), tc.status)
		}
	}
}

func TestWorkersUploadAndLease(t *testing.T) {
	api := &fakeWorkerAPI{}
	if resp := workerCall(t, api, "POST", "/api/bench/workers/lease", testWorkerToken, `{"worker_id":"w1"}`); resp.StatusCode() != 204 {
		t.Fatalf("no work: status %d", resp.StatusCode())
	}
	api.lease = &schemas.WorkerLease{LeaseID: "l_1", ExpID: "e_1"}
	resp := workerCall(t, api, "POST", "/api/bench/workers/lease", testWorkerToken, `{"worker_id":"w1"}`)
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"lease_id":"l_1"`) {
		t.Fatalf("lease: %d %s", resp.StatusCode(), resp.Body())
	}
	resp = workerCall(t, api, "POST", "/api/bench/workers/leases/l_1/upload?worker_id=w1&offset=5", testWorkerToken, "chunk")
	if resp.StatusCode() != 200 || api.offset != 5 || string(api.data) != "chunk" || !strings.Contains(string(resp.Body()), `"bytes":10`) {
		t.Fatalf("upload: %d %s", resp.StatusCode(), resp.Body())
	}
	for _, q := range []string{"offset=-1", "offset=x", ""} {
		resp := workerCall(t, api, "POST", "/api/bench/workers/leases/l_1/upload?worker_id=w1&"+q, testWorkerToken, "c")
		if resp.StatusCode() != 400 {
			t.Fatalf("%s: status %d", q, resp.StatusCode())
		}
	}
	if resp := workerCall(t, api, "POST", "/api/bench/workers/leases/l_1/complete", testWorkerToken, `{"worker_id":"w1","nope":1}`); resp.StatusCode() != 400 {
		t.Fatalf("unknown field: status %d", resp.StatusCode())
	}
}

// hostedFakeWorkerAPI is fakeWorkerAPI on a hosted coordinator of one org.
type hostedFakeWorkerAPI struct{ fakeWorkerAPI }

func (hostedFakeWorkerAPI) OrgID() string { return "org_a" }

func (hostedFakeWorkerAPI) Workers() []schemas.WorkerInfo {
	return []schemas.WorkerInfo{{ID: "w1", Host: "box", Version: "v9", State: schemas.WorkerRunning, Slots: 2,
		Running: []string{"s1"}, Pool: "gpu", Labels: []string{"a100"}, TokenID: "rtk_1", GPUs: []schemas.GPUInfo{{}}}}
}

// TestWorkersListHostedInventory: only an org admin sees runner
// infrastructure on a hosted server (4-B-02).
func TestWorkersListHostedInventory(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    *auth.Principal
		full bool
	}{
		{"anonymous", nil, false},
		{"viewer", &auth.Principal{Kind: auth.KindUser, OrgID: "org_a", Role: auth.RoleViewer}, false},
		{"admin of another org", &auth.Principal{Kind: auth.KindUser, OrgID: "org_b", Role: auth.RoleOwner}, false},
		{"admin", &auth.Principal{Kind: auth.KindUser, OrgID: "org_a", Role: auth.RoleAdmin}, true},
	} {
		r := router.New()
		NewWorkersHandler(schemas.ModeLive, &hostedFakeWorkerAPI{}).RegisterRoutes(r)
		var ctx fasthttp.RequestCtx
		ctx.Request.SetRequestURI("/api/bench/workers")
		if tc.p != nil {
			auth.SetPrincipal(&ctx, tc.p)
		}
		r.Handler(&ctx)
		var out WorkersList
		if err := json.Unmarshal(ctx.Response.Body(), &out); err != nil || len(out.Workers) != 1 {
			t.Fatalf("%s: %v %s", tc.name, err, ctx.Response.Body())
		}
		w := out.Workers[0]
		if w.ID != "w1" || w.Slots != 2 || w.Pool != "gpu" || len(w.Running) != 1 {
			t.Fatalf("%s: summary lost: %+v", tc.name, w)
		}
		leaked := w.Host != "" || w.Version != "" || w.TokenID != "" || len(w.Labels) > 0 || len(w.GPUs) > 0
		if leaked != tc.full {
			t.Fatalf("%s: full=%v, got %+v", tc.name, tc.full, w)
		}
	}
}
