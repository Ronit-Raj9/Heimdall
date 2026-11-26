package handlers

// Improvement runs (W4 design 5, A2.9): the pre-run estimate of an
// improvement spec. POST /improvements/estimate reads the create body and
// answers the estimate; it changes nothing.

import (
	"context"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// ImproveEstimateInputs fills the estimator's inputs for a parsed spec: the
// suite's task pool, the partition, the per-run cost and time, and the
// incumbent's history. cmd/bifrost-bench sets it (improveestimate_wire.go);
// nil means this server cannot estimate improvement runs. The org of the
// request is in ctx (ImproveEstimateEnv).
var ImproveEstimateInputs func(ctx context.Context, spec *improve.Spec) (improve.EstimateInput, error)

type improveEstimateEnvKey struct{}

// ImproveEstimateEnv returns the org environment of an estimate request.
func ImproveEstimateEnv(ctx context.Context) (OrgEnv, bool) {
	env, ok := ctx.Value(improveEstimateEnvKey{}).(OrgEnv)
	return env, ok
}

func init() {
	RegisterOrgArea("w45-improve-estimate", func(env OrgEnv, r *router.Router) {
		h := &improveEstimateHandler{env: env}
		r.POST(APIPrefix+"/improvements/estimate", env.R(h.post))
	})
}

type improveEstimateHandler struct{ env OrgEnv }

func (h *improveEstimateHandler) post(ctx *fasthttp.RequestCtx) {
	spec, err := improve.ParseSpec(ctx.PostBody())
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	est := improve.DefaultEstimator
	if ImproveEstimateInputs == nil || est == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "estimates are not available on this server")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	in, err := ImproveEstimateInputs(context.WithValue(work, improveEstimateEnvKey{}, h.env), spec)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	in.Spec = spec
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, est.Estimate(in))
}
