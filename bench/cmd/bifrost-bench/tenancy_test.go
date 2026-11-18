package main

// Tenancy area (P3): cross-org isolation suite. Two orgs with real sealed
// evidence (copies of bench/testdata/recorded, never the originals), a
// public demo org, and the real hosted server: the real org builder,
// orgDeps, registry, dispatcher and policy table, with a test auth
// middleware in place of sessions. Table-driven over every registered route.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/server"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
	"github.com/valyala/fasthttp/fasthttputil"
)

const testAsHeader = "X-Test-As"

// hostedFixture is a hosted server with orgs acme (alice owner, vera
// viewer), beta (bob owner) and demo (public_read, no members).
type hostedFixture struct {
	t        *testing.T
	env      *hostedEnv
	srv      *server.Server
	d        *db.DB
	acme     *db.Org
	beta     *db.Org
	demo     *db.Org
	users    map[string]*db.User
	as       map[string]*auth.Principal
	aExp     string
	bExp     string
	aRuns    []string
	bRuns    []string
	aPair    string
	bPair    string
	starts   *startLog
	client   *fasthttp.Client
	routes   map[string][]string // org router routes
	platform map[string][]string
}

// startLog records the launcher Start calls each org runtime received.
type startLog struct {
	mu    sync.Mutex
	calls []string
}

func (s *startLog) add(c string) { s.mu.Lock(); s.calls = append(s.calls, c); s.mu.Unlock() }

// testLauncher is an org launcher that starts nothing: Start only checks the
// experiment exists in its own org's store and records the call.
type testLauncher struct {
	oa  *app
	log *startLog
}

func (l testLauncher) Plan(ctx context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	return l.oa.newPlanner().Plan(ctx, exp)
}
func (l testLauncher) Create(context.Context, *schemas.Manifest) error {
	return errors.New("create disabled in the isolation test")
}
func (l testLauncher) Start(ctx context.Context, expID string) error {
	if _, err := l.oa.store.LoadManifest(ctx, expID); err != nil {
		return err
	}
	l.log.add(l.oa.orgID + ":" + expID)
	return nil
}
func (l testLauncher) Events(string) (<-chan schemas.LiveEvent, func()) {
	ch := make(chan schemas.LiveEvent)
	close(ch)
	return ch, func() {}
}

