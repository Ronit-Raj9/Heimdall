package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// gwTestSource is a leaderboard.Source over in-memory manifests and runs.
type gwTestSource struct {
	manifests map[string]*schemas.Manifest
	runs      map[string][]leaderboard.Run
}

func (s gwTestSource) Experiments() []string { return nil }
func (s gwTestSource) Manifest(id string) (*schemas.Manifest, bool) {
	m, ok := s.manifests[id]
	return m, ok
}
func (s gwTestSource) Runs(id string) []leaderboard.Run { return s.runs[id] }
func (s gwTestSource) RunFile(runID, name string) ([]byte, error) {
	return nil, fmt.Errorf("%s: %w", name, fs.ErrNotExist)
}
func (s gwTestSource) LatestDecision(string) (schemas.Decision, bool) { return "", false }

func gatewayTestsRouter(env OrgEnv, src leaderboard.Source) fasthttp.RequestHandler {
	r := router.New()
	h := newGatewayTestsHandler(env)
	if src != nil {
		h.source = func() leaderboard.Source { return src }
	}
	h.routes(r)
	return r.Handler
}

var gwDraftCalls int

func init() {
	gwfeature.Register(gwfeature.Feature{ID: "w4p1_test_draft", Name: "Test draft", Draft: func(ctx context.Context, env gwfeature.DraftEnv, req gwfeature.DraftRequest) (*schemas.Experiment, []string, error) {
		gwDraftCalls++
		if req.Options["refuse"] != "" {
			return nil, nil, fmt.Errorf("pick a benchmark with at least five tasks")
		}
		return &schemas.Experiment{Suite: req.Suite, Base: req.Base, Arms: map[string]schemas.ArmSpec{"A": {Label: "today"}, "B": {Label: "changed"}}}, []string{"A note."}, nil
	}})
}

