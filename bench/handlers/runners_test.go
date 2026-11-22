package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/runners"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Runners area: the runner admin routes and the hosted worker routes.

// fakeRunnerAdmin records the organization of every call.
type fakeRunnerAdmin struct {
	err  error
	orgs []string
	req  schemas.RunnerTokenCreate
	by   string
}

func (f *fakeRunnerAdmin) seen(org string) { f.orgs = append(f.orgs, org) }

func (f *fakeRunnerAdmin) ListRunners(_ context.Context, org string) ([]schemas.RunnerView, error) {
	f.seen(org)
	return []schemas.RunnerView{{ID: "rnr_1", OrgID: org, Name: "box-01", Pool: "gpu", State: schemas.RunnerIdle, Running: []string{}, Labels: []string{}}}, f.err
}

func (f *fakeRunnerAdmin) GetRunner(_ context.Context, org, id string) (*schemas.RunnerView, error) {
	f.seen(org)
	return &schemas.RunnerView{ID: id, OrgID: org}, f.err
}

func (f *fakeRunnerAdmin) RemoveRunner(_ context.Context, org, id string) (*schemas.RunnerView, error) {
	f.seen(org)
	return &schemas.RunnerView{ID: id, OrgID: org, Name: "box-01"}, f.err
}

func (f *fakeRunnerAdmin) ListTokens(_ context.Context, org string) ([]schemas.RunnerTokenView, error) {
	f.seen(org)
	return []schemas.RunnerTokenView{{ID: "rtk_1", OrgID: org, Pool: "gpu", State: schemas.RunnerTokenActive}}, f.err
}

func (f *fakeRunnerAdmin) CreateToken(_ context.Context, org, by string, req schemas.RunnerTokenCreate) (*schemas.RunnerTokenCreated, error) {
	f.seen(org)
	f.req, f.by = req, by
	if f.err != nil {
		return nil, f.err
	}
	return &schemas.RunnerTokenCreated{Token: "hmr_secret", Info: schemas.RunnerTokenView{ID: "rtk_2", OrgID: org, Pool: req.Pool},
		Command: "bifrost-bench runner start ...", Install: "curl ..."}, nil
}

func (f *fakeRunnerAdmin) RevokeToken(_ context.Context, org, id string) (*schemas.RunnerTokenView, error) {
	f.seen(org)
	return &schemas.RunnerTokenView{ID: id, OrgID: org, State: schemas.RunnerTokenRevoked}, f.err
}

type auditRow struct{ org, action, targetType, targetID string }

// call sends one request through h's routes as principal p (nil: none).
func call(t *testing.T, register func(*router.Router), p *auth.Principal, method, uri, body string, header ...string) *fasthttp.Response {
	t.Helper()
	r := router.New()
	register(r)
	var ctx fasthttp.RequestCtx
	ctx.Request.Header.SetMethod(method)
	ctx.Request.SetRequestURI(uri)
	for i := 0; i+1 < len(header); i += 2 {
		ctx.Request.Header.Set(header[i], header[i+1])
	}
	ctx.Request.SetBodyString(body)
	if p != nil {
		auth.SetPrincipal(&ctx, p)
	}
	r.Handler(&ctx)
	resp := &fasthttp.Response{}
	ctx.Response.CopyTo(resp)
	return resp
}

