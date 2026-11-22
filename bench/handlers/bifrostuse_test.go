package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

func bifrostUseRouter(env OrgEnv) fasthttp.RequestHandler {
	h := &bifrostUseHandler{env: env}
	r := router.New()
	r.GET(APIPrefix+"/integrations/bifrost", env.R(h.org))
	r.GET(APIPrefix+"/evaluations/{id}/bifrost", env.R(h.evaluation))
	return r.Handler
}

func featureState(fs []BifrostFeature, id string) string {
	for _, f := range fs {
		if f.ID == id {
			return f.State
		}
	}
	return ""
}

func TestBifrostUseNoGateway(t *testing.T) {
	h := bifrostUseRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode})
	st, body := secretsCall(h, "GET", APIPrefix+"/integrations/bifrost", "")
	if st != 200 {
		t.Fatalf("%d %s", st, body)
	}
	var u BifrostUse
	if err := json.Unmarshal([]byte(body), &u); err != nil {
		t.Fatal(err)
	}
	if u.GatewayConfigured || featureState(u.Features, "gateway") != BifrostFeatureOff || featureState(u.Features, "heimdall_plugin") != BifrostFeatureOff {
		t.Fatalf("no gateway: %+v", u)
	}
}

func TestBifrostUsePluginsAndEvaluation(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugins/loaded" {
			w.WriteHeader(404)
			return
		}
		gotToken = r.Header.Get("X-Bifrost-Setup-Token")
		_, _ = w.Write([]byte(`{"plugins":["logging","heimdall","governance"]}`))
	}))
	defer srv.Close()
	old := bifrostUseGetenv
	bifrostUseGetenv = func(k string) string {
		if k == "BIFROST_ADMIN" {
			return "tok"
		}
		return ""
	}
	defer func() { bifrostUseGetenv = old }()

	root := t.TempDir()
	ev := &schemas.Evaluation{ID: "ev_abc123", Org: "o1", Status: "done", Spec: schemas.EvaluationSpec{
		Arms:      []schemas.EvaluationArm{{Label: "a", Model: "gpt-x", Provider: "openai"}},
		Faults:    []string{"clean", "model-down"},
		BudgetUSD: 12.5,
	}}
	if err := evalstore.Open(root).Save(ev); err != nil {
		t.Fatal(err)
	}
	cfg := &lib.Config{Root: root}
	cfg.Gateway.URL = srv.URL
	cfg.Gateway.AdminTokenEnv = "BIFROST_ADMIN"
	h := bifrostUseRouter(OrgEnv{Cfg: cfg, Mode: testMode, Services: Services{OrgID: "o1"}})

	st, body := secretsCall(h, "GET", APIPrefix+"/integrations/bifrost", "")
	var u BifrostUse
	if st != 200 || json.Unmarshal([]byte(body), &u) != nil {
		t.Fatalf("%d %s", st, body)
	}
	if gotToken != "tok" || !u.Reachable || featureState(u.Features, "heimdall_plugin") != BifrostFeatureOn ||
		featureState(u.Features, "semantic_cache") != BifrostFeatureAvailable || featureState(u.Features, "virtual_keys") != BifrostFeatureOn {
		t.Fatalf("org summary: token=%q %+v", gotToken, u)
	}

	st, body = secretsCall(h, "GET", APIPrefix+"/evaluations/ev_abc123/bifrost", "")
	var e EvaluationBifrostUse
	if st != 200 || json.Unmarshal([]byte(body), &e) != nil {
		t.Fatalf("%d %s", st, body)
	}
	if featureState(e.Uses, "budgets") != BifrostFeatureOn || featureState(e.Uses, "heimdall_plugin") != BifrostFeatureOn {
		t.Fatalf("uses: %+v", e.Uses)
	}
	if !strings.Contains(body, "$12.50") || !strings.Contains(body, "inside Bifrost by the Heimdall plugin") || !strings.Contains(body, "openai/gpt-x") {
		t.Fatalf("details: %s", body)
	}

	// Another org's evaluation is not found; a bad id is rejected.
	h2 := bifrostUseRouter(OrgEnv{Cfg: cfg, Mode: testMode, Services: Services{OrgID: "o2"}})
	if st, _ := secretsCall(h2, "GET", APIPrefix+"/evaluations/ev_abc123/bifrost", ""); st != 404 {
		t.Fatalf("other org: %d", st)
	}
	if st, _ := secretsCall(h, "GET", APIPrefix+"/evaluations/bad/bifrost", ""); st != 400 {
		t.Fatalf("bad id: %d", st)
	}
}

func TestBifrostUseUnreachable(t *testing.T) {
	cfg := &lib.Config{Root: t.TempDir()}
	cfg.Gateway.URL = "http://127.0.0.1:1"
	h := bifrostUseRouter(OrgEnv{Cfg: cfg, Mode: testMode})
	st, body := secretsCall(h, "GET", APIPrefix+"/integrations/bifrost", "")
	var u BifrostUse
	if st != 200 || json.Unmarshal([]byte(body), &u) != nil {
		t.Fatalf("%d %s", st, body)
	}
	if u.Reachable || u.CheckError == "" || featureState(u.Features, "heimdall_plugin") != BifrostFeatureUnknown || featureState(u.Features, "virtual_keys") != BifrostFeatureOn {
		t.Fatalf("unreachable: %+v", u)
	}
}