// copyExperiments copies the named recorded experiments into a fresh folder.
func copyExperiments(t *testing.T, ids ...string) string {
	t.Helper()
	dst := t.TempDir()
	for _, id := range ids {
		if err := os.CopyFS(filepath.Join(dst, id), os.DirFS(filepath.Join("..", "..", "testdata", "recorded", id))); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func newHostedFixture(t *testing.T) *hostedFixture { return newHostedFixtureWith(t, false) }

// newHostedFixtureWith builds the fixture; realLauncher keeps the shipped
// orgLauncher (runners area: coordinator, remote only) instead of the
// recording test launcher.
func newHostedFixtureWith(t *testing.T, realLauncher bool) *hostedFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	if code := runInit([]string{dir}); code != exitOK {
		t.Fatalf("init exit %d", code)
	}
	if err := os.CopyFS(filepath.Join(dir, "benchmarks", "coding-core"), os.DirFS(filepath.Join("..", "..", "benchmarks", "coding-core"))); err != nil {
		t.Fatal(err)
	}
	a, err := newApp(ctx, filepath.Join(dir, "deploy", "bench.yaml"), appOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.cfg.Auth = lib.AuthConfig{Mode: lib.AuthModeHosted, PublicURL: "https://heimdall.test"}
	a.cfg.Demo.Org = "demo"
	a.logger = schemas.NoopLogger{}
	d := dbtest.Open(t)

	f := &hostedFixture{t: t, d: d, starts: &startLog{}, users: map[string]*db.User{}}
	if !realLauncher {
		prev := orgLauncher
		orgLauncher = func(_ *hostedEnv, oa *app, _ *store.Index) (handlers.Launcher, handlers.WorkerAPI) {
			return testLauncher{oa: oa, log: f.starts}, nil
		}
		t.Cleanup(func() { orgLauncher = prev })
	}

	f.env = newEnv(ctx, a, d, false)
	// The real auth service, so its routes are registered; requests still
	// authenticate through the test middleware below.
	authCfg, err := auth.ConfigFrom(a.cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	if f.env.auth, err = auth.New(authCfg, auth.Deps{DB: d}); err != nil {
		t.Fatal(err)
	}
	f.env.authCfg = authCfg
	for _, n := range []string{"alice", "vera", "bob", "mallory"} {
		u, err := d.CreateUser(ctx, db.User{Email: n + "@example.com", Name: n})
		if err != nil {
			t.Fatal(err)
		}
		f.users[n] = u
	}
	mk := func(slug, owner string, public bool) *db.Org {
		o, err := createOrg(ctx, d, f.env.layout, a.cfg, slug, strings.ToUpper(slug), owner, public)
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	f.acme = mk("acme", f.users["alice"].ID, false)
	f.beta = mk("beta", f.users["bob"].ID, false)
	f.demo = mk("demo", "", true)
	if err := d.AddMember(ctx, f.acme.ID, f.users["vera"].ID, db.RoleViewer); err != nil {
		t.Fatal(err)
	}
	f.aExp, f.bExp = "e_pilot_mixed_nc93", "e_smoke_sdoj"
	logger := schemas.NoopLogger{}
	for _, imp := range []struct {
		org string
		ids []string
	}{{"acme", []string{f.aExp}}, {"beta", []string{f.bExp}}, {"demo", []string{f.aExp, f.bExp}}} {
		rep, _, err := importIntoOrg(ctx, a.cfg, d, imp.org, copyExperiments(t, imp.ids...), logger)
		if err != nil || rep.Imported != len(imp.ids) || rep.Refused != 0 {
			t.Fatalf("import into %s: %+v, %v", imp.org, rep, err)
		}
	}
	f.aRuns, f.aPair = runsOf(t, f.env.layout, f.acme.ID, f.aExp)
	f.bRuns, f.bPair = runsOf(t, f.env.layout, f.beta.ID, f.bExp)

	f.as = map[string]*auth.Principal{
		"alice":   {Kind: auth.KindUser, UserID: f.users["alice"].ID, OrgID: f.acme.ID, Role: auth.RoleOwner},
		"vera":    {Kind: auth.KindUser, UserID: f.users["vera"].ID, OrgID: f.acme.ID, Role: auth.RoleViewer},
		"bob":     {Kind: auth.KindUser, UserID: f.users["bob"].ID, OrgID: f.beta.ID, Role: auth.RoleOwner},
		"mallory": {Kind: auth.KindUser, UserID: f.users["mallory"].ID},
		"tokA":    {Kind: auth.KindToken, TokenID: "tok_a", OrgID: f.acme.ID, Role: auth.RoleMember},
		"runA":    {Kind: auth.KindRunner, TokenID: "rnr_a", OrgID: f.acme.ID},
	}
	f.env.authMW = func(next fasthttp.RequestHandler) fasthttp.RequestHandler {
		return func(ctx *fasthttp.RequestCtx) {
			p := auth.Anonymous()
			if who := string(ctx.Request.Header.Peek(testAsHeader)); who != "" {
				q, ok := f.as[who]
				if !ok {
					ctx.SetStatusCode(599)
					return
				}
				cp := *q
				p = &cp
			}
			auth.SetPrincipal(ctx, p)
			next(ctx)
		}
	}
	f.srv, err = newHostedServer(f.env)
	if err != nil {
		t.Fatal(err)
	}
	// Served on an in-memory listener: handlers need a live RequestCtx.
	ln := fasthttputil.NewInmemoryListener()
	sctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.srv.Serve(sctx, ln) }()
	t.Cleanup(func() {
		cancel()
		_ = ln.Close() // a shutdown racing the first Accept would otherwise leave Serve waiting
		<-done
		_ = f.env.registry.Close()
	})
	f.client = &fasthttp.Client{Dial: func(string) (net.Conn, error) { return ln.Dial() }}
	if r := f.do("GET", "/healthz", "", nil, nil); r.status != 200 {
		t.Fatalf("healthz: %d %s", r.status, r.body)
	}
	f.routes = server.OrgRoutes(a.cfg)
	return f
}

// runsOf lists the sealed run ids of exp in org and one of its pairs.
func runsOf(t *testing.T, layout tenancy.Layout, orgID, exp string) ([]string, string) {
	t.Helper()
	p, err := layout.Paths(orgID)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.NewFSStore(p.StoreRoot, nil)
	if err != nil {
		t.Fatal(err)
	}
	ix := store.NewIndex(st)
	if err := ix.Rebuild(context.Background()); err != nil {
		t.Fatal(err)
	}
	es, _ := ix.Runs(exp)
	var ids []string
	pair := ""
	for _, e := range es {
		if e.Sealed {
			ids = append(ids, e.Run.RunID)
			if pair == "" && e.Slot.PairID != "" {
				pair = e.Slot.PairID
			}
		}
	}
	if len(ids) == 0 || pair == "" {
		t.Fatalf("org %s exp %s: %d runs, pair %q", orgID, exp, len(ids), pair)
	}
	return ids, pair
}

type resp struct {
	status int
	body   string
}

func (f *hostedFixture) do(method, path, as string, hdr map[string]string, body any) resp {
	f.t.Helper()
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.Header.SetMethod(method)
	req.SetRequestURI("http://127.0.0.1" + path)
	req.Header.SetHost("127.0.0.1")
	if as != "" {
		req.Header.Set(testAsHeader, as)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if body != nil {
		b, _ := json.Marshal(body)
		req.Header.SetContentType("application/json")
		req.SetBody(b)
	}
	res := &fasthttp.Response{}
	if err := f.client.DoTimeout(req, res, 30*time.Second); err != nil {
		f.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp{status: res.StatusCode(), body: string(res.Body())}
}

// fill replaces the path parameters of pattern with org B's ids.
func (f *hostedFixture) fill(pattern string, exp, run, pair string) string {
	out := pattern
	repl := func(param, v string) { out = strings.ReplaceAll(out, "{"+param+"}", v) }
	switch {
	case strings.Contains(out, "/experiments/{id}"):
		repl("id", exp)
	case strings.Contains(out, "/runs/{id}"):
		repl("id", run)
	case strings.Contains(out, "/pairs/{id}"):
		repl("id", pair)
	case strings.Contains(out, "/suites/{id}"):
		repl("id", "coding-core")
	default:
		repl("id", "x_missing")
	}
	repl("exp_id", exp)
	repl("name", "run.json")
	repl("artifact", "out")
	repl("signature", "sig")
	repl("task", "t01")
	repl("lease_id", "lease_x")
	return out
}

// platformServed reports whether the platform router answers pattern
// (health, doctor): those never reach an org router.
func (f *hostedFixture) platformServed(method, pattern string) bool {
	for _, p := range platformRoutes(f.t, f)[method] {
		if p == pattern {
			return true
		}
	}
	return false
}

func (f *hostedFixture) bMarkers() []string { return append([]string{f.bExp}, f.bRuns...) }

func containsAny(s string, subs []string) string {
	for _, x := range subs {
		if strings.Contains(s, x) {
			return x
		}
	}
	return ""
}

func sortedGETs(routes map[string][]string) []string {
	out := append([]string(nil), routes["GET"]...)
	sort.Strings(out)
	return out
}

// TestHostedIsolationReads: for every GET route of the org router, a member
// of acme asking for beta's ids never gets beta's data, whether through its
// own org or by naming beta in X-Heimdall-Org; bob, beta's owner, does.
func TestHostedIsolationReads(t *testing.T) {
	f := newHostedFixture(t)
	ownOK := 0
	for _, pat := range sortedGETs(f.routes) {
		if !strings.HasPrefix(pat, handlers.APIPrefix) || f.platformServed("GET", pat) {
			continue
		}
		path := f.fill(pat, f.bExp, f.bRuns[0], f.bPair)
		t.Run(pat, func(t *testing.T) {
			for _, who := range []string{"alice", "vera", "tokA"} {
				r := f.do("GET", path, who, nil, nil)
				if r.status >= 200 && r.status < 300 {
					if m := containsAny(r.body, f.bMarkers()); m != "" {
						t.Fatalf("%s read beta's %s through %s: %d %s", who, m, path, r.status, trim(r.body))
					}
				}
				if r.status == 500 {
					t.Errorf("%s %s: 500 %s", who, path, trim(r.body))
				}
				// Naming beta explicitly never reaches beta's router.
				r = f.do("GET", path, who, map[string]string{tenancy.OrgHeader: "beta"}, nil)
				if r.status != 404 && r.status != 403 {
					t.Fatalf("%s with %s: beta: %d %s", who, tenancy.OrgHeader, r.status, trim(r.body))
				}
				r = f.do("GET", path, who, map[string]string{tenancy.OrgHeader: f.beta.ID}, nil)
				if r.status != 404 && r.status != 403 {
					t.Fatalf("%s with %s: %s: %d", who, tenancy.OrgHeader, f.beta.ID, r.status)
				}
			}
			// The ids are real: beta's owner reads them.
			if r := f.do("GET", path, "bob", nil, nil); r.status == 200 {
				ownOK++
			}
		})
	}
	if ownOK < 15 {
		t.Fatalf("only %d routes answered 200 to beta's owner: the ids under test are not real", ownOK)
	}
	// The core detail routes: 200 for bob, 404 for alice.
	for _, path := range []string{
		"/api/bench/experiments/" + f.bExp, "/api/bench/experiments/" + f.bExp + "/verdict",
		"/api/bench/runs/" + f.bRuns[0], "/api/bench/runs/" + f.bRuns[0] + "/files/run.json",
		"/api/bench/pairs/" + f.bPair, "/api/bench/experiments/" + f.bExp + "/reviews",
	} {
		if r := f.do("GET", path, "bob", nil, nil); r.status != 200 {
			t.Errorf("bob %s: %d %s", path, r.status, trim(r.body))
		}
		if r := f.do("GET", path, "alice", nil, nil); r.status != 404 {
			t.Errorf("alice %s: %d, want 404", path, r.status)
		}
	}
	// Lists show only the caller's org.
	r := f.do("GET", "/api/bench/experiments", "alice", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, f.aExp) || strings.Contains(r.body, f.bExp) {
		t.Fatalf("alice's experiment list: %d %s", r.status, trim(r.body))
	}
}

// TestHostedIsolationWrites: writes across orgs are refused and change
// nothing; viewers cannot write.
func TestHostedIsolationWrites(t *testing.T) {
	f := newHostedFixture(t)
	review := map[string]any{"exp_id": f.bExp, "run_id": f.bRuns[0], "reviewer": "alice", "verdict": "pass", "reason": "x"}
	release := map[string]any{"exp_id": f.bExp, "approver": "alice", "reason": "x"}
	writes := []struct {
		method, path string
		body         any
	}{
		{"POST", "/api/bench/experiments/" + f.bExp + "/start", nil},
		{"POST", "/api/bench/experiments/" + f.bExp + "/cancel", nil},
		{"POST", "/api/bench/experiments/" + f.bExp + "/resume", nil},
		{"POST", "/api/bench/reviews", review},
		{"POST", "/api/bench/releases", release},
	}
	for _, w := range writes {
		for _, who := range []string{"alice", "tokA"} {
			if r := f.do(w.method, w.path, who, nil, w.body); r.status >= 200 && r.status < 300 {
				t.Errorf("%s %s %s across orgs: %d %s", who, w.method, w.path, r.status, trim(r.body))
			}
			if r := f.do(w.method, w.path, who, map[string]string{tenancy.OrgHeader: "beta"}, w.body); r.status != 404 {
				t.Errorf("%s %s %s naming beta: %d, want 404", who, w.method, w.path, r.status)
			}
		}
		if r := f.do(w.method, w.path, "vera", nil, w.body); r.status != 403 {
			t.Errorf("viewer %s %s: %d, want 403", w.method, w.path, r.status)
		}
		if r := f.do(w.method, w.path, "", nil, w.body); r.status != 401 {
			t.Errorf("anonymous %s %s: %d, want 401", w.method, w.path, r.status)
		}
	}
	for _, c := range f.starts.calls {
		if strings.HasSuffix(c, ":"+f.bExp) {
			t.Fatalf("a launcher started beta's experiment: %v", f.starts.calls)
		}
	}
	// Beta's review log is untouched.
	r := f.do("GET", "/api/bench/experiments/"+f.bExp+"/reviews", "bob", nil, nil)
	if r.status != 200 || strings.Contains(r.body, `"reviewer":"alice"`) {
		t.Fatalf("beta's review log: %d %s", r.status, trim(r.body))
	}
	// Own-org start reaches only the caller's own runtime.
	if r := f.do("POST", "/api/bench/experiments/"+f.aExp+"/start", "alice", nil, nil); r.status >= 300 {
		t.Fatalf("alice starting her own experiment: %d %s", r.status, trim(r.body))
	}
	if len(f.starts.calls) != 1 || f.starts.calls[0] != f.acme.ID+":"+f.aExp {
		t.Fatalf("start calls %v", f.starts.calls)
	}
	// The write left an audit row in acme, none in beta.
	rows, err := f.d.ListAudit(context.Background(), db.AuditFilter{OrgID: f.acme.ID, Action: "experiments.start"})
	if err != nil || len(rows.Entries) != 1 || rows.Entries[0].ActorID != f.users["alice"].ID {
		t.Fatalf("acme audit: %+v %v", rows, err)
	}
	rows, _ = f.d.ListAudit(context.Background(), db.AuditFilter{OrgID: f.beta.ID, Action: "experiments.*"})
	if len(rows.Entries) != 0 {
		t.Fatalf("beta audit has acme's writes: %+v", rows.Entries)
	}
}

// TestHostedPlatformIsolation covers the org, member, invitation and audit
// routes across orgs and roles.
func TestHostedPlatformIsolation(t *testing.T) {
	f := newHostedFixture(t)
	bob := f.users["bob"].ID
	alice := f.users["alice"].ID
	cases := []struct {
		name, method, path, as string
		body                   any
		want                   int
	}{
		{"own org", "GET", "/api/bench/orgs/acme", "alice", nil, 200},
		{"other org by slug", "GET", "/api/bench/orgs/beta", "alice", nil, 404},
		{"other org by id", "GET", "/api/bench/orgs/" + f.beta.ID, "alice", nil, 404},
		{"unknown org", "GET", "/api/bench/orgs/nope", "alice", nil, 404},
		{"other org members", "GET", "/api/bench/orgs/beta/members", "alice", nil, 404},
		{"other org audit", "GET", "/api/bench/orgs/beta/audit", "alice", nil, 404},
		{"other org invitations", "GET", "/api/bench/orgs/beta/invitations", "alice", nil, 404},
		{"rename other org", "POST", "/api/bench/orgs/beta/rename", "alice", map[string]any{"name": "pwned"}, 404},
		{"remove member of other org", "POST", "/api/bench/orgs/beta/members/" + bob + "/remove", "alice", nil, 404},
		{"role in other org", "POST", "/api/bench/orgs/beta/members/" + bob + "/role", "alice", map[string]any{"role": "viewer"}, 404},
		{"invite into other org", "POST", "/api/bench/orgs/beta/invitations", "alice", map[string]any{"email": "x@example.com", "role": "owner"}, 404},
		{"public read of other org", "POST", "/api/bench/orgs/beta/public-read", "alice", map[string]any{"public_read": true}, 404},
		{"token to other org", "GET", "/api/bench/orgs/beta", "tokA", nil, 404},
		{"viewer reads org", "GET", "/api/bench/orgs/acme", "vera", nil, 200},
		{"viewer members list", "GET", "/api/bench/orgs/acme/members", "vera", nil, 403},
		{"viewer rename", "POST", "/api/bench/orgs/acme/rename", "vera", map[string]any{"name": "v"}, 403},
		{"viewer audit", "GET", "/api/bench/orgs/acme/audit", "vera", nil, 403},
		{"anonymous private org", "GET", "/api/bench/orgs/acme", "", nil, 401},
		{"anonymous demo org", "GET", "/api/bench/orgs/demo", "", nil, 200},
		{"anonymous demo members", "GET", "/api/bench/orgs/demo/members", "", nil, 401},
		{"anonymous creates org", "POST", "/api/bench/orgs", "", map[string]any{"slug": "anon"}, 401},
		{"token creates org", "POST", "/api/bench/orgs", "tokA", map[string]any{"slug": "tok"}, 403},
		{"runner on org route", "GET", "/api/bench/orgs/acme", "runA", nil, 403},
		{"last owner demoted", "POST", "/api/bench/orgs/acme/members/" + alice + "/role", "alice", map[string]any{"role": "viewer"}, 409},
		{"last owner removed", "POST", "/api/bench/orgs/acme/members/" + alice + "/remove", "alice", nil, 409},
		{"owner renames", "POST", "/api/bench/orgs/acme/rename", "alice", map[string]any{"name": "Acme Inc"}, 200},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if r := f.do(c.method, c.path, c.as, nil, c.body); r.status != c.want {
				t.Fatalf("%d, want %d: %s", r.status, c.want, trim(r.body))
			}
		})
	}
	// Beta was not renamed; acme's audit holds the rename, beta's does not.
	if o, _ := f.d.OrgByID(context.Background(), f.beta.ID); o.Name != "BETA" {
		t.Fatalf("beta renamed to %q", o.Name)
	}
	r := f.do("GET", "/api/bench/orgs/acme/audit", "alice", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, "org.rename") {
		t.Fatalf("acme audit: %d %s", r.status, trim(r.body))
	}
	r = f.do("GET", "/api/bench/orgs/beta/audit", "bob", nil, nil)
	if r.status != 200 || strings.Contains(r.body, "org.rename") || strings.Contains(r.body, alice) {
		t.Fatalf("beta audit shows acme's events: %s", trim(r.body))
	}
	// My orgs: alice sees acme only; mallory none; members of acme only.
	r = f.do("GET", "/api/bench/orgs", "alice", nil, nil)
	if !strings.Contains(r.body, `"slug":"acme"`) || strings.Contains(r.body, "beta") || strings.Contains(r.body, `"slug":"demo"`) {
		t.Fatalf("alice's orgs: %s", trim(r.body))
	}
	if r = f.do("GET", "/api/bench/orgs", "mallory", nil, nil); r.status != 200 || strings.Contains(r.body, "acme") {
		t.Fatalf("mallory's orgs: %d %s", r.status, trim(r.body))
	}
	r = f.do("GET", "/api/bench/orgs/acme/members", "alice", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, "vera@") || strings.Contains(r.body, "bob@") {
		t.Fatalf("acme members: %d %s", r.status, trim(r.body))
	}
}

// TestHostedAnonymousAndRunners: anonymous visitors read the demo org and
// nothing else; runners reach only their org's worker routes.
func TestHostedAnonymousAndRunners(t *testing.T) {
	f := newHostedFixture(t)
	r := f.do("GET", "/api/bench/experiments", "", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, f.aExp) || !strings.Contains(r.body, f.bExp) {
		t.Fatalf("anonymous demo list: %d %s", r.status, trim(r.body))
	}
	for _, path := range sortedGETs(f.routes) {
		if !strings.HasPrefix(path, handlers.APIPrefix) || f.platformServed("GET", path) {
			continue
		}
		p := f.fill(path, f.bExp, f.bRuns[0], f.bPair)
		for _, org := range []string{"acme", "beta", f.acme.ID} {
			if r := f.do("GET", p, "", map[string]string{tenancy.OrgHeader: org}, nil); r.status != 401 {
				t.Errorf("anonymous %s on %s: %d, want 401", p, org, r.status)
			}
		}
	}
	for method, pats := range f.routes {
		if method == "GET" || method == "HEAD" {
			continue
		}
		for _, pat := range pats {
			p := f.fill(pat, f.bExp, f.bRuns[0], f.bPair)
			if r := f.do(method, p, "", nil, map[string]any{}); r.status != 401 && r.status != 403 {
				t.Errorf("anonymous %s %s on the demo: %d", method, p, r.status)
			}
		}
	}
	// Non-member signed-in users read the demo too, and only read.
	if r := f.do("GET", "/api/bench/experiments", "mallory", map[string]string{tenancy.OrgHeader: "demo"}, nil); r.status != 200 {
		t.Fatalf("mallory reading the demo: %d", r.status)
	}
	if r := f.do("POST", "/api/bench/experiments/"+f.bExp+"/start", "mallory", map[string]string{tenancy.OrgHeader: "demo"}, nil); r.status != 403 {
		t.Fatalf("mallory writing the demo: %d", r.status)
	}
	if r := f.do("GET", "/api/bench/experiments", "mallory", nil, nil); r.status != 403 {
		t.Fatalf("mallory with no org: %d", r.status)
	}
	// Runners.
	if r := f.do("GET", "/api/bench/experiments", "runA", nil, nil); r.status != 403 {
		t.Fatalf("runner on a data route: %d", r.status)
	}
	if r := f.do("POST", "/api/bench/workers/lease", "runA", map[string]string{tenancy.OrgHeader: "beta"}, map[string]any{}); r.status != 404 {
		t.Fatalf("runner naming beta: %d", r.status)
	}
	if r := f.do("POST", "/api/bench/workers/lease", "runA", nil, map[string]any{}); r.status == 401 || r.status == 403 || r.status == 404 {
		t.Fatalf("runner on its own worker route refused by policy: %d %s", r.status, trim(r.body))
	}
	if r := f.do("POST", "/api/bench/workers/lease", "alice", nil, map[string]any{}); r.status != 403 {
		t.Fatalf("user on a worker route: %d", r.status)
	}
}

// TestHostedPlanGuard: a hosted org's planner refuses paths outside its
// project before anything is read.
func TestHostedPlanGuard(t *testing.T) {
	f := newHostedFixture(t)
	p, _ := f.env.layout.Paths(f.acme.ID)
	noop, err := os.ReadFile(filepath.Join("..", "..", "agents", "noop.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ProjectRoot, "agents", "noop.yaml"), noop, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, exp := range []map[string]any{
		{"name": "x", "suite": "../../../benchmarks/coding-core", "base": "agents/noop.yaml"},
		{"name": "x", "suite": "coding-core", "base": "/etc/passwd"},
		{"name": "x", "suite": "coding-core", "base": "../" + f.beta.ID + "/agents/noop.yaml"},
		{"name": "x", "suite": "coding-core", "base": "agents/noop.yaml", "policy": "../../orgs/" + f.beta.ID + "/x"},
	} {
		r := f.do("POST", "/api/bench/experiments/plan", "alice", nil, exp)
		if !strings.Contains(r.body, "organization") && !strings.Contains(r.body, "leaves the folder") && !strings.Contains(r.body, "relative") {
			t.Errorf("plan %v: %d %s", exp, r.status, trim(r.body))
		}
	}
}

// firstID returns the first "id" string anywhere in a JSON body.
func firstID(t *testing.T, body string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode %s: %v", trim(body), err)
	}
	var walk func(any) string
	walk = func(x any) string {
		switch m := x.(type) {
		case map[string]any:
			if id, ok := m["id"].(string); ok && id != "" {
				return id
			}
			keys := make([]string, 0, len(m))
			for k := range m {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if id := walk(m[k]); id != "" {
					return id
				}
			}
		case []any:
			for _, e := range m {
				if id := walk(e); id != "" {
					return id
				}
			}
		}
		return ""
	}
	id := walk(v)
	if id == "" {
		t.Fatalf("no id in %s", trim(body))
	}
	return id
}

// TestHostedPlatformDataIsolation: beta's jobs, runner tokens and API
// tokens are invisible and untouchable from acme, through acme's own scope
// or by naming beta.
func TestHostedPlatformDataIsolation(t *testing.T) {
	f := newHostedFixtureWith(t, true) // the shipped launcher: the jobs area finds the org's launcher through it
	mk := func(path string, body any) string {
		r := f.do("POST", path, "bob", nil, body)
		if r.status != 201 && r.status != 200 && r.status != 202 {
			t.Fatalf("bob POST %s: %d %s", path, r.status, trim(r.body))
		}
		return firstID(t, r.body)
	}
	runnerTok := mk("/api/bench/runners/tokens", map[string]any{"pool": "default", "name": "beta-box"})
	apiTok := mk("/api/bench/auth/tokens", map[string]any{"name": "beta-ci"})
	job := mk("/api/bench/jobs", map[string]any{"kind": "index.rebuild"})
	secrets := []string{runnerTok, apiTok, job, "beta-box", "beta-ci"}

	for _, path := range []string{"/api/bench/runners/tokens", "/api/bench/auth/tokens", "/api/bench/jobs", "/api/bench/runners", "/api/bench/orgs/acme/audit"} {
		r := f.do("GET", path, "alice", nil, nil)
		if r.status != 200 {
			t.Errorf("alice GET %s: %d %s", path, r.status, trim(r.body))
		}
		if m := containsAny(r.body, secrets); m != "" {
			t.Errorf("alice GET %s shows beta's %s", path, m)
		}
		r = f.do("GET", path, "alice", map[string]string{tenancy.OrgHeader: "beta"}, nil)
		switch {
		case strings.HasPrefix(path, "/api/bench/orgs/"):
			// The path names the org; the header does not override it.
			if m := containsAny(r.body, secrets); m != "" || r.status != 200 {
				t.Errorf("alice GET %s naming beta: %d, leaks %q", path, r.status, m)
			}
		case r.status != 404:
			t.Errorf("alice GET %s naming beta: %d", path, r.status)
		}
	}
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/bench/jobs/" + job}, {"GET", "/api/bench/jobs/" + job + "/logs"},
		{"POST", "/api/bench/jobs/" + job + "/cancel"},
		{"POST", "/api/bench/runners/tokens/" + runnerTok + "/revoke"},
		{"POST", "/api/bench/auth/tokens/" + apiTok + "/revoke"},
	} {
		for _, who := range []string{"alice", "tokA"} {
			r := f.do(c.method, c.path, who, nil, map[string]any{})
			if r.status >= 200 && r.status < 300 {
				t.Errorf("%s %s %s: %d %s", who, c.method, c.path, r.status, trim(r.body))
			}
			if m := containsAny(r.body, []string{"beta-box", "beta-ci"}); m != "" {
				t.Errorf("%s %s %s leaks %s", who, c.method, c.path, m)
			}
			if r := f.do(c.method, c.path, who, map[string]string{tenancy.OrgHeader: "beta"}, map[string]any{}); r.status != 404 {
				t.Errorf("%s %s %s naming beta: %d", who, c.method, c.path, r.status)
			}
		}
	}
	// Viewers and anonymous visitors never reach the admin lists.
	for _, path := range []string{"/api/bench/runners/tokens", "/api/bench/auth/tokens", "/api/bench/runners"} {
		if r := f.do("GET", path, "vera", nil, nil); r.status != 403 {
			t.Errorf("viewer GET %s: %d", path, r.status)
		}
		if r := f.do("GET", path, "", nil, nil); r.status != 401 {
			t.Errorf("anonymous GET %s: %d", path, r.status)
		}
	}
	// The ids are real: bob sees them.
	if r := f.do("GET", "/api/bench/jobs/"+job, "bob", nil, nil); r.status != 200 {
		t.Fatalf("bob GET his job: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/runners/tokens", "bob", nil, nil); !strings.Contains(r.body, runnerTok) {
		t.Fatalf("bob's runner tokens: %s", trim(r.body))
	}
}

// TestHostedRunnerReachesWorkerAPI: with the shipped org launcher a runner
// principal is dispatched to its own org runtime, whose worker API is that
// org's coordinator: never the 503 of a server without worker mode.
func TestHostedRunnerReachesWorkerAPI(t *testing.T) {
	f := newHostedFixtureWith(t, true)
	f.as["runA"].Pool = "default"
	r := f.do("GET", "/api/bench/workers/whoami", "runA", nil, nil)
	if r.status == 503 || r.status == 401 || r.status == 403 || r.status == 404 {
		t.Fatalf("runner whoami: %d %s", r.status, trim(r.body))
	}
	if !strings.Contains(r.body, f.acme.ID) && !strings.Contains(r.body, "default") {
		t.Errorf("whoami does not name the runner's scope: %s", trim(r.body))
	}
	if r := f.do("POST", "/api/bench/workers/lease", "runA", nil, map[string]any{"worker_id": "w1"}); r.status == 503 {
		t.Fatalf("runner lease: 503 %s", trim(r.body))
	}
	if r := f.do("GET", "/api/bench/workers/whoami", "runA", map[string]string{tenancy.OrgHeader: "beta"}, nil); r.status != 404 {
		t.Fatalf("runner naming beta: %d", r.status)
	}
	if r := f.do("GET", "/api/bench/workers/whoami", "alice", nil, nil); r.status != 403 {
		t.Fatalf("user on whoami: %d", r.status)
	}
}

// TestHostedPlanDefersGatewayToRunner: the hosted server has no gateway;
// planning, freezing and starting an experiment still work, the manifest
// records gateway.deferred_to_runner, and a runner of the org is offered the
// slot over HTTP. (engine.TestRemoteDeferredGateway runs the slot on a
// runner, uploads it with the runner's preflight record and evaluates
// gateway_hygiene from it.)
func TestHostedPlanDefersGatewayToRunner(t *testing.T) {
	f := newHostedFixtureWith(t, true)
	if f.env.base.reg.Gateway != nil {
		t.Fatal("the fixture's hosted app has a gateway")
	}
	p, _ := f.env.layout.Paths(f.acme.ID)
	toy, err := os.ReadFile(filepath.Join("..", "..", "agents", "toy.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ProjectRoot, "agents", "toy.yaml"), toy, 0o644); err != nil {
		t.Fatal(err)
	}
	exp := `name: hosted-smoke
smoke: true
suite: coding-core
base: agents/toy.yaml
arms:
  A: {label: toy}
  B: {label: toy with a shorter wall clock, set: {budgets.wall_s: 600}}
primary: {condition: clean, metric: verified_success}
conditions:
  - {id: clean, tasks: [t01], trials: 1}
budget: {total_usd: 0}
verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}
seed: 20261118
concurrency: 1
`
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)
	req.Header.SetMethod("POST")
	req.SetRequestURI("http://127.0.0.1/api/bench/experiments")
	req.Header.SetHost("127.0.0.1")
	req.Header.Set(testAsHeader, "alice")
	req.Header.SetContentType("application/yaml")
	req.SetBodyString(exp)
	res := &fasthttp.Response{}
	if err := f.client.DoTimeout(req, res, 60*time.Second); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode() != 201 {
		t.Fatalf("POST /experiments: %d %s", res.StatusCode(), trim(string(res.Body())))
	}
	var created struct {
		ExpID string `json:"exp_id"`
	}
	if err := json.Unmarshal(res.Body(), &created); err != nil || created.ExpID == "" {
		t.Fatalf("created %s: %v", trim(string(res.Body())), err)
	}
	raw, err := os.ReadFile(filepath.Join(p.StoreRoot, created.ExpID, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m schemas.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !m.Gateway.Deferred() || !strings.Contains(string(raw), `"deferred_to_runner": true`) {
		t.Fatalf("manifest gateway %+v", m.Gateway)
	}
	if r := f.do("POST", "/api/bench/experiments/"+created.ExpID+"/start", "alice", nil, nil); r.status != 202 && r.status != 200 {
		t.Fatalf("start: %d %s", r.status, trim(r.body))
	}
	f.as["runA"].Pool = schemas.DefaultRunnerPool
	if r := f.do("POST", "/api/bench/workers/register", "runA", nil, map[string]any{"worker_id": "box-a", "slots": 1}); r.status != 200 {
		t.Fatalf("register: %d %s", r.status, trim(r.body))
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		r := f.do("POST", "/api/bench/workers/lease", "runA", nil, map[string]any{"worker_id": "box-a"})
		if r.status == 200 {
			var l schemas.WorkerLease
			if err := json.Unmarshal([]byte(r.body), &l); err != nil || l.ExpID != created.ExpID || l.ManifestHash != m.Hash {
				t.Fatalf("lease %s: %v", trim(r.body), err)
			}
			break
		}
		if r.status != 204 || time.Now().After(deadline) {
			t.Fatalf("lease: %d %s", r.status, trim(r.body))
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The runner fetches the frozen manifest with the deferred record.
	if r := f.do("GET", "/api/bench/workers/manifests/"+created.ExpID+"?worker_id=box-a", "runA", nil, nil); r.status != 200 || !strings.Contains(r.body, `"deferred_to_runner":true`) {
		t.Fatalf("worker manifest: %d %s", r.status, trim(r.body))
	}
}

// TestHostedEveryOrgGetsTheSuite: an org made through the API has the
// built-in suite at once; one made by signup gets it when its runtime is
// first built; an org that deletes it does not get it back.
func TestHostedEveryOrgGetsTheSuite(t *testing.T) {
	f := newHostedFixture(t)
	ctx := context.Background()
	suiteOf := func(orgID string) string {
		p, _ := f.env.layout.Paths(orgID)
		return filepath.Join(p.BenchmarksRoot(), tenancy.DefaultSuite, "suite.yaml")
	}
	r := f.do("POST", "/api/bench/orgs", "mallory", nil, map[string]any{"slug": "mallory-co"})
	if r.status != 201 {
		t.Fatalf("create org: %d %s", r.status, trim(r.body))
	}
	if _, err := os.Stat(suiteOf(firstID(t, r.body))); err != nil {
		t.Fatalf("API-created org has no %s: %v", tenancy.DefaultSuite, err)
	}

	cfg := *f.env.base.cfg
	cfg.Auth.Signup = lib.SignupOpen
	authCfg, err := auth.ConfigFrom(&cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := auth.New(authCfg, auth.Deps{DB: f.d})
	if err != nil {
		t.Fatal(err)
	}
	u, _, err := svc.Signup(ctx, auth.SignupRequest{Email: "sue@example.com", Password: "correct horse battery staple 42", OrgName: "Sue Co"}, "127.0.0.9", "test", "")
	if err != nil {
		t.Fatal(err)
	}
	orgs, err := f.d.OrgsForUser(ctx, u.ID)
	if err != nil || len(orgs) != 1 {
		t.Fatalf("sue's orgs %+v %v", orgs, err)
	}
	sue := orgs[0].ID
	f.as["sue"] = &auth.Principal{Kind: auth.KindUser, UserID: u.ID, OrgID: sue, Role: auth.RoleOwner}
	r = f.do("GET", "/api/bench/suites", "sue", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, tenancy.DefaultSuite) {
		t.Fatalf("signup org suites: %d %s", r.status, trim(r.body))
	}
	if _, err := os.Stat(suiteOf(sue)); err != nil {
		t.Fatalf("signup org has no %s: %v", tenancy.DefaultSuite, err)
	}
	// Deleted by the org: not seeded again on the next build.
	if err := os.RemoveAll(filepath.Dir(suiteOf(sue))); err != nil {
		t.Fatal(err)
	}
	f.env.registry.Evict(sue)
	if r := f.do("GET", "/api/bench/suites", "sue", nil, nil); r.status != 200 || strings.Contains(r.body, tenancy.DefaultSuite) {
		t.Fatalf("deleted suite came back: %d %s", r.status, trim(r.body))
	}
}

// TestHostedLeaveAndDemoFlag: members leave for themselves only, never as
// the last owner; the demo org is flagged in org answers.
func TestHostedLeaveAndDemoFlag(t *testing.T) {
	f := newHostedFixture(t)
	for _, c := range []struct {
		name, path, as string
		want           int
	}{
		{"non-member leaves another org", "/api/bench/orgs/acme/leave", "mallory", 404},
		{"token leaves", "/api/bench/orgs/acme/leave", "tokA", 403},
		{"anonymous leaves the demo", "/api/bench/orgs/demo/leave", "", 401},
		{"non-member leaves the public demo", "/api/bench/orgs/demo/leave", "mallory", 403},
		{"last owner leaves", "/api/bench/orgs/acme/leave", "alice", 409},
		{"viewer leaves", "/api/bench/orgs/acme/leave", "vera", 204},
		{"former member", "/api/bench/orgs/acme", "vera", 404},
	} {
		if r := f.do("POST", c.path, c.as, nil, nil); c.name != "former member" && r.status != c.want {
			t.Errorf("%s: %d, want %d: %s", c.name, r.status, c.want, trim(r.body))
		}
		if c.name == "former member" {
			if r := f.do("GET", c.path, c.as, nil, nil); r.status != c.want {
				t.Errorf("%s: %d, want %d", c.name, r.status, c.want)
			}
		}
	}
	if ms, _ := f.d.Members(context.Background(), f.acme.ID); len(ms) != 1 {
		t.Fatalf("acme members after vera left: %+v", ms)
	}
	r := f.do("GET", "/api/bench/orgs/demo", "", nil, nil)
	if r.status != 200 || !strings.Contains(r.body, `"demo":true`) {
		t.Fatalf("demo org: %d %s", r.status, trim(r.body))
	}
	if r := f.do("GET", "/api/bench/orgs/acme", "alice", nil, nil); strings.Contains(r.body, `"demo"`) {
		t.Fatalf("acme flagged as demo: %s", trim(r.body))
	}
}

// TestHostedPolicyCoversEveryRoute fails when the hosted server registers a
// route without an entry in tenancy/policy.go.
func TestHostedPolicyCoversEveryRoute(t *testing.T) {
	f := newHostedFixture(t)
	pol := tenancy.DefaultPolicy()
	var missing []string
	for method, pats := range f.routes {
		for _, p := range pats {
			m := method
			if m == "HEAD" {
				m = "GET"
			}
			if !strings.HasPrefix(p, handlers.APIPrefix) {
				continue // site-root routes (healthz, readyz) are platform routes
			}
			if _, ok := pol.OrgRule(m, p); !ok {
				missing = append(missing, "org      "+method+" "+p)
			}
		}
	}
	plat := platformRoutes(t, f)
	for method, pats := range plat {
		for _, p := range pats {
			m := method
			if m == "HEAD" {
				m = "GET"
			}
			if _, ok := pol.PlatformRule(m, p); !ok {
				missing = append(missing, "platform "+method+" "+p)
			}
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Fatalf("%d route(s) without a policy entry in tenancy/policy.go:\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	// A table entry for a route nobody registers is stale: it documents a
	// decision about nothing.
	registered := func(routes map[string][]string, k string) bool {
		method, pat, _ := strings.Cut(k, " ")
		for _, m := range []string{method, "HEAD"} {
			for _, p := range routes[m] {
				if p == pat && (m == method || method == "GET") {
					return true
				}
			}
		}
		return false
	}
	platformKeys, orgKeys := pol.Entries()
	var stale []string
	for _, k := range platformKeys {
		if !registered(plat, k) {
			stale = append(stale, "platform "+k)
		}
	}
	for _, k := range orgKeys {
		if !registered(f.routes, k) {
			stale = append(stale, "org      "+k)
		}
	}
	if len(stale) > 0 {
		t.Fatalf("%d stale entries in tenancy/policy.go (no such route):\n  %s", len(stale), strings.Join(stale, "\n  "))
	}
}

// platformRoutes lists the routes the hosted server serves directly.
func platformRoutes(t *testing.T, f *hostedFixture) map[string][]string {
	t.Helper()
	var areas []server.RouteRegistrar
	for _, mk := range hostedAreas {
		r, err := mk(f.env)
		if err != nil {
			t.Fatal(err)
		}
		if r != nil {
			areas = append(areas, r)
		}
	}
	return server.PlatformRoutes(f.env.base.cfg, areas)
}

func trim(s string) string {
	if len(s) > 300 {
		return s[:300] + "..."
	}
	return s
}
