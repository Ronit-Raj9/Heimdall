package handlers

// Model switch area (lane Z21): the model switch report of an evaluation
// that ran one agent on several models, the Bifrost routing config written
// from it, and the apply path that writes those rules to the org's own
// Bifrost. The evaluation itself is a normal evaluation in mode
// model_switch (POST /evaluations); its launch rules are below.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/modelswitch"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// ModelSwitchReport is GET /evaluations/{id}/model-switch.
type ModelSwitchReport = modelswitch.Report

// ModelSwitchExport is GET /evaluations/{id}/model-switch/bifrost-config.
type ModelSwitchExport = modelswitch.Export

type modelSwitchHandler struct {
	env   OrgEnv
	evals ReportCardEvaluations
	// gateway builds the apply client; tests replace it.
	gateway func(base, credential string, public bool) modelswitch.Gateway
}

func init() {
	RegisterOrgArea("modelswitch", registerModelSwitch)
}

func registerModelSwitch(env OrgEnv, r *router.Router) {
	var evals ReportCardEvaluations
	if env.Cfg != nil && env.Cfg.Root != "" {
		evals = NewFileEvaluations(env.Cfg.Root)
	}
	h := &modelSwitchHandler{env: env, evals: evals, gateway: func(base, credential string, public bool) modelswitch.Gateway {
		return modelswitch.Gateway{BaseURL: base, Credential: credential, Client: modelswitch.GatewayClient(public)}
	}}
	p := APIPrefix + "/evaluations/{id}/model-switch"
	r.GET(p, env.R(h.getReport))
	r.GET(p+"/bifrost-config", env.R(h.getConfig))
	r.POST(p+"/apply", env.W(h.apply))
}

// checkModelSwitchArms is the launch rule of mode model_switch: two to
// EvaluationModelSwitchMaxArms arms of one agent at one version, each on a
// different model.
func checkModelSwitchArms(arms []schemas.EvaluationArm) error {
	n := len(arms)
	if n < 2 || n > schemas.EvaluationModelSwitchMaxArms {
		return badf("mode model_switch takes 2 to %d models, got %d", schemas.EvaluationModelSwitchMaxArms, n)
	}
	agentOf := func(a schemas.EvaluationArm) string {
		return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(a.Agent), "agents/"), ".yaml")
	}
	seen := map[string]int{}
	for i, a := range arms {
		if agentOf(a) != agentOf(arms[0]) {
			return badf("mode model_switch compares one agent on several models: arm %d runs %s, arm 1 runs %s", i+1, agentOf(a), agentOf(arms[0]))
		}
		if a.Version != arms[0].Version {
			return badf("mode model_switch runs one version of the agent: arm %d asks for version %d, arm 1 for %d", i+1, a.Version, arms[0].Version)
		}
		k := strings.TrimSpace(a.Provider) + "/" + strings.TrimSpace(a.Model)
		if j, dup := seen[k]; dup {
			if strings.TrimSpace(a.Model) == "" {
				return badf("arms %d and %d both run the agent's own model: pick a model for one of them", j+1, i+1)
			}
			return badf("arms %d and %d run the same model %s: pick different models", j+1, i+1, strings.Trim(k, "/"))
		}
		seen[k] = i
	}
	return nil
}

func (h *modelSwitchHandler) ready(ctx *fasthttp.RequestCtx) bool {
	switch {
	case h.env.Services.Index == nil:
		sendUnavailable(ctx, h.env.Mode, "run index")
	case h.evals == nil:
		sendUnavailable(ctx, h.env.Mode, "evaluation store")
	default:
		return true
	}
	return false
}

