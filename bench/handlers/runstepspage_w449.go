package handlers

import (
	"fmt"
	"strconv"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// W4-49 D: the step timeline a page at a time, and one step with the steps
// around it, so a reader (a person or the MCP investigation tools) never
// has to fetch the whole timeline.

// Page sizes of GET /runs/{id}/steps/page.
const (
	StepsPageDefault = 50
	StepsPageMax     = 200
	stepChildrenMax  = 50
)

// RunStepsPage answers GET /runs/{id}/steps/page.
type RunStepsPage struct {
	RunID        string               `json:"run_id"`
	Steps        []schemas.StepRecord `json:"steps"`
	NextAfterSeq *int                 `json:"next_after_seq"`
	Total        int                  `json:"total"`
	Truncated    bool                 `json:"truncated"`
}

// RunStep answers GET /runs/{id}/step/{seq}.
type RunStep struct {
	Step      schemas.StepRecord   `json:"step"`
	Witnesses []schemas.StepRecord `json:"witnesses"`
	WitnessOf *schemas.StepRecord  `json:"witness_of"`
	Children  []schemas.StepRecord `json:"children"`
}

func init() {
	RegisterOrgArea("runstepspage_w449", func(env OrgEnv, r *router.Router) {
		registerStepPages(r, env.Mode, env.Services.Index, env.Common...)
	})
}

func registerStepPages(r *router.Router, mode schemas.Mode, index ReadIndex, mw ...lib.Middleware) {
	h := &RunsHandler{mode: mode, index: index}
	r.GET(APIPrefix+"/runs/{id}/steps/page", lib.ChainMiddlewares(h.getRunStepsPage, mw...))
	r.GET(APIPrefix+"/runs/{id}/step/{seq}", lib.ChainMiddlewares(h.getRunStep, mw...))
}

// StepsPageFilter selects the rows of one page.
type StepsPageFilter struct {
	AfterSeq  int
	Limit     int
	Kind      string
	AgentID   string
	ParentSeq *int
}

// PageSteps cuts one page out of a timeline: rows with seq above AfterSeq
// that match every filter given, at most Limit of them. next is the last
// returned seq when more rows match, else nil; total counts every match.
func PageSteps(steps []schemas.StepRecord, f StepsPageFilter) (page []schemas.StepRecord, next *int, total int) {
	page = []schemas.StepRecord{}
	for _, s := range steps {
		if f.Kind != "" && s.Kind != f.Kind {
			continue
		}
		if f.AgentID != "" && s.AgentID != f.AgentID {
			continue
		}
		if f.ParentSeq != nil && s.ParentSeq != *f.ParentSeq {
			continue
		}
		total++
		if s.Seq <= f.AfterSeq {
			continue
		}
		if len(page) < f.Limit {
			page = append(page, s)
		} else if next == nil {
			last := page[len(page)-1].Seq
			next = &last
		}
	}
	return page, next, total
}

func queryInt(ctx *fasthttp.RequestCtx, name string, def int) (int, bool) {
	v := string(ctx.QueryArgs().Peek(name))
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

func (h *RunsHandler) getRunStepsPage(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	f := StepsPageFilter{Kind: string(ctx.QueryArgs().Peek("kind")), AgentID: string(ctx.QueryArgs().Peek("agent_id"))}
	var good bool
	if f.AfterSeq, good = queryInt(ctx, "after_seq", 0); !good || f.AfterSeq < 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "after_seq must be a whole number, 0 or more")
		return
	}
	if f.Limit, good = queryInt(ctx, "limit", StepsPageDefault); !good || f.Limit < 1 || f.Limit > StepsPageMax {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("limit must be a whole number from 1 to %d", StepsPageMax))
		return
	}
	if v := string(ctx.QueryArgs().Peek("parent_seq")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parent_seq must be a whole number")
			return
		}
		f.ParentSeq = &n
	}
	all, err := readRunSteps(h.index, rv.Run.RunID)
	if err != nil {
		sendDepError(ctx, h.mode, "read step timeline of run "+rv.Run.RunID, err)
		return
	}
	page, next, total := PageSteps(all.Steps, f)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, RunStepsPage{RunID: rv.Run.RunID, Steps: page, NextAfterSeq: next, Total: total, Truncated: all.Truncated})
}

// StepAround finds one step with its witnesses, the row it witnesses and
// its children (at most 50). ok is false when no row has that seq.
func StepAround(steps []schemas.StepRecord, seq int) (RunStep, bool) {
	out := RunStep{Witnesses: []schemas.StepRecord{}, Children: []schemas.StepRecord{}}
	bySeq := make(map[int]schemas.StepRecord, len(steps))
	for _, s := range steps {
		bySeq[s.Seq] = s
	}
	st, ok := bySeq[seq]
	if !ok {
		return out, false
	}
	out.Step = st
	for _, w := range st.WitnessedBy {
		if r, found := bySeq[w]; found {
			out.Witnesses = append(out.Witnesses, r)
		}
	}
	if st.WitnessOf != 0 {
		if r, found := bySeq[st.WitnessOf]; found {
			out.WitnessOf = &r
		}
	}
	for _, s := range steps {
		if s.ParentSeq == seq && s.Seq != seq && len(out.Children) < stepChildrenMax {
			out.Children = append(out.Children, s)
		}
	}
	return out, true
}

func (h *RunsHandler) getRunStep(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	raw := pathParam(ctx, "seq")
	seq, err := strconv.Atoi(raw)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "the step number must be a whole number")
		return
	}
	all, err := readRunSteps(h.index, rv.Run.RunID)
	if err != nil {
		sendDepError(ctx, h.mode, "read step timeline of run "+rv.Run.RunID, err)
		return
	}
	out, found := StepAround(all.Steps, seq)
	if !found {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, fmt.Sprintf("Run %s has no step %d.", rv.Run.RunID, seq))
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