func TestRunnersAdminRoutes(t *testing.T) {
	api := &fakeRunnerAdmin{}
	var audits []auditRow
	h := NewRunnersHandler(schemas.ModeLive, api, func(_ *fasthttp.RequestCtx, org, action, tt, tid string, details map[string]any) {
		if strings.Contains(fmt.Sprint(details), "hmr_secret") {
			t.Fatal("the token reached the audit log")
		}
		audits = append(audits, auditRow{org, action, tt, tid})
	}).WithInstallScript([]byte("#!/usr/bin/env bash\n"))
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleAdmin}

	for _, tc := range []struct {
		name   string
		p      *auth.Principal
		status int
	}{
		{"anonymous", auth.Anonymous(), 401},
		{"no principal", nil, 401},
		{"runner", &auth.Principal{Kind: auth.KindRunner, OrgID: "org_a", TokenID: "rtk_1", Pool: "gpu"}, 401},
		{"member", &auth.Principal{Kind: auth.KindUser, UserID: "usr_2", OrgID: "org_a", Role: auth.RoleMember}, 403},
		{"viewer token", &auth.Principal{Kind: auth.KindToken, TokenID: "tok_1", OrgID: "org_a", Role: auth.RoleViewer}, 403},
	} {
		for _, rt := range [][2]string{{"GET", "/api/bench/runners"}, {"POST", "/api/bench/runners/tokens"}, {"POST", "/api/bench/runners/tokens/rtk_1/revoke"},
			{"GET", "/api/bench/runners/tokens"}, {"GET", "/api/bench/runners/rnr_1"}, {"POST", "/api/bench/runners/rnr_1/remove"}} {
			if resp := call(t, reg, tc.p, rt[0], rt[1], ""); resp.StatusCode() != tc.status {
				t.Fatalf("%s %s %s: status %d, want %d", tc.name, rt[0], rt[1], resp.StatusCode(), tc.status)
			}
		}
	}
	if len(api.orgs) != 0 || len(audits) != 0 {
		t.Fatalf("refused calls reached the service: %v %v", api.orgs, audits)
	}

	resp := call(t, reg, admin, "GET", "/api/bench/runners", "")
	var list schemas.RunnersList
	if err := json.Unmarshal(resp.Body(), &list); err != nil || resp.StatusCode() != 200 || len(list.Runners) != 1 || list.Runners[0].OrgID != "org_a" {
		t.Fatalf("list %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, admin, "GET", "/api/bench/runners/tokens", "")
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"tokens":[{"id":"rtk_1"`) {
		t.Fatalf("tokens %d %s", resp.StatusCode(), resp.Body())
	}
	resp = call(t, reg, admin, "POST", "/api/bench/runners/tokens", `{"pool":"gpu","name":"box-01","expires_in_days":30}`)
	var created schemas.RunnerTokenCreated
	if err := json.Unmarshal(resp.Body(), &created); err != nil || resp.StatusCode() != 201 || created.Token != "hmr_secret" || created.Command == "" {
		t.Fatalf("create %d %s", resp.StatusCode(), resp.Body())
	}
	if string(resp.Header.Peek("Cache-Control")) != "no-store" || api.by != "usr_1" || api.req.Pool != "gpu" || api.req.ExpiresInDays != 30 {
		t.Fatalf("create: cache %q by %q req %+v", resp.Header.Peek("Cache-Control"), api.by, api.req)
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/runners/tokens", `{"pool":"gpu","secret":1}`); resp.StatusCode() != 400 {
		t.Fatalf("unknown field: %d", resp.StatusCode())
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/runners/tokens/rtk_1/revoke", ""); resp.StatusCode() != 200 {
		t.Fatalf("revoke %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "GET", "/api/bench/runners/rnr_1", ""); resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"id":"rnr_1"`) {
		t.Fatalf("get %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, reg, admin, "POST", "/api/bench/runners/rnr_1/remove", ""); resp.StatusCode() != 200 {
		t.Fatalf("remove %d %s", resp.StatusCode(), resp.Body())
	}
	want := []auditRow{{"org_a", "runner_token.create", "runner_token", "rtk_2"}, {"org_a", "runner_token.revoke", "runner_token", "rtk_1"},
		{"org_a", "runner.remove", "runner", "rnr_1"}}
	if fmt.Sprint(audits) != fmt.Sprint(want) {
		t.Fatalf("audits %v, want %v", audits, want)
	}
	for _, org := range api.orgs {
		if org != "org_a" {
			t.Fatalf("a call reached organization %q", org)
		}
	}
	// The install script is public and holds no secret.
	resp = call(t, reg, nil, "GET", "/api/bench/runners/install.sh", "")
	if resp.StatusCode() != 200 || !strings.HasPrefix(string(resp.Body()), "#!/usr/bin/env bash") {
		t.Fatalf("install script %d %s", resp.StatusCode(), resp.Body())
	}
	// Errors keep their meaning.
	for err, status := range map[error]int{schemas.ErrNotFound: 404, schemas.ErrWorkerRequest: 400, errors.New("disk"): 500} {
		api.err = fmt.Errorf("wrapped: %w", err)
		if resp := call(t, reg, admin, "POST", "/api/bench/runners/tokens/rtk_9/revoke", ""); resp.StatusCode() != status {
			t.Fatalf("%v: status %d, want %d", err, resp.StatusCode(), status)
		}
	}
	// Off the hosted platform the routes answer 503.
	off := NewRunnersHandler(schemas.ModeLive, nil, nil)
	if resp := call(t, func(r *router.Router) { off.RegisterRoutes(r) }, auth.Local(), "GET", "/api/bench/runners", ""); resp.StatusCode() != 503 {
		t.Fatalf("local mode: %d", resp.StatusCode())
	}
}

// hostedFake is a worker API of one hosted organization; it records the
// runner scope each call carried.
type hostedFake struct {
	fakeWorkerAPI
	scopes []schemas.RunnerScope
}

func (f *hostedFake) OrgID() string { return "org_a" }

func (f *hostedFake) Register(ctx context.Context, req *schemas.WorkerRegister) (*schemas.WorkerRegistered, error) {
	s, _ := schemas.RunnerScopeFrom(ctx)
	f.scopes = append(f.scopes, s)
	return f.fakeWorkerAPI.Register(ctx, req)
}

func (f *hostedFake) Lease(ctx context.Context, id string) (*schemas.WorkerLease, error) {
	s, _ := schemas.RunnerScopeFrom(ctx)
	f.scopes = append(f.scopes, s)
	return f.fakeWorkerAPI.Lease(ctx, id)
}

func TestWorkersHostedAuth(t *testing.T) {
	api := &hostedFake{}
	verify := func(_ context.Context, tok string) (schemas.RunnerScope, error) {
		switch tok {
		case "hmr_a":
			return schemas.RunnerScope{OrgID: "org_a", Pool: "cpu", TokenID: "rtk_v"}, nil
		case "hmr_b":
			return schemas.RunnerScope{OrgID: "org_b", Pool: "default", TokenID: "rtk_b"}, nil
		}
		return schemas.RunnerScope{}, errors.New("bad token")
	}
	plain := func(r *router.Router) { NewWorkersHandler(schemas.ModeLive, api).RegisterRoutes(r) }
	verified := func(r *router.Router) {
		NewWorkersHandler(schemas.ModeLive, api).WithRunnerVerifier(verify).RegisterRoutes(r)
	}
	runnerA := &auth.Principal{Kind: auth.KindRunner, OrgID: "org_a", TokenID: "rtk_1", Pool: "gpu"}
	runnerB := &auth.Principal{Kind: auth.KindRunner, OrgID: "org_b", TokenID: "rtk_2", Pool: "gpu"}
	user := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleOwner}
	body := `{"worker_id":"w1","slots":1}`

	if resp := call(t, plain, runnerA, "POST", "/api/bench/workers/register", body); resp.StatusCode() != 200 {
		t.Fatalf("runner of the org: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, plain, runnerB, "POST", "/api/bench/workers/register", body); resp.StatusCode() != 403 {
		t.Fatalf("runner of another org: %d", resp.StatusCode())
	}
	for name, p := range map[string]*auth.Principal{"user": user, "anonymous": auth.Anonymous(), "none": nil} {
		if resp := call(t, plain, p, "POST", "/api/bench/workers/lease", `{"worker_id":"w1"}`); resp.StatusCode() != 401 {
			t.Fatalf("%s: %d", name, resp.StatusCode())
		}
	}
	// The shared worker token means nothing to a hosted coordinator.
	if resp := call(t, plain, nil, "POST", "/api/bench/workers/lease", `{"worker_id":"w1"}`, "Authorization", "Bearer "+testWorkerToken); resp.StatusCode() != 401 {
		t.Fatalf("shared token on a hosted coordinator: %d", resp.StatusCode())
	}
	// Without the auth middleware, the handler's own verifier checks the bearer.
	if resp := call(t, verified, nil, "POST", "/api/bench/workers/lease", `{"worker_id":"w1"}`, "Authorization", "Bearer hmr_a"); resp.StatusCode() != 204 {
		t.Fatalf("verified token: %d %s", resp.StatusCode(), resp.Body())
	}
	if resp := call(t, verified, nil, "POST", "/api/bench/workers/lease", `{"worker_id":"w1"}`, "Authorization", "Bearer hmr_b"); resp.StatusCode() != 403 {
		t.Fatalf("verified token of another org: %d", resp.StatusCode())
	}
	if resp := call(t, verified, nil, "POST", "/api/bench/workers/lease", `{"worker_id":"w1"}`, "Authorization", "Bearer hmr_revoked"); resp.StatusCode() != 401 {
		t.Fatalf("revoked token: %d", resp.StatusCode())
	}
	want := []schemas.RunnerScope{{OrgID: "org_a", Pool: "gpu", TokenID: "rtk_1"}, {OrgID: "org_a", Pool: "cpu", TokenID: "rtk_v"}}
	if fmt.Sprint(api.scopes) != fmt.Sprint(want) {
		t.Fatalf("scopes %v, want %v", api.scopes, want)
	}
	// Scope errors from the coordinator.
	for err, status := range map[error]int{schemas.ErrWorkerScope: 403, schemas.ErrWorkerAuth: 401} {
		api.err = fmt.Errorf("wrapped: %w", err)
		if resp := call(t, plain, runnerA, "POST", "/api/bench/workers/leases/l_1/heartbeat", `{"worker_id":"w1"}`); resp.StatusCode() != status {
			t.Fatalf("%v: %d, want %d", err, resp.StatusCode(), status)
		}
	}
	api.err = nil
	resp := call(t, plain, runnerA, "GET", "/api/bench/workers/whoami", "")
	if resp.StatusCode() != 200 || !strings.Contains(string(resp.Body()), `"mode":"local"`) {
		t.Fatalf("whoami %d %s", resp.StatusCode(), resp.Body())
	}
}

// TestRemoveSharedRunnerAnswers409 (N12): removing the shared worker
// through the org's runner routes answers 409 with a plain sentence.
func TestRemoveSharedRunnerAnswers409(t *testing.T) {
	api := &fakeRunnerAdmin{err: runners.ErrShared}
	h := NewRunnersHandler(schemas.ModeLive, api, nil)
	reg := func(r *router.Router) { h.RegisterRoutes(r) }
	admin := &auth.Principal{Kind: auth.KindUser, UserID: "usr_1", OrgID: "org_a", Role: auth.RoleAdmin}
	resp := call(t, reg, admin, "POST", "/api/bench/runners/rnr_1/remove", "")
	body := string(resp.Body())
	if resp.StatusCode() != fasthttp.StatusConflict || !strings.Contains(body, "The shared worker is provided by this server and cannot be removed.") ||
		strings.Contains(body, "remove runner:") {
		t.Fatalf("remove shared runner: %d %s", resp.StatusCode(), body)
	}
}
