package handlers

// How Heimdall uses Bifrost (lane C10): a read only summary of the Bifrost
// features an org and one evaluation rely on, for the Settings > Integrations
// card and the evaluation page section.
//
//	GET /integrations/bifrost            the org's gateway and its features
//	GET /evaluations/{id}/bifrost        the same, plus what this evaluation uses
//
// The org summary is built from the bench config (gateway URL, admin token
// env) and one short call to the gateway's GET /api/plugins/loaded. A gateway
// that cannot be reached is reported, never an error: the page still shows
// what Heimdall would use.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// BifrostUseHTTP is the client for the plugin check; tests may replace it.
var BifrostUseHTTP = &http.Client{Timeout: 3 * time.Second}

// bifrostUseGetenv reads the admin token env; tests may replace it.
var bifrostUseGetenv = os.Getenv

const bifrostDocsBase = "https://docs.getbifrost.ai"

// Feature states.
const (
	BifrostFeatureOn        = "on"        // in use
	BifrostFeatureAvailable = "available" // can be turned on per evaluation
	BifrostFeatureOff       = "off"       // not in use
	BifrostFeatureUnknown   = "unknown"   // the gateway could not be asked
)

// BifrostFeature is one Bifrost feature and how Heimdall uses it.
type BifrostFeature struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Detail string `json:"detail"`
	Docs   string `json:"docs"`
	Code   string `json:"code,omitempty"` // where Heimdall uses it, repo path
}

// BifrostUse is GET /integrations/bifrost.
type BifrostUse struct {
	GatewayConfigured bool             `json:"gateway_configured"`
	GatewayHost       string           `json:"gateway_host,omitempty"`
	Reachable         bool             `json:"reachable"`
	CheckError        string           `json:"check_error,omitempty"`
	LoadedPlugins     []string         `json:"loaded_plugins"`
	Features          []BifrostFeature `json:"features"`
}

// EvaluationBifrostUse is GET /evaluations/{id}/bifrost: the org summary
// plus the features this evaluation's spec turns on.
type EvaluationBifrostUse struct {
	BifrostUse
	EvaluationID string           `json:"evaluation_id"`
	Uses         []BifrostFeature `json:"uses"`
}

type bifrostUseHandler struct{ env OrgEnv }

func init() {
	RegisterOrgArea("bifrostuse", func(env OrgEnv, r *router.Router) {
		h := &bifrostUseHandler{env: env}
		r.GET(APIPrefix+"/integrations/bifrost", env.R(h.org))
		r.GET(APIPrefix+"/evaluations/{id}/bifrost", env.R(h.evaluation))
		r.GET(APIPrefix+"/evaluations/{id}/bifrost/cache", env.R(h.cacheAB)) // C08 cache on vs off report (evalcacheab.go)
	})
}

func (h *bifrostUseHandler) org(ctx *fasthttp.RequestCtx) {
	work, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.summary(work))
}

// loadEvaluation reads the {id} evaluation of the caller's org; it has sent
// the error and returns nil when there is none.
func (h *bifrostUseHandler) loadEvaluation(ctx *fasthttp.RequestCtx) *schemas.Evaluation {
	id, _ := ctx.UserValue("id").(string)
	if !evalstore.ValidID(id) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "evaluation id must look like ev_<letters and digits>")
		return nil
	}
	ev, err := evalstore.Open(h.root()).Load(id)
	if err == nil && h.env.Services.OrgID != "" && ev.Org != "" && ev.Org != h.env.Services.OrgID {
		err = evalstore.ErrNotFound
	}
	if errors.Is(err, evalstore.ErrNotFound) {
		sendNotFound(ctx, h.env.Mode, "evaluation", id)
		return nil
	}
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return nil
	}
	return ev
}

func (h *bifrostUseHandler) evaluation(ctx *fasthttp.RequestCtx) {
	ev := h.loadEvaluation(ctx)
	if ev == nil {
		return
	}
	work, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	sum := h.summary(work)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, EvaluationBifrostUse{BifrostUse: sum, EvaluationID: ev.ID, Uses: EvaluationBifrostFeatures(ev, sum)})
}

func (h *bifrostUseHandler) root() string {
	if p := h.env.Services.Projects; p != nil {
		return p.Root()
	}
	if h.env.Cfg != nil {
		return h.env.Cfg.Root
	}
	return ""
}