// build loads the evaluation and writes its report; it answers the error
// itself and returns nil then.
func (h *modelSwitchHandler) build(ctx *fasthttp.RequestCtx, work context.Context) *modelswitch.Report {
	id, ok := idParam(ctx, h.env.Mode, "id", "evaluation")
	if !ok || !h.ready(ctx) {
		return nil
	}
	ev, err := h.evals.Get(work, id)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "evaluation", err)
		return nil
	}
	opt := modelswitch.Options{Segments: h.segments(work)}
	if raw := strings.TrimSpace(string(ctx.QueryArgs().Peek("alpha"))); raw != "" {
		a, err := strconv.ParseFloat(raw, 64)
		if err != nil || !(a > 0 && a < 1) {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("alpha %q is not a number in (0, 1)", raw))
			return nil
		}
		opt.Alpha = a
	}
	s := h.env.Services
	var keys KeyLister
	if s.Keys != nil {
		keys = s.Keys
	}
	src := PricedLeaderboardSource(s.Index, KeyPriceLookup(work, keys, quickOrgID(ctx, s.OrgID)))
	rep, err := modelswitch.Build(work, src, ev, opt)
	if errors.Is(err, modelswitch.ErrNotModelSwitch) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return nil
	}
	if err != nil {
		sendDepError(ctx, h.env.Mode, "model switch report", err)
		return nil
	}
	return rep
}

// getReport handles GET /evaluations/{id}/model-switch.
func (h *modelSwitchHandler) getReport(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	if rep := h.build(ctx, work); rep != nil {
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, rep)
	}
}

// routingOptions reads the routing options from the query (GET) or body
// (apply).
func routingOptions(get func(string) string) (modelswitch.RoutingOptions, error) {
	o := modelswitch.RoutingOptions{
		Prefix: strings.TrimSpace(get("prefix")), HeaderPrefix: strings.TrimSpace(get("header_prefix")),
		Scope: strings.TrimSpace(get("scope")), ScopeID: strings.TrimSpace(get("scope_id")),
	}
	switch o.Scope {
	case "", "global":
		o.ScopeID = ""
	case "team", "customer", "virtual_key":
		if o.ScopeID == "" {
			return o, badf("scope %s needs scope_id: the id of the %s", o.Scope, strings.ReplaceAll(o.Scope, "_", " "))
		}
	default:
		return o, badf("scope %q is not global, team, customer or virtual_key", o.Scope)
	}
	if o.Prefix != "" && modelswitch.RuleSlug(o.Prefix) != o.Prefix {
		return o, badf("prefix %q may hold only lower-case letters, digits and dashes", o.Prefix)
	}
	if o.HeaderPrefix != "" && (!strings.HasPrefix(o.HeaderPrefix, "x-") || modelswitch.RuleSlug(o.HeaderPrefix) != strings.TrimSuffix(o.HeaderPrefix, "-")) {
		return o, badf("header_prefix %q must start with x- and hold only lower-case letters, digits and dashes", o.HeaderPrefix)
	}
	if raw := strings.TrimSpace(get("budget_saver_at")); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 || v > 100 {
			return o, badf("budget_saver_at %q is not a percent in (0, 100]", raw)
		}
		o.BudgetSaverAt = v
	}
	if raw := strings.TrimSpace(get("base_priority")); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 1_000_000 {
			return o, badf("base_priority %q is not a whole number from 1", raw)
		}
		o.BasePriority = v
	}
	return o, nil
}

// getConfig handles GET /evaluations/{id}/model-switch/bifrost-config;
// download=1 answers the config.json fragment alone, as a file.
func (h *modelSwitchHandler) getConfig(ctx *fasthttp.RequestCtx) {
	args := ctx.QueryArgs()
	opt, err := routingOptions(func(k string) string { return string(args.Peek(k)) })
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	rep := h.build(ctx, work)
	if rep == nil {
		return
	}
	ex := modelswitch.Routing(rep, opt)
	if d := string(args.Peek("download")); d == "1" || d == "true" {
		data, err := json.MarshalIndent(ex.Config, "", "  ")
		if err != nil {
			sendDepError(ctx, h.env.Mode, "bifrost config", err)
			return
		}
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("application/json; charset=utf-8")
		ctx.Response.Header.Set("Content-Disposition", `attachment; filename="bifrost-routing-`+modelswitch.RuleSlug(rep.Evaluation)+`.json"`)
		ctx.SetBody(append(data, '\n'))
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ex)
}

// ModelSwitchApplyRequest is the body of POST .../model-switch/apply: the
// gateway and credential, plus the routing options of the config.
type ModelSwitchApplyRequest struct {
	modelswitch.ApplyRequest
	Prefix        string  `json:"prefix,omitempty"`
	HeaderPrefix  string  `json:"header_prefix,omitempty"`
	BudgetSaverAt float64 `json:"budget_saver_at,omitempty"`
	BasePriority  int     `json:"base_priority,omitempty"`
}

