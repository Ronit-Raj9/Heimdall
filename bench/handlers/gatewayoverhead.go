package handlers

import (
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// GET /experiments/{id}/gateway-overhead (W4 design 3, section 13): Bifrost's
// own time per model call, split by plugin, over any comparison's clean
// runs. It needs no gateway test and saves nothing.

func init() {
	RegisterOrgArea("gatewayoverhead", func(env OrgEnv, r *router.Router) {
		newGatewayOverheadHandler(env).routes(r)
	})
}

type gatewayOverheadHandler struct {
	env    OrgEnv
	source func() leaderboard.Source // nil result: no run index on this server
}

func newGatewayOverheadHandler(env OrgEnv) *gatewayOverheadHandler {
	h := &gatewayOverheadHandler{env: env}
	h.source = func() leaderboard.Source {
		if env.Services.Index == nil {
			return nil
		}
		return LeaderboardSource(env.Services.Index)
	}
	return h
}

func (h *gatewayOverheadHandler) routes(r *router.Router) {
	r.GET(APIPrefix+"/experiments/{id}/gateway-overhead", h.env.R(h.overhead))
}

func (h *gatewayOverheadHandler) overhead(ctx *fasthttp.RequestCtx) {
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
	runs, err := gwfeature.LoadRuns(src, id, false)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "the gateway time report could not be built")
		return
	}
	var options map[string]string
	if m.Experiment.GatewayTest != nil {
		options = m.Experiment.GatewayTest.Options
	}
	e := gwfeature.ComputeOverhead(runs, options)
	e.Feature, e.ExpID, e.ComputedAt = schemas.GatewayFeatureOverhead, id, time.Now().UTC()
	e.Source.Kind, e.Source.ExpID, e.Source.Runs = "experiment", id, len(runs)
	for _, r := range runs {
		e.Source.Rows += len(r.Rows)
		for _, row := range r.Rows {
			if row.RawKnown {
				e.Source.RawRows++
			}
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, e)
}
