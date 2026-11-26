package handlers

import (
	"errors"
	"fmt"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
)

// W4 feature 14: "Use this in Bifrost" (audit/W4_DESIGN_3_BIFROST.md
// section 16.3). GET /experiments/{id}/bifrost-setting returns the setting
// to paste into Bifrost for a comparison's result. It only returns text:
// Heimdall never applies it to any gateway.

func init() {
	RegisterOrgArea("bifrostsetting", func(env OrgEnv, r *router.Router) {
		newBifrostSettingHandler(env).routes(r)
	})
}

// Reasons when there is no setting.
const (
	settingReasonInconclusive = "The comparison could not tell the two setups apart, so there is no setting to change."
	settingReasonDiffer       = "The setups differ in more than the model, so no single Bifrost setting gives this result."
	settingReasonNoResult     = "The comparison has no valid result yet."
	settingReasonNoFeature    = "This test found no Bifrost setting to change."
)

type bifrostSettingHandler struct {
	env    OrgEnv
	tests  *gatewayTestsHandler
	source func() leaderboard.Source
}

func newBifrostSettingHandler(env OrgEnv) *bifrostSettingHandler {
	t := newGatewayTestsHandler(env)
	return &bifrostSettingHandler{env: env, tests: t, source: t.source}
}

func (h *bifrostSettingHandler) routes(r *router.Router) {
	r.GET(APIPrefix+"/experiments/{id}/bifrost-setting", h.env.R(h.get))
}

// BifrostSettingAnswer is GET /experiments/{id}/bifrost-setting. Setting is
// null, with the reason, when there is nothing to paste.
type BifrostSettingAnswer struct {
	Setting *gwfeature.BifrostSetting `json:"setting"`
	Reason  string                    `json:"reason"`
}

func (h *bifrostSettingHandler) get(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "comparison")
	if !ok {
		return
	}
	src := h.source()
	if src == nil {
		sendUnavailable(ctx, h.env.Mode, "the run index")
		return
	}
	m, ok := src.Manifest(id)
	if !ok || m == nil {
		sendNotFound(ctx, h.env.Mode, "comparison", id)
		return
	}
	if m.Experiment.GatewayTest != nil {
		work, cancel := workContext(ctx, readTimeout)
		defer cancel()
		e, err := gwfeature.Compute(work, src, h.tests.root(), id, false)
		if err != nil && !errors.Is(err, gwfeature.ErrNotFeatureTest) {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "the feature report could not be built: "+err.Error())
			return
		}
		if err == nil {
			ans := BifrostSettingAnswer{Setting: e.Setting}
			if e.Setting == nil {
				ans.Reason = e.Reason
				if ans.Reason == "" {
					ans.Reason = settingReasonNoFeature
				}
			}
			lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, ans)
			return
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, comparisonSetting(src, m, id))
}

// comparisonSetting is the routing rule of a plain two-setup comparison
// whose setups differ only in model and provider.
func comparisonSetting(src leaderboard.Source, m *schemas.Manifest, id string) BifrostSettingAnswer {
	decision, _ := src.LatestDecision(id)
	switch decision {
	case schemas.DecisionImproved, schemas.DecisionRegressed:
	case schemas.DecisionInconclusive:
		return BifrostSettingAnswer{Reason: settingReasonInconclusive}
	default:
		return BifrostSettingAnswer{Reason: settingReasonNoResult}
	}
	a, okA := m.Arms["A"]
	b, okB := m.Arms["B"]
	if !okA || !okB || !modelOnlyDiffers(a.Config, b.Config) {
		return BifrostSettingAnswer{Reason: settingReasonDiffer}
	}
	win, lose := b.Config, a.Config
	if decision == schemas.DecisionRegressed {
		win, lose = a.Config, b.Config
	}
	rule := gwfeature.RoutingRule{
		Name:          "heimdall-" + id,
		Enabled:       false,
		CelExpression: fmt.Sprintf("model == %q", lose.Model),
		Targets:       []gwfeature.RoutingTarget{{Provider: win.Gateway.Provider, Model: win.Model, Weight: 1}},
		Fallbacks:     []string{lose.Gateway.Provider + "/" + lose.Model},
		Scope:         "global",
		Priority:      10,
	}
	note := fmt.Sprintf("The rule is created switched off. Requests for %s go to %s, and back to %s if %s fails.", lose.Model, win.Model, lose.Model, win.Model)
	title := fmt.Sprintf("Routing rule: %s to %s", lose.Model, win.Model)
	return BifrostSettingAnswer{Setting: gwfeature.RuleSetting(title, note, rule)}
}

// modelOnlyDiffers reports whether two resolved configs are equal after
// blanking model, gateway.provider and gateway.fallbacks.
func modelOnlyDiffers(a, b schemas.AgentConfig) bool {
	blank := func(c schemas.AgentConfig) (string, bool) {
		c.Model, c.Gateway.Provider, c.Gateway.Fallbacks = "", "", nil
		h, err := utils.HashJSON(c)
		return h, err == nil
	}
	ha, okA := blank(a)
	hb, okB := blank(b)
	return okA && okB && ha == hb
}