// apply handles POST /evaluations/{id}/model-switch/apply.
func (h *modelSwitchHandler) apply(ctx *fasthttp.RequestCtx) {
	var req ModelSwitchApplyRequest
	dec := json.NewDecoder(strings.NewReader(string(ctx.PostBody())))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse apply request: "+err.Error())
		return
	}
	fields := map[string]string{"prefix": req.Prefix, "header_prefix": req.HeaderPrefix, "scope": req.Scope, "scope_id": req.ScopeID}
	if req.BudgetSaverAt != 0 {
		fields["budget_saver_at"] = strconv.FormatFloat(req.BudgetSaverAt, 'f', -1, 64)
	}
	if req.BasePriority != 0 {
		fields["base_priority"] = strconv.Itoa(req.BasePriority)
	}
	opt, err := routingOptions(func(k string) string { return fields[k] })
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	public := h.env.Services.Hosted
	u, err := modelswitch.CheckGatewayURL(req.GatewayURL, public)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	credential := ""
	if name := strings.TrimSpace(req.CredentialSecret); name != "" {
		credential = OrgSecretGetenv(ctx, h.env.Services)(name)
		if credential == "" {
			lib.SendErrorLink(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
				fmt.Sprintf("org secret %q is not set: add the gateway's admin credential as a secret first", name), "/settings?tab=secrets")
			return
		}
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	rep := h.build(ctx, work)
	if rep == nil {
		return
	}
	ex := modelswitch.Routing(rep, opt)
	res, err := h.gateway(strings.TrimRight(u.String(), "/"), credential, public).Apply(work, ex, req.ApplyRequest)
	if errors.Is(err, modelswitch.ErrGatewayRefused) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, res)
}

// segments returns each task's segment labels from its suite: task kind,
// capability (the benchmark's), difficulty, tags (one value per tag) and
// the task's own metadata keys from task.yaml. A suite that does not load
// gives the capability alone.
func (h *modelSwitchHandler) segments(ctx context.Context) func(benchmark, task string) map[string]string {
	suites := h.env.Services.Suites
	type loaded struct {
		capability string
		tasks      map[string]map[string]string
	}
	cache := map[string]*loaded{}
	return func(benchmark, task string) map[string]string {
		id, _, _ := strings.Cut(benchmark, "@")
		l, ok := cache[id]
		if !ok {
			l = &loaded{capability: reportcard.CapabilityFor(benchmark, ""), tasks: map[string]map[string]string{}}
			if suites != nil {
				if st, err := suites.LoadSuite(ctx, id); err == nil && st != nil {
					l.capability = reportcard.CapabilityFor(benchmark, st.Track)
					for _, t := range st.Tasks {
						if t != nil {
							l.tasks[t.ID] = taskSegments(t)
						}
					}
				}
			}
			cache[id] = l
		}
		out := map[string]string{"capability": l.capability}
		for k, v := range l.tasks[task] {
			out[k] = v
		}
		return out
	}
}

// Segment keys a metadata key may not take over.
var builtinSegmentKeys = map[string]bool{"capability": true, "task_kind": true, "difficulty": true, "tag": true}

// taskSegments are one task's segment labels. Metadata is read from the
// task file so labels from the customer's data (tier, language, intent)
// slice the report whatever the task schema version.
func taskSegments(t *schemas.Task) map[string]string {
	out := map[string]string{}
	if t.Kind != "" {
		out["task_kind"] = string(t.Kind)
	}
	if t.Difficulty != "" {
		out["difficulty"] = t.Difficulty
	}
	if len(t.Tags) > 0 {
		out["tag"] = strings.Join(t.Tags, "\n")
	}
	if t.Dir == "" {
		return out
	}
	data, err := os.ReadFile(filepath.Join(t.Dir, "task.yaml"))
	if err != nil {
		return out
	}
	var doc struct {
		Metadata map[string]string `yaml:"metadata"`
	}
	if yaml.Unmarshal(data, &doc) != nil {
		return out
	}
	for k, v := range doc.Metadata {
		k = strings.TrimSpace(k)
		if k == "" || len(k) > 64 || builtinSegmentKeys[k] || strings.TrimSpace(v) == "" {
			continue
		}
		out[k] = strings.TrimSpace(v)
	}
	return out
}
