package handlers

// Lane I28 (W4-41, D4 10.3): the first point where two runs of the same task
// did something different, and what each did and spent after it. It reads
// the step timeline and cost ledger of both runs; nothing is stored.

import (
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/steprules"
	"github.com/valyala/fasthttp"
)

// PairDivergence answers GET /pairs/{id}/divergence and
// GET /runs/{id}/divergence/{other}.
type PairDivergence struct {
	PairID     string                `json:"pair_id"`
	Arms       [2]string             `json:"arms"`
	Runs       [2]string             `json:"runs"`
	Outcomes   [2]string             `json:"outcomes"`  // verified outcomes from the run views
	Available  bool                  `json:"available"` // both runs have a step timeline
	Divergence *steprules.Divergence `json:"divergence,omitempty"`
	Reason     string                `json:"reason,omitempty"` // when not available
}

type divergenceHandler struct {
	mode  schemas.Mode
	index ReadIndex
}

func init() {
	RegisterOrgArea("divergence", func(env OrgEnv, r *router.Router) {
		h := &divergenceHandler{mode: env.Mode, index: env.Services.Index}
		r.GET(APIPrefix+"/pairs/{id}/divergence", env.R(h.pair))
		r.GET(APIPrefix+"/runs/{id}/divergence/{other}", env.R(h.runs))
	})
}

func (h *divergenceHandler) pair(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.mode, "id", "pair")
	if !ok {
		return
	}
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "the run index")
		return
	}
	views := h.index.Pair(id)
	if len(views) < 2 {
		sendNotFound(ctx, h.mode, "pair", id)
		return
	}
	h.answer(ctx, id, views[0], views[1])
}

func (h *divergenceHandler) runs(ctx *fasthttp.RequestCtx) {
	a, ok := idParam(ctx, h.mode, "id", "run")
	if !ok {
		return
	}
	b, ok := idParam(ctx, h.mode, "other", "run")
	if !ok {
		return
	}
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "the run index")
		return
	}
	va, ok := h.index.Run(a)
	if !ok {
		sendNotFound(ctx, h.mode, "run", a)
		return
	}
	vb, ok := h.index.Run(b)
	if !ok {
		sendNotFound(ctx, h.mode, "run", b)
		return
	}
	if va.Slot.Task != vb.Slot.Task {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "These runs are of different tasks.")
		return
	}
	pair := ""
	if va.Slot.PairID == vb.Slot.PairID {
		pair = va.Slot.PairID
	}
	h.answer(ctx, pair, *va, *vb)
}

func (h *divergenceHandler) answer(ctx *fasthttp.RequestCtx, pairID string, a, b RunView) {
	out := PairDivergence{PairID: pairID}
	views := [2]RunView{a, b}
	var steps [2][]schemas.StepRecord
	var costs [2]map[string]float64
	for i, v := range views {
		runID := v.Run.RunID
		out.Arms[i], out.Runs[i] = v.Slot.Arm, runID
		out.Outcomes[i] = string(v.Run.Outcome)
		read := func(n string) ([]byte, error) { return h.index.RunFile(runID, n) }
		s, _, err := steprules.Load(read, 0)
		if err != nil {
			sendDepError(ctx, h.mode, "read the steps of run "+runID, err)
			return
		}
		c, err := steprules.LoadCost(read)
		if err != nil {
			c = nil
		}
		steps[i], costs[i] = s, c
	}
	for i := range views {
		if len(steps[i]) == 0 {
			out.Reason = "Run " + out.Runs[i] + " was recorded before step timelines existed."
			lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
			return
		}
	}
	out.Available = true
	opt := steprules.DefaultDivergeOptions()
	opt.CostOf = func(side int, req string) (float64, bool) {
		if costs[side] == nil {
			return 0, false
		}
		v, ok := costs[side][req]
		return v, ok
	}
	d := steprules.FirstDivergence(steps[0], steps[1], opt)
	out.Divergence = &d
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}
