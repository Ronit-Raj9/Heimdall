package handlers

// Jobs area (P4): cancel, resume and regrade of an experiment, served by a
// Launcher whose experiments run as jobs (cmd/bifrost-bench/servejobs.go).

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// RunController is optionally implemented by a Launcher whose experiments run
// as experiment.run jobs.
type RunController interface {
	// Cancel requests the cancellation of the experiment's active run: no new
	// slot is leased and in-flight runs are stopped and sealed as
	// aborted_infra, never counted. ErrNotRunning when nothing runs.
	Cancel(ctx context.Context, expID string) error
	// Resume queues the experiment's run again; sealed runs are kept.
	Resume(ctx context.Context, expID string) error
	// Cancelled reports whether the experiment's newest run was cancelled.
	Cancelled(expID string) bool
}

// Regrader is optionally implemented by a Launcher: it queues an
// experiment.regate job, which recomputes the gates of the experiment's
// sealed runs as a new regrade (nothing runs again).
type Regrader interface {
	Regrade(ctx context.Context, expID string, req RegradeRequest) (*jobs.Job, error)
}

// RegradeRequest is the optional body of POST /experiments/{id}/regrade.
type RegradeRequest struct {
	// ID is the regrade id to write; default a new rg_ id.
	ID string `json:"id,omitempty"`
	// Note says why; every report that uses the regrade shows it.
	Note string `json:"note,omitempty"`
	// SuiteLock is the suite.lock the experiment was frozen with, relative to
	// the project folder, needed when the suite's lock changed since.
	SuiteLock string `json:"suite_lock,omitempty"`
}

// cancelExperiment handles POST /experiments/{id}/cancel: 202 with the
// status. The state reads cancelled once the run job has stopped.
func (h *ExperimentsHandler) cancelExperiment(ctx *fasthttp.RequestCtx) {
	rc, ok := h.launcher.(RunController)
	if !ok || h.launcher == nil {
		sendUnavailable(ctx, h.mode, "experiment cancel (job runner)")
		return
	}
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := rc.Cancel(withPrincipal(work, ctx), m.ExpID); err != nil {
		sendJobError(ctx, h.mode, "cancel experiment "+m.ExpID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, h.status(m, runs, false))
}

// resumeExperiment handles POST /experiments/{id}/resume: the run job is
// queued again and the scheduler resumes from the sealed runs. 202 with the
// status; 409 while a run job is active.
func (h *ExperimentsHandler) resumeExperiment(ctx *fasthttp.RequestCtx) {
	rc, ok := h.launcher.(RunController)
	if !ok || h.launcher == nil {
		sendUnavailable(ctx, h.mode, "experiment resume (job runner)")
		return
	}
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	if msg := experimentCapProblem(m, h.liveCapOf(m.ExpID), h.status(m, runs, false).SpentUSD); msg != "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, msg) // N2: the server refuses, not only the button
		return
	}
	if h.resumePaused(ctx, m, runs) { // run control: a pause is lifted first; an active run needs nothing more (runcontrol.go)
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := rc.Resume(withPrincipal(work, ctx), m.ExpID); err != nil {
		sendJobError(ctx, h.mode, "resume experiment "+m.ExpID, err)
		return
	}
	h.recorder.follow(m.ExpID)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, h.status(m, runs, true))
}

// regradeExperiment handles POST /experiments/{id}/regrade: 202 with the
// queued experiment.regate job. The regrade id is in the job's params; once
// the job succeeded, ?grade_source=<id> on the verdict reads it.
func (h *ExperimentsHandler) regradeExperiment(ctx *fasthttp.RequestCtx) {
	rg, ok := h.launcher.(Regrader)
	if !ok || h.launcher == nil {
		sendUnavailable(ctx, h.mode, "regrade (job runner)")
		return
	}
	m, _, ok := h.lookup(ctx)
	if !ok {
		return
	}
	var req RegradeRequest
	if body := bytes.TrimSpace(ctx.PostBody()); len(body) > 0 {
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be {\"id\"?, \"note\"?, \"suite_lock\"?}: "+err.Error())
			return
		}
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := rg.Regrade(withPrincipal(work, ctx), m.ExpID, req)
	if err != nil {
		sendJobError(ctx, h.mode, "regrade experiment "+m.ExpID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, j)
}

// withPrincipal carries the request's principal onto a work context, so a
// job a request queues records who started it.
func withPrincipal(work context.Context, ctx *fasthttp.RequestCtx) context.Context {
	if p, ok := auth.Lookup(ctx); ok {
		return auth.WithPrincipal(work, p)
	}
	return work
}

// cancelledFact reads whether the launcher reports the experiment cancelled.
func (h *ExperimentsHandler) cancelledFact(expID string) bool {
	if rc, ok := h.launcher.(RunController); ok {
		return rc.Cancelled(expID)
	}
	return false
}

// experimentCapProblem is why a comparison may not spend again (resume, a
// Jobs retry of its run): no spend cap at all, frozen or live, or a cap its
// spend already reached. Empty when it may (N2, K5's leftover).
func experimentCapProblem(m *schemas.Manifest, liveCap float64, spent schemas.Value) string {
	capUSD := max(m.Experiment.Budget.TotalUSD, liveCap)
	if capUSD <= 0 {
		return "This comparison has no spend cap. Set one in its run controls, then resume."
	}
	if v, ok := spent.Float(); ok && v >= capUSD {
		return "The spend cap is used up. Raise it in the run controls to continue."
	}
	return ""
}

// liveCapOf is the live spend cap of the comparison's run controls (PATCH
// /experiments/{id}/controls), 0 when none is set or it cannot be read.
func (h *ExperimentsHandler) liveCapOf(expID string) float64 {
	op, ok := h.launcher.(RunOperator)
	if !ok || op == nil {
		return 0
	}
	work, cancel := workContext(nil, readTimeout)
	defer cancel()
	st, _, err := op.Controls(work, expID)
	if err != nil || st == nil {
		return 0
	}
	return st.TotalUSD
}
