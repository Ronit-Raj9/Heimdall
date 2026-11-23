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

	t.Setenv("HEIMDALL_FAULT_MODE", "plugin")
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
	// Fix wave 3: formatUSD prints whole dollars from $10, so $12.50 reads $13.
	if !strings.Contains(body, "Budget $13 for this evaluation") || !strings.Contains(body, "inside Bifrost by the Heimdall plugin") || !strings.Contains(body, "openai/gpt-x") {
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

// TestModelFaultTextFollowsFaultMode: the evaluation page says the Heimdall
// plugin injects model faults only when the fault mode is plugin and the
// gateway loaded it; otherwise the fault proxy does, and a loaded plugin is
// shown as loaded but not used for faults.
func TestModelFaultTextFollowsFaultMode(t *testing.T) {
	ev := &schemas.Evaluation{ID: "ev_abc123", Spec: schemas.EvaluationSpec{Faults: []string{"clean", "model-brownout"}}}
	sum := BifrostUse{GatewayConfigured: true, Reachable: true, LoadedPlugins: []string{"heimdall"}}
	sum.Features = BifrostFeatures(sum)
	find := func(uses []BifrostFeature, id string) BifrostFeature {
		for _, f := range uses {
			if f.ID == id {
				return f
			}
		}
		return BifrostFeature{}
	}

	t.Setenv("HEIMDALL_FAULT_MODE", "")
	uses := EvaluationBifrostFeatures(ev, sum)
	mf, hp := find(uses, "model_faults"), find(uses, "heimdall_plugin")
	if mf.Detail != "Model faults (model-brownout) are injected by Heimdall's fault proxy behind Bifrost." || mf.State != BifrostFeatureOn {
		t.Fatalf("mode unset: model faults %+v", mf)
	}
	if hp.Detail != "Loaded. Faults go through the fault proxy because the fault mode is proxy." || hp.State == BifrostFeatureOn {
		t.Fatalf("mode unset: plugin row %+v", hp)
	}

	t.Setenv("HEIMDALL_FAULT_MODE", "plugin")
	uses = EvaluationBifrostFeatures(ev, sum)
	if hp := find(uses, "heimdall_plugin"); hp.Detail != "Model faults (model-brownout) are injected inside Bifrost by the Heimdall plugin." || hp.State != BifrostFeatureOn {
		t.Fatalf("plugin mode: %+v", uses)
	}
	if find(uses, "model_faults").ID != "" {
		t.Fatalf("plugin mode also lists the proxy: %+v", uses)
	}

	// Plugin mode, but the gateway does not list the plugin: the proxy.
	none := BifrostUse{GatewayConfigured: true, Reachable: true, LoadedPlugins: []string{}}
	none.Features = BifrostFeatures(none)
	uses = EvaluationBifrostFeatures(ev, none)
	if mf := find(uses, "model_faults"); !strings.Contains(mf.Detail, "fault proxy behind Bifrost") || find(uses, "heimdall_plugin").ID != "" {
		t.Fatalf("plugin not loaded: %+v", uses)
	}

	// The runs' recorded fault paths are appended.
	uses = withFaultSplit(EvaluationBifrostFeatures(ev, sum), []string{"plugin", "plugin", "plugin", "plugin", "proxy", "proxy", ""})
	if hp := find(uses, "heimdall_plugin"); !strings.HasSuffix(hp.Detail, " Applied by the plugin in 4 runs, by the proxy in 2 runs.") {
		t.Fatalf("split: %q", hp.Detail)
	}
	t.Setenv("HEIMDALL_FAULT_MODE", "")
	uses = withFaultSplit(EvaluationBifrostFeatures(ev, sum), []string{"proxy"})
	if mf := find(uses, "model_faults"); !strings.HasSuffix(mf.Detail, " Applied by the proxy in 1 run.") {
		t.Fatalf("split proxy: %q", mf.Detail)
	}
	if hp := find(uses, "heimdall_plugin"); strings.Contains(hp.Detail, "Applied") {
		t.Fatalf("split on the unused plugin row: %q", hp.Detail)
	}
}

// TestEvaluationBudgetTextUsesChildCaps: a cap raised on a benchmark's run
// controls reaches the child's budget (budgetFacts), and the Bifrost budget
// line sums the children's caps instead of the budget typed at launch.
func TestEvaluationBudgetTextUsesChildCaps(t *testing.T) {
	ev := &schemas.Evaluation{ID: "ev_abc123", Spec: schemas.EvaluationSpec{BudgetUSD: 1}, Children: []schemas.EvaluationChild{
		{Benchmark: "a", ExperimentID: "e_a", BudgetUSD: 1.10},
		{Benchmark: "b", Status: schemas.EvaluationChildSkipped},
	}}
	sum := BifrostUse{GatewayConfigured: true, Reachable: true, LoadedPlugins: []string{"governance"}}
	sum.Features = BifrostFeatures(sum)
	for _, f := range EvaluationBifrostFeatures(ev, sum) {
		if f.ID == "budgets" {
			if !strings.Contains(f.Detail, "$1.10") {
				t.Fatalf("budget %q", f.Detail)
			}
			return
		}
	}
	t.Fatal("no budget row")
}