func TestDraftRefusesBudgetAboveCap(t *testing.T) {
	h := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, nil)
	gwDraftCalls = 0
	for _, body := range []string{
		`{"feature":"w4p1_test_draft","suite":"s@1","base":"agents/a.yaml","budget_usd":51}`,
		`{"feature":"w4p1_test_draft","suite":"s@1","base":"agents/a.yaml","budget_usd":0}`,
		`{"feature":"w4p1_test_draft","suite":"s@1","base":"agents/a.yaml","budget_usd":5,"trials":6}`,
		`{"feature":"w4p1_test_draft","suite":"s@1","base":"agents/a.yaml","budget_usd":5,"surprise":1}`,
	} {
		if st, out := secretsCall(h, "POST", APIPrefix+"/gateway/tests/draft", body); st != 400 {
			t.Errorf("%s: %d %s", body, st, out)
		}
	}
	if gwDraftCalls != 0 {
		t.Fatalf("draft called %d times for refused requests", gwDraftCalls)
	}
	st, out := secretsCall(h, "POST", APIPrefix+"/gateway/tests/draft", `{"feature":"w4p1_test_draft","suite":"s@1","base":"agents/a.yaml","budget_usd":5}`)
	if st != 200 {
		t.Fatalf("%d %s", st, out)
	}
	var resp struct {
		Experiment schemas.Experiment `json:"experiment"`
		Notes      []string           `json:"notes"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	e := resp.Experiment
	if e.GatewayTest == nil || e.GatewayTest.Feature != "w4p1_test_draft" || e.GatewayTest.Control != "A" || len(e.GatewayTest.Variants) != 1 || e.GatewayTest.Variants[0] != "B" {
		t.Fatalf("gateway test: %+v", e.GatewayTest)
	}
	if e.Budget.TotalUSD != 5 || e.Name != "Bifrost test: Test draft" || len(resp.Notes) != 1 {
		t.Fatalf("draft: %+v %v", e, resp.Notes)
	}
	if st, out := secretsCall(h, "POST", APIPrefix+"/gateway/tests/draft", `{"feature":"w4p1_test_draft","suite":"s@1","base":"a","budget_usd":5,"options":{"refuse":"1"}}`); st != 400 || !strings.Contains(out, "at least five tasks") {
		t.Fatalf("refusing draft: %d %s", st, out)
	}
}

func TestDraftUnknownFeature400(t *testing.T) {
	h := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, nil)
	st, out := secretsCall(h, "POST", APIPrefix+"/gateway/tests/draft", `{"feature":"nope","suite":"s@1","base":"a","budget_usd":5}`)
	if st != 400 || !strings.Contains(out, "nope") {
		t.Fatalf("%d %s", st, out)
	}
	st, out = secretsCall(h, "GET", APIPrefix+"/gateway/features", "")
	if st != 200 || !strings.Contains(out, `{"id":"w4p1_test_draft","name":"Test draft",`) {
		t.Fatalf("features: %d %s", st, out)
	}
}

func TestGatewayEffect404ForPlainComparison(t *testing.T) {
	src := gwTestSource{manifests: map[string]*schemas.Manifest{"e_plain": {ExpID: "e_plain"}}}
	h := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, src)
	st, out := secretsCall(h, "GET", APIPrefix+"/experiments/e_plain/gateway-effect", "")
	if st != 404 || !strings.Contains(out, "this comparison is not a Bifrost feature test") {
		t.Fatalf("%d %s", st, out)
	}
	st, out = secretsCall(h, "GET", APIPrefix+"/experiments/e_plain/gateway-effect?optional=1", "")
	if st != 200 || !strings.Contains(out, `"effect":null`) {
		t.Fatalf("optional: %d %s", st, out)
	}
	if st, _ := secretsCall(h, "GET", APIPrefix+"/experiments/e_missing/gateway-effect?optional=1", ""); st != 404 {
		t.Fatalf("missing comparison with optional: %d", st)
	}
	if st, _ := secretsCall(h, "GET", APIPrefix+"/experiments/e_missing/gateway-effect", ""); st != 404 {
		t.Fatalf("missing comparison: %d", st)
	}
	st, out = secretsCall(h, "GET", APIPrefix+"/gateway/effects?limit=500", "")
	if st != 400 {
		t.Fatalf("limit 500: %d %s", st, out)
	}
	st, out = secretsCall(h, "GET", APIPrefix+"/gateway/effects", "")
	if st != 200 || !strings.HasSuffix(strings.TrimSpace(out), `"effects":[]}`) {
		t.Fatalf("effects: %d %s", st, out)
	}
}

func TestGatewaySourceNeverReturnsCredential(t *testing.T) {
	const secretValue = "bf-admin-secret-4242"
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/private/path/api/version" {
			w.WriteHeader(404)
			return
		}
		w.Write([]byte(`"v2.5.1"`))
	}))
	defer srv.Close()
	old := gatewaySecret
	gatewaySecret = func(ctx context.Context, s Services, name string) string {
		if name == "BF_ADMIN" {
			return secretValue
		}
		return ""
	}
	defer func() { gatewaySecret = old }()
	root := t.TempDir()
	h := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode}, nil)
	st, out := secretsCall(h, "GET", APIPrefix+"/gateway/source", "")
	if st != 200 || strings.TrimSpace(out) != `{"mode":"none","connected":false,"host":"","error":""}` {
		t.Fatalf("none: %d %s", st, out)
	}
	st, out = secretsCall(h, "POST", APIPrefix+"/gateway/source", `{"gateway_url":"`+srv.URL+`/private/path","credential_secret":"BF_ADMIN"}`)
	if st != 200 {
		t.Fatalf("save: %d %s", st, out)
	}
	if gotAuth != "Bearer "+secretValue {
		t.Fatalf("version check sent %q", gotAuth)
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	for _, path := range []string{"/gateway/source", "/gateway/source"} {
		st, out = secretsCall(h, "GET", APIPrefix+path, "")
		if st != 200 || strings.Contains(out, secretValue) || strings.Contains(out, "BF_ADMIN") || strings.Contains(out, "/private/path") {
			t.Fatalf("get: %d %s", st, out)
		}
		if !strings.Contains(out, `"connected":true`) || !strings.Contains(out, `"host":"`+host+`"`) || !strings.Contains(out, `"mode":"saved"`) {
			t.Fatalf("get: %s", out)
		}
	}
	c, _ := gwfeature.LoadSource(root)
	if c == nil || c.CredentialSecret != "BF_ADMIN" {
		t.Fatalf("saved %+v", c)
	}
	data, _ := json.Marshal(c)
	if strings.Contains(string(data), secretValue) {
		t.Fatal("the secret value was saved")
	}
	st, out = secretsCall(h, "POST", APIPrefix+"/gateway/source/remove", "")
	if st != 200 || !strings.Contains(out, `"mode":"none"`) {
		t.Fatalf("remove: %d %s", st, out)
	}
}

func TestGatewaySourceHostedRefusesPrivateURL(t *testing.T) {
	root := t.TempDir()
	h := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Hosted: true, OrgID: "o1"}}, nil)
	for _, u := range []string{"http://127.0.0.1:8080", "https://127.0.0.1", "https://10.0.0.5", "https://user:pw@bf.example.com"} {
		st, out := secretsCall(h, "POST", APIPrefix+"/gateway/source", `{"gateway_url":"`+u+`"}`)
		if st != 400 {
			t.Errorf("%s: %d %s", u, st, out)
		}
	}
	if c, _ := gwfeature.LoadSource(root); c != nil {
		t.Fatalf("a refused source was saved: %+v", c)
	}
}

func TestGatewaySourceGetHostedRunGatewayNotUsed(t *testing.T) {
	cfg := &lib.Config{Root: t.TempDir()}
	cfg.Gateway.URL = "http://bifrost:8080"
	if get := gatewaySourceGet(context.Background(), OrgEnv{Cfg: cfg, Services: Services{Hosted: true, OrgID: "o1"}}); get != nil {
		t.Fatal("hosted without a saved source must have no source reader")
	}
	if get := gatewaySourceGet(context.Background(), OrgEnv{Cfg: cfg}); get == nil {
		t.Fatal("local mode reads the run gateway as the source")
	}
	if get := gatewayRunGet(context.Background(), OrgEnv{Cfg: cfg, Services: Services{Hosted: true}}); get == nil {
		t.Fatal("the run gateway reader must exist when a gateway is configured")
	}
	if get := gatewayRunGet(context.Background(), OrgEnv{Cfg: &lib.Config{}}); get != nil {
		t.Fatal("no gateway configured: no reader")
	}
}
