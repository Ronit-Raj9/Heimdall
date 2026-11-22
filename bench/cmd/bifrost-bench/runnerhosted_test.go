package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// TestHostedRunnersEndToEnd drives the runners area through the hosted
// server: the real auth middleware with the runner token verifier, the
// tenancy dispatcher and policy, the per-org coordinators and the admin
// routes with their audit rows. Users are the fixture's fake principals;
// runners use real minted tokens over HTTP.
func TestHostedRunnersEndToEnd(t *testing.T) {
	f := newHostedFixture(t)
	prev := orgLauncher
	orgLauncher = hostedOrgLauncher
	t.Cleanup(func() { orgLauncher = prev; auth.RegisterRunnerVerifier(nil) })
	cfg, err := auth.ConfigFrom(f.env.base.cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(cfg, auth.Deps{DB: f.d})
	if err != nil {
		t.Fatal(err)
	}
	real, fake := auth.Middleware(cfg, svc), f.env.authMW
	f.env.authMW = func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		r, fk := real(next), fake(next)
		return func(ctx *fasthttp.RequestCtx) {
			if len(ctx.Request.Header.Peek(testAsHeader)) > 0 {
				fk(ctx)
				return
			}
			r(ctx)
		}
	}
	if f.srv, err = newHostedServer(f.env); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	hs := &fasthttp.Server{Handler: f.srv.Handler()}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Shutdown() })
	url := "http://" + ln.Addr().String()
	ctx := context.Background()

	// Users go over HTTP too (the fixture's fake principals, by header): a
	// bare RequestCtx has no server, and the dispatcher hands the request
	// context to the database.
	do := func(method, path, as string, body any) resp {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, url+path, rd)
		if err != nil {
			t.Fatal(err)
		}
		if as != "" {
			req.Header.Set(testAsHeader, as)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return resp{status: res.StatusCode, body: string(b)}
	}
	create := func(as string, body map[string]any) schemas.RunnerTokenCreated {
		t.Helper()
		r := do("POST", "/api/bench/runners/tokens", as, body)
		var out schemas.RunnerTokenCreated
		if r.status != 201 || json.Unmarshal([]byte(r.body), &out) != nil || !strings.HasPrefix(out.Token, "hmr_") {
			t.Fatalf("%s creates a token: %d %s", as, r.status, trim(r.body))
		}
		return out
	}
	if r := do("POST", "/api/bench/runners/tokens", "vera", map[string]any{"pool": "gpu"}); r.status != 403 {
		t.Fatalf("viewer creates a token: %d", r.status)
	}
	tokA := create("alice", map[string]any{"pool": "gpu", "name": "box-a"})
	tokB := create("bob", map[string]any{})
	if !strings.Contains(tokA.Command, "-server https://heimdall.test") || !strings.Contains(tokA.Command, "-pool gpu -name box-a") {
		t.Fatalf("command %q", tokA.Command)
	}

	call := func(method, path, token, org, body string) (int, string) {
		t.Helper()
		req, err := http.NewRequestWithContext(ctx, method, url+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if org != "" {
			req.Header.Set(tenancy.OrgHeader, org)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	who, err := engine.FetchWhoami(ctx, url, tokA.Token, nil)
	if err != nil || who.Mode != "hosted" || who.OrgID != f.acme.ID || who.Pool != "gpu" || who.TokenID != tokA.Info.ID {
		t.Fatalf("whoami acme: %+v %v", who, err)
	}
	if who, err := engine.FetchWhoami(ctx, url, tokB.Token, nil); err != nil || who.OrgID != f.beta.ID || who.Pool != schemas.DefaultRunnerPool {
		t.Fatalf("whoami beta: %+v %v", who, err)
	}
	if s, b := call("POST", "/api/bench/workers/register", tokA.Token, "", `{"worker_id":"box-a","slots":2,"labels":["gpu"]}`); s != 200 {
		t.Fatalf("register: %d %s", s, b)
	}
	if s, _ := call("POST", "/api/bench/workers/register", tokA.Token, "", `{"worker_id":"box-x","slots":1,"pool":"default"}`); s != 403 {
		t.Fatalf("register outside the token's pool: %d", s)
	}
	if s, _ := call("POST", "/api/bench/workers/lease", tokA.Token, "", `{"worker_id":"box-a"}`); s != 204 {
		t.Fatalf("lease with nothing running: %d", s)
	}
	// acme's token never reaches beta: naming beta is refused, and beta's
	// runner does not know acme's runner.
	if s, _ := call("POST", "/api/bench/workers/lease", tokA.Token, "beta", `{"worker_id":"box-a"}`); s < 400 {
		t.Fatalf("acme token naming beta: %d", s)
	}
	if s, _ := call("POST", "/api/bench/workers/lease", tokB.Token, "", `{"worker_id":"box-a"}`); s != 404 {
		t.Fatalf("beta token for acme's runner: %d", s)
	}
	// A runner token is refused on every other route.
	if s, _ := call("GET", "/api/bench/runners", tokA.Token, "", ""); s != 403 && s != 401 {
		t.Fatalf("runner token on the admin list: %d", s)
	}

	var list schemas.RunnersList
	r := do("GET", "/api/bench/runners", "alice", nil)
	if r.status != 200 || json.Unmarshal([]byte(r.body), &list) != nil || len(list.Runners) != 1 {
		t.Fatalf("acme runners: %d %s", r.status, trim(r.body))
	}
	rn := list.Runners[0]
	if rn.Name != "box-a" || rn.Pool != "gpu" || rn.State != schemas.RunnerIdle || !rn.Live || rn.Slots != 2 || rn.TokenPrefix != tokA.Info.Prefix {
		t.Fatalf("acme runner %+v", rn)
	}
	if r := do("GET", "/api/bench/runners", "bob", nil); r.status != 200 || strings.Contains(r.body, "box-a") {
		t.Fatalf("beta sees acme's runner: %d %s", r.status, trim(r.body))
	}
	if r := do("POST", "/api/bench/runners/tokens/"+tokA.Info.ID+"/revoke", "bob", nil); r.status != 404 {
		t.Fatalf("beta revokes acme's token: %d", r.status)
	}
	if r := do("POST", "/api/bench/runners/"+rn.ID+"/remove", "bob", nil); r.status != 404 {
		t.Fatalf("beta removes acme's runner: %d", r.status)
	}

	// Revoked: refused at once, and the runner is dropped.
	if r := do("POST", "/api/bench/runners/tokens/"+tokA.Info.ID+"/revoke", "alice", nil); r.status != 200 || !strings.Contains(r.body, `"state":"revoked"`) {
		t.Fatalf("revoke: %d %s", r.status, trim(r.body))
	}
	if _, err := engine.FetchWhoami(ctx, url, tokA.Token, nil); !errors.Is(err, schemas.ErrWorkerAuth) {
		t.Fatalf("revoked token: %v", err)
	}
	if hub := runnersOf(f.env).hub; len(hub.Workers(f.acme.ID)) != 0 {
		t.Fatalf("revoked token's runner still registered: %+v", hub.Workers(f.acme.ID))
	}
	if r := do("POST", "/api/bench/runners/"+rn.ID+"/remove", "alice", nil); r.status != 200 {
		t.Fatalf("remove: %d %s", r.status, trim(r.body))
	}
	if r := do("GET", "/api/bench/runners", "alice", nil); !strings.Contains(r.body, `"runners":[]`) {
		t.Fatalf("runners after removal: %s", trim(r.body))
	}

	page, err := f.d.ListAudit(ctx, db.AuditFilter{OrgID: f.acme.ID})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range page.Entries {
		if strings.HasPrefix(e.Action, "runner") || strings.HasPrefix(e.Action, "workers") {
			actions = append(actions, e.Action)
		}
		if bytes.Contains([]byte(e.Details), []byte(tokA.Token)) {
			t.Fatal("a token reached the audit log")
		}
	}
	got := strings.Join(actions, ",")
	for _, want := range []string{"runner_token.create", "runner_token.revoke", "runner.remove", "workers.register"} {
		if !strings.Contains(got, want) {
			t.Fatalf("audit actions %s lack %s", got, want)
		}
	}
	if r := do("GET", "/api/bench/runners/install.sh", "", nil); r.status != 200 || !strings.HasPrefix(r.body, "#!") {
		t.Fatalf("install script: %d", r.status)
	}
}

// TestInstallRevisionFromEnv: a server built without version control
// information installs runners at HEIMDALL_BUILD_COMMIT, a plain commit id
// only.
func TestInstallRevisionFromEnv(t *testing.T) {
	if buildRevision() != "" {
		t.Skip("this test binary carries its own revision")
	}
	for v, want := range map[string]string{
		"":                  "",
		"0123ABCdef":        "0123abcdef",
		"main":              "",
		"abc12":             "",
		"0123abc; rm -rf /": "",
	} {
		t.Setenv(installCommitEnv, v)
		if got := installRevision(); got != want {
			t.Fatalf("installRevision(%q) = %q, want %q", v, got, want)
		}
	}
}
