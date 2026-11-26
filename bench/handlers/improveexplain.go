package handlers

import (
	"context"
	"strconv"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Improvement version explain (wave 4, D5 A4.3): what changed in one
// version of an improvement run and why.

// ImproveExplainer is the part of an improvement source that explains a
// version; a source without it answers 501.
type ImproveExplainer interface {
	Explain(ctx context.Context, id string, n int) (*improve.Explain, error)
}

// OrgImproveSource returns the improvement source of the organization whose
// routes env registers; cmd/bifrost-bench sets it. nil, or a source that is
// not an ImproveExplainer, makes the explain route answer 501.
var OrgImproveSource func(env OrgEnv) ImproveSource

func init() {
	RegisterOrgArea("w45-improve-explain", registerImproveExplain)
}

func registerImproveExplain(env OrgEnv, r *router.Router) {
	h := &improveExplainHandler{mode: env.Mode, env: env}
	r.GET(APIPrefix+"/improvements/{id}/versions/{n}/explain", env.R(h.get))
}

type improveExplainHandler struct {
	mode schemas.Mode
	env  OrgEnv
}

// get handles GET /api/bench/improvements/{id}/versions/{n}/explain.
func (h *improveExplainHandler) get(ctx *fasthttp.RequestCtx) {
	id := pathParam(ctx, "id")
	if !improve.ValidID(id) {
		sendNotFound(ctx, h.mode, "improvement", id)
		return
	}
	n, err := strconv.Atoi(pathParam(ctx, "n"))
	if err != nil || n < 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "the version must be a whole number of 0 or more")
		return
	}
	var x ImproveExplainer
	if OrgImproveSource != nil {
		x, _ = OrgImproveSource(h.env).(ImproveExplainer)
	}
	if x == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotImplemented, lib.ErrTypeUnavailable, "This server cannot explain improvement versions.")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := x.Explain(work, id, n)
	if err != nil {
		(&ImproveHandler{mode: h.mode}).sendErr(ctx, "improvement "+id+" version "+strconv.Itoa(n), err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