func (h *bifrostUseHandler) summary(ctx context.Context) BifrostUse {
	var gw lib.GatewayConfig
	if h.env.Cfg != nil {
		gw = h.env.Cfg.Gateway
	}
	out := BifrostUse{LoadedPlugins: []string{}}
	if strings.TrimSpace(gw.URL) == "" {
		out.Features = BifrostFeatures(out)
		return out
	}
	out.GatewayConfigured = true
	if u, err := url.Parse(gw.URL); err == nil {
		out.GatewayHost = u.Host
	}
	token := ""
	if gw.AdminTokenEnv != "" {
		token = bifrostUseGetenv(gw.AdminTokenEnv)
	}
	plugins, err := loadedPlugins(ctx, gw.URL, token)
	if err != nil {
		out.CheckError = err.Error()
	} else {
		out.Reachable = true
		out.LoadedPlugins = plugins
	}
	out.Features = BifrostFeatures(out)
	return out
}

// loadedPlugins is GET /api/plugins/loaded on the gateway.
func loadedPlugins(ctx context.Context, base, token string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/plugins/loaded", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("X-Bifrost-Setup-Token", token)
	}
	resp, err := BifrostUseHTTP.Do(req)
	if err != nil {
		return nil, errors.New("gateway not reachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gateway answered %d to the plugin check", resp.StatusCode)
	}
	var body struct {
		Plugins []string `json:"plugins"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, errors.New("gateway plugin list could not be read")
	}
	if body.Plugins == nil {
		body.Plugins = []string{}
	}
	sort.Strings(body.Plugins)
	return body.Plugins, nil
}

// HeimdallPluginName is the name the heimdall Bifrost plugin registers
// under (plugins/heimdall PluginName); the gateway lists it by that name.
const HeimdallPluginName = "heimdall"

func hasPlugin(list []string, names ...string) bool {
	for _, p := range list {
		for _, n := range names {
			if strings.EqualFold(p, n) {
				return true
			}
		}
	}
	return false
}

// pluginState is on/off when the gateway answered, unknown otherwise.
func pluginState(s BifrostUse, names ...string) string {
	if !s.Reachable {
		return BifrostFeatureUnknown
	}
	if hasPlugin(s.LoadedPlugins, names...) {
		return BifrostFeatureOn
	}
	return BifrostFeatureOff
}

// BifrostFeatures lists every Bifrost feature Heimdall uses and its state
// for the org's gateway. Pure: built only from the summary.
func BifrostFeatures(s BifrostUse) []BifrostFeature {
	gwState := BifrostFeatureOff
	if s.GatewayConfigured {
		gwState = BifrostFeatureOn
	}
	optional := func(st string) string {
		if st == BifrostFeatureOn || st == BifrostFeatureUnknown {
			return st
		}
		return BifrostFeatureAvailable
	}
	if !s.GatewayConfigured {
		optional = func(string) string { return BifrostFeatureOff }
	}
	gov := pluginState(s, "governance")
	if !s.Reachable && s.GatewayConfigured {
		gov = gwState // per-run keys are made on every run with a gateway
	}
	if !s.GatewayConfigured {
		gov = BifrostFeatureOff
	}
	return []BifrostFeature{
		{ID: "gateway", Name: "Gateway", State: gwState,
			Detail: "Every model call of an agent run goes through Bifrost, so each call is logged with its tokens, cost and latency.",
			Docs:   bifrostDocsBase + "/features/drop-in-replacement", Code: "bench/gateways/bifrost/bifrost.go"},
		{ID: "virtual_keys", Name: "Virtual key per run", State: gov,
			Detail: "Each run gets its own Bifrost virtual key, revoked when the run ends, so its calls never mix with other runs.",
			Docs:   bifrostDocsBase + "/features/governance/virtual-keys", Code: "bench/gateways/bifrost/keys.go"},
		{ID: "budgets", Name: "Budgets and rate limits", State: gov,
			Detail: "The evaluation budget is set as a hard budget on the run keys. Bifrost stops calls once it is spent.",
			Docs:   bifrostDocsBase + "/features/governance/budget-and-limits", Code: "bench/gateways/bifrost/keys.go"},
		{ID: "fallbacks", Name: "Fallbacks and routing", State: optional(BifrostFeatureOff),
			Detail: "An arm can carry a routing policy: primary and fallback providers as Bifrost routing rules. The routing card shows which target served each call.",
			Docs:   bifrostDocsBase + "/features/retries-and-fallbacks", Code: "bench/gateways/bifrost/routing.go"},
		{ID: "semantic_cache", Name: "Semantic cache", State: optional(pluginState(s, "semantic_cache", "semanticcache")),
			Detail: "With the semantic cache plugin loaded, an evaluation can compare cache on and cache off and report hit rate and cost saved.",
			Docs:   bifrostDocsBase + "/features/semantic-caching", Code: "bench/gateways/bifrost/bifrostcache.go"},
		{ID: "heimdall_plugin", Name: "Heimdall plugin", State: optional(pluginState(s, HeimdallPluginName)),
			Detail: "A Bifrost plugin that injects model faults inside the gateway and tags each call with the run and evaluation ids.",
			Docs:   bifrostDocsBase + "/plugins/writing-go-plugin", Code: "plugins/heimdall"},
		{ID: "logs", Name: "Request logs", State: optional(pluginState(s, "logging")),
			Detail: "Heimdall reads Bifrost's request logs after each run for the run's calls, cost, cache hits and fallbacks.",
			Docs:   bifrostDocsBase + "/features/observability/default", Code: "bench/gateways/bifrost/logs.go"},
	}
}

// EvaluationBifrostFeatures lists what one evaluation's spec uses, with
// details from the spec (budget, arms, faults).
func EvaluationBifrostFeatures(ev *schemas.Evaluation, s BifrostUse) []BifrostFeature {
	byID := map[string]BifrostFeature{}
	for _, f := range s.Features {
		byID[f.ID] = f
	}
	var out []BifrostFeature
	add := func(id, detail string) {
		f := byID[id]
		f.Detail = detail
		if f.State != BifrostFeatureUnknown {
			f.State = BifrostFeatureOn
		}
		out = append(out, f)
	}
	if !s.GatewayConfigured {
		return []BifrostFeature{}
	}
	sp := ev.Spec
	models := map[string]bool{}
	for _, a := range sp.Arms {
		m := a.Model
		if a.Provider != "" {
			m = a.Provider + "/" + a.Model
		}
		if m != "" {
			models[m] = true
		}
	}
	names := make([]string, 0, len(models))
	for m := range models {
		names = append(names, m)
	}
	sort.Strings(names)
	gw := "Model calls of every run go through Bifrost."
	if len(names) > 0 {
		gw = "Model calls of every run go through Bifrost: " + strings.Join(names, ", ") + "."
	}
	add("gateway", gw)
	add("virtual_keys", "One virtual key per run, revoked when the run ends.")
	if sp.BudgetUSD > 0 {
		add("budgets", fmt.Sprintf("Budget $%.2f for this evaluation, enforced by Bifrost on the run keys.", sp.BudgetUSD))
	}
	if fo := sp.Failover; fo != nil && fo.Enabled { // C07 provider failover option
		var fbs []string
		for _, f := range fo.Fallbacks {
			fbs = append(fbs, strings.TrimPrefix(f.Provider+"/"+f.Model, "/"))
		}
		d := "Provider failover: a second arm sets Bifrost fallbacks for the agent's model and runs under an outage of the primary model."
		if len(fbs) > 0 {
			d = "Provider failover: a second arm sets Bifrost fallbacks (" + strings.Join(fbs, ", ") + ") for the agent's model and runs under an outage of the primary model. The report card shows whether failover worked."
		}
		add("fallbacks", d)
	}
	if sp.BifrostCache { // C08 cache on vs off option
		add("semantic_cache", "Bifrost cache on vs off: a second arm, identical except that Bifrost's semantic cache is on, so the report shows the hit rate and the latency and cost saved.")
	}
	if sp.MCPRoute == schemas.MCPRouteBifrost || sp.MCPRoute == schemas.MCPRouteBifrostCodeMode { // C11 MCP route
		out = append(out, BifrostFeature{ID: "mcp_gateway", Name: "MCP gateway", State: BifrostFeatureOn,
			Detail: "MCP tool calls go through Bifrost's MCP gateway, so they show in Bifrost's logs. The tool proxy stays behind Bifrost, so tool faults still work.",
			Docs:   bifrostDocsBase + "/features/mcp", Code: "bench/handlers/evaluations.go"})
	}
	var gwFaults []string
	for _, f := range sp.Faults {
		if strings.HasPrefix(f, "model-") || f == "rate-limit" || f == "timeout" {
			gwFaults = append(gwFaults, f)
		}
	}
	if len(gwFaults) > 0 {
		how := "through the fault proxy behind Bifrost"
		if byID["heimdall_plugin"].State == BifrostFeatureOn {
			how = "inside Bifrost by the Heimdall plugin"
		}
		f := byID["heimdall_plugin"]
		if f.State != BifrostFeatureOn {
			f.ID, f.Name, f.Docs, f.Code = "model_faults", "Model faults", bifrostDocsBase+"/features/retries-and-fallbacks", "bench/faultproxy"
		}
		f.State = BifrostFeatureOn
		f.Detail = "Model faults (" + strings.Join(gwFaults, ", ") + ") are injected " + how + "."
		out = append(out, f)
	}
	if lf := byID["logs"]; lf.State != BifrostFeatureOff {
		add("logs", "Each run's calls, tokens and cost are read from Bifrost's logs.")
	}
	return out
}
