package handlers

// Run control for evaluations (runcontrol.go): pause and resume act on every
// child experiment that still runs; "Rerun failures" creates a new,
// exploratory evaluation whose children rerun each child's failed tasks.

import (
	"errors"
	"strings"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// EvaluationRerunResult is POST /evaluations/{id}/rerun-failures.
type EvaluationRerunResult struct {
	Evaluation *schemas.Evaluation    `json:"evaluation"`
	Children   []*RerunFailuresResult `json:"children"`
	// Skipped says, per benchmark, why it got no rerun (nothing failed, not
	// finished, could not be planned).
	Skipped map[string]string `json:"skipped,omitempty"`
}

func (h *evaluationsHandler) operator(ctx *fasthttp.RequestCtx) (RunOperator, bool) {
	op, ok := h.env.Services.Launcher.(RunOperator)
	if !ok || h.env.Services.Launcher == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "run controls need the job runner on this server")
		return nil, false
	}
	return op, true
}

// runningChildren returns the children that still run.
func runningChildren(ev *schemas.Evaluation) []*schemas.EvaluationChild {
	var out []*schemas.EvaluationChild
	for i := range ev.Children {
		c := &ev.Children[i]
		if c.ExperimentID != "" && !terminalChild(c.Status) && c.Status != string(schemas.ExperimentCreatedState) {
			out = append(out, c)
		}
	}
	return out
}

// markPaused sets ev.Paused from the children's controls.
func (h *evaluationsHandler) markPaused(op RunOperator, ev *schemas.Evaluation) {
	kids := runningChildren(ev)
	ev.Paused = len(kids) > 0
	work, cancel := workContext(nil, readTimeout)
	defer cancel()
	for _, c := range kids {
		st, _, err := op.Controls(work, c.ExperimentID)
		if (err != nil && !errors.Is(err, runctl.ErrBrokenLog)) || st == nil || !st.Paused() {
			ev.Paused = false
			return
		}
	}
}

// pause handles POST /evaluations/{id}/pause: every running child drains.
func (h *evaluationsHandler) pause(ctx *fasthttp.RequestCtx) {
	h.control(ctx, true)
}

// resume handles POST /evaluations/{id}/resume: every paused child resumes.
func (h *evaluationsHandler) resume(ctx *fasthttp.RequestCtx) {
	h.control(ctx, false)
}

func (h *evaluationsHandler) control(ctx *fasthttp.RequestCtx, pause bool) {
	op, ok := h.operator(ctx)
	if !ok {
		return
	}
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	var req RunControlRequest
	if !decodeOptional(ctx, h.env.Mode, &req, `{"reason"?}`) {
		return
	}
	h.refresh(ev)
	kids := runningChildren(ev)
	if len(kids) == 0 {
		h.fail(ctx, fasthttp.StatusConflict, "evaluation "+ev.ID+" has no running benchmark ("+ev.Status+")")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	wctx := withPrincipal(work, ctx)
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = "evaluation " + ev.ID
	} else {
		reason = "evaluation " + ev.ID + ": " + reason
	}
	var errs []string
	for _, c := range kids {
		var err error
		if pause {
			_, err = op.Pause(wctx, c.ExperimentID, reason)
		} else {
			_, _, err = op.Unpause(wctx, c.ExperimentID, reason)
		}
		if err != nil {
			errs = append(errs, c.Benchmark+": "+err.Error())
		}
	}
	h.markPaused(op, ev)
	if len(errs) > 0 {
		h.fail(ctx, fasthttp.StatusBadGateway, "some benchmarks did not change: "+strings.Join(errs, "; "))
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, ev)
}

// rerunFailures handles POST /evaluations/{id}/rerun-failures: a new
// evaluation, rerun_of the parent, with one exploratory child per finished
// benchmark that had failures.
func (h *evaluationsHandler) rerunFailures(ctx *fasthttp.RequestCtx) {
	s := h.env.Services
	if s.Launcher == nil || s.Index == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "no experiment launcher on this server")
		return
	}
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	var req RerunFailuresRequest
	if !decodeOptional(ctx, h.env.Mode, &req, `{"outcomes"?, "reason"?}`) {
		return
	}
	h.refresh(ev)
	if !terminalEvaluation(ev.Status) {
		h.fail(ctx, fasthttp.StatusConflict, "evaluation "+ev.ID+" is still running: rerun its failures once it has finished")
		return
	}
	now := h.now().UTC()
	who := ""
	if p := principalOf(ctx); p != "" {
		who = p
	}
	child := &schemas.Evaluation{
		ID:  "ev_" + now.Format("20060102_150405") + "_" + randHex(3),
		Org: s.OrgID, Spec: ev.Spec, Status: schemas.EvaluationQueued, RerunOf: ev.ID,
		Children: []schemas.EvaluationChild{}, CreatedAt: now, CreatedBy: who,
	}
	child.Spec.Name = strings.TrimSuffix(ev.Spec.Name, " (failures)") + " (failures)"
	out := &EvaluationRerunResult{Evaluation: child, Children: []*RerunFailuresResult{}, Skipped: map[string]string{}}
	work, cancel := workContext(ctx, evaluationLaunchTimeout)
	defer cancel()
	var benches []string
	for _, c := range ev.Children {
		if c.ExperimentID == "" || c.Status == schemas.EvaluationChildSkipped {
			continue
		}
		m, found := s.Index.Manifest(c.ExperimentID)
		if !found {
			out.Skipped[c.Benchmark] = "its experiment is not on this server"
			continue
		}
		exp, n, err := RerunExperiment(m, s.Index.Runs(c.ExperimentID), req.Outcomes, now)
		if err != nil {
			if errors.Is(err, runctl.ErrInvalid) {
				h.fail(ctx, fasthttp.StatusBadRequest, err.Error())
				return
			}
			out.Skipped[c.Benchmark] = err.Error()
			continue
		}
		exp.EvaluationID = child.ID
		res, err := launchRerun(work, ctx, s.Launcher, m, exp, n)
		if err != nil {
			out.Skipped[c.Benchmark] = err.Error()
			continue
		}
		out.Children = append(out.Children, res)
		benches = append(benches, c.Benchmark)
		status := string(schemas.ExperimentRunning)
		if !res.Started {
			status = string(schemas.ExperimentCreatedState)
		}
		child.Children = append(child.Children, schemas.EvaluationChild{Benchmark: c.Benchmark, ExperimentID: res.ExperimentID, Status: status})
	}
	if len(child.Children) == 0 {
		h.fail(ctx, fasthttp.StatusConflict, "evaluation "+ev.ID+" has no benchmark with a failure to rerun")
		return
	}
	child.Spec.Benchmarks = benches
	child.Spec.Pack = ""
	child.Status = evaluationStatus(&schemas.Evaluation{Status: schemas.EvaluationRunning, Children: child.Children})
	if err := h.store().Save(child); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "save evaluation: "+err.Error())
		return
	}
	if len(out.Skipped) == 0 {
		out.Skipped = nil
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, out)
}

// principalOf is the request principal's email or user id.
func principalOf(ctx *fasthttp.RequestCtx) string {
	p := auth.PrincipalFrom(ctx)
	if p == nil {
		return ""
	}
	if p.Email != "" {
		return p.Email
	}
	return p.UserID
}
