package handlers

import (
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// evaluationListOptions are the optional parameters of GET /evaluations
// (UI fix wave, G3): paging and q (id and name), status (a comma list of
// evaluation statuses, e.g. done for finished only) and fields=light.
type evaluationListOptions struct {
	paging   listPaging
	statuses []string
	light    bool
}

// on reports whether any option was given; without one the list answers
// exactly as before.
func (o evaluationListOptions) on() bool {
	return o.paging.on || len(o.statuses) > 0 || o.light
}

// evaluationStatuses are the values status= takes.
var evaluationStatuses = []string{
	schemas.EvaluationQueued, schemas.EvaluationRunning, schemas.EvaluationDone,
	schemas.EvaluationFailed, schemas.EvaluationCancelled,
}

// readEvaluationListOptions reads the options or answers a 400.
func readEvaluationListOptions(ctx *fasthttp.RequestCtx, mode schemas.Mode) (evaluationListOptions, bool) {
	var o evaluationListOptions
	p, ok := readListPaging(ctx, mode)
	if !ok {
		return o, false
	}
	o.paging = p
	args := ctx.QueryArgs()
	if raw := strings.TrimSpace(string(args.Peek("status"))); raw != "" {
		for _, s := range strings.Split(raw, ",") {
			s = strings.TrimSpace(s)
			if !slices.Contains(evaluationStatuses, s) {
				lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
					"status must be a comma list of "+strings.Join(evaluationStatuses, ", "))
				return o, false
			}
			o.statuses = append(o.statuses, s)
		}
	}
	switch string(args.Peek("fields")) {
	case "", "full":
	case "light":
		o.light = true
	default:
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "fields must be light or full")
		return o, false
	}
	return o, true
}

// EvaluationPagedList is GET /evaluations with any option: the evaluations
// kept, and with limit, offset or q the paging (total counts the rows that
// matched q and status).
type EvaluationPagedList struct {
	Evaluations []*schemas.Evaluation `json:"evaluations"`
	*ListPage
}

// listPaged answers GET /evaluations with options. The org check is the
// plain list's. Every evaluation of the org is refreshed as the plain list
// does (a refresh saves status changes and sends finish notifications, so a
// page never skips them); then q, status and the page cut the rows.
func (h *evaluationsHandler) listPaged(ctx *fasthttp.RequestCtx, o evaluationListOptions, evs []*schemas.Evaluation) {
	kept := make([]*schemas.Evaluation, 0, len(evs))
	for _, ev := range evs {
		if h.env.Services.OrgID != "" && ev.Org != "" && ev.Org != h.env.Services.OrgID {
			continue
		}
		h.refresh(ev)
		h.livePaused(ev)
		kept = append(kept, ev)
	}
	h.backfillResults(kept) // N161 (evalresult.go)
	kept = slices.DeleteFunc(kept, func(ev *schemas.Evaluation) bool {
		return !o.paging.match(ev.ID, ev.Spec.Name) || (len(o.statuses) > 0 && !slices.Contains(o.statuses, ev.Status))
	})
	var page *ListPage
	if o.paging.on {
		lo, hi := o.paging.window(len(kept))
		p := o.paging.page(len(kept))
		page, kept = &p, kept[lo:hi]
	}
	out := EvaluationPagedList{Evaluations: make([]*schemas.Evaluation, 0, len(kept)), ListPage: page}
	for _, ev := range kept {
		if o.light {
			ev = lightEvaluation(ev)
		}
		out.Evaluations = append(out.Evaluations, ev)
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, plainServed(out))
}

// lightEvaluation is the list row of an evaluation (fields=light): what the
// evaluations list, the comparisons list's names and the patterns picker
// read (id, name, mode, arms, benchmarks, status, children's benchmark,
// experiment, status and spend, result, dates), without the heavy parts: the
// spec's gate, stream, failover and launch detail, each child's skipped task
// list, fault map and skipped faults, the result's analysis ids and the
// notification time. The stored evaluation is not changed.
func lightEvaluation(ev *schemas.Evaluation) *schemas.Evaluation {
	c := *ev
	c.Spec.Gate, c.Spec.Stream, c.Spec.Failover = nil, nil, nil
	c.NotifiedAt = nil
	c.Children = make([]schemas.EvaluationChild, len(ev.Children))
	for i, ch := range ev.Children {
		ch.SkippedTasks, ch.FaultOf, ch.FaultsSkipped = nil, nil, nil
		c.Children[i] = ch
	}
	if ev.Result != nil {
		r := *ev.Result
		r.Analyses = nil
		c.Result = &r
	}
	return &c
}
