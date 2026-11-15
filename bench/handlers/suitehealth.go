package handlers

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskhealth"
	"github.com/valyala/fasthttp"
)

// This file is the Suite health area of the suites API
// (docs/suite-health.md, docs/held-out-and-canaries.md): quarantine state and
// the held-out split on GET /suites and GET /suites/{id}/tasks/{task}, and
// GET /suites/{id}/health.

// WithHeldOutVisible sets whether held-out tasks are served like public ones
// (server config held_out.visible). The default, false, lists a held-out
// task by id with its lock status only and refuses its detail, so its
// instruction text and files never reach a browser, a screenshot or an API
// client by accident. Anyone who can read the benchmarks folder can still
// read them; this is not access control.
func (h *SuitesHandler) WithHeldOutVisible(visible bool) *SuitesHandler {
	h.heldOutVisible = visible
	return h
}

// healthSummary adds the quarantine sidecar, the held-out split and the
// canary flag to a suite summary, and strips held-out tasks to their id and
// lock status unless they are visible. A sidecar that cannot be read leaves
// quarantine unset here; the planner refuses to plan on it and
// GET /suites/{id}/health reports the error.
func (h *SuitesHandler) healthSummary(s *schemas.Suite, out *schemas.SuiteSummary) {
	out.HasCanary = s.Canary != ""
	quarantined, _ := readQuarantineFile(s.Root)
	for i := range out.Tasks {
		st := &out.Tasks[i]
		if q, ok := quarantined[st.ID]; ok {
			st.Quarantine = &q
			out.QuarantinedCount++
		}
		if t := s.TaskByID(st.ID); t == nil || t.Split != schemas.SplitHeldOut {
			continue
		}
		out.HeldOutCount++
		st.Split = schemas.SplitHeldOut
		if !h.heldOutVisible {
			*st = redactTask(*st)
		}
	}
}

// refuseHeldOut answers 404 for a held-out task's detail unless held-out
// tasks are visible, and reports whether it did.
func (h *SuitesHandler) refuseHeldOut(ctx *fasthttp.RequestCtx, suiteID string, t *schemas.Task) bool {
	if h.heldOutVisible || t.Split != schemas.SplitHeldOut {
		return false
	}
	lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
		"task "+suiteID+"/"+t.ID+" is held out; its instruction and files are not served (server config held_out.visible is false)")
	return true
}

// redactTask keeps what a held-out task may show: its id, version, lock
// status, admission state and quarantine. Title, project, kind, tags,
// capabilities, provenance, admission checks, counts and scenario fields are
// dropped: each can describe the task well enough to leak it.
func redactTask(st schemas.SuiteTask) schemas.SuiteTask {
	return schemas.SuiteTask{
		ID: st.ID, Version: st.Version, Status: st.Status, Stale: st.Stale, Admitted: st.Admitted, AdmittedAt: st.AdmittedAt,
		Capabilities: []string{}, Tags: []string{}, Checks: []schemas.SuiteAdmissionCheck{},
		Split: schemas.SplitHeldOut, Redacted: true, Quarantine: st.Quarantine,
	}
}

// readQuarantineFile reads the quarantined tasks of a suite's sidecar
// (schemas.SuiteHealthFileName). A missing file means none.
func readQuarantineFile(root string) (map[string]schemas.TaskQuarantine, error) {
	data, err := os.ReadFile(filepath.Join(root, schemas.SuiteHealthFileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f schemas.SuiteHealthFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return f.Quarantined, nil
}

// SuiteHealthHandler serves GET /suites/{id}/health: per task, verifier
// flakiness, agent outcome variance, infra trouble and observed difficulty,
// computed from every sealed run of the suite across experiments.
type SuiteHealthHandler struct {
	mode   schemas.Mode
	suites SuiteSource
	source taskhealth.Source
	now    func() time.Time
}

// NewSuiteHealthHandler creates the suite health handler. Either dependency
// may be nil; the route then answers 503.
func NewSuiteHealthHandler(mode schemas.Mode, suites SuiteSource, source taskhealth.Source) *SuiteHealthHandler {
	return &SuiteHealthHandler{mode: mode, suites: suites, source: source, now: time.Now}
}

// RegisterRoutes registers GET /suites/{id}/health.
func (h *SuiteHealthHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/suites/{id}/health", lib.ChainMiddlewares(h.getHealth, middlewares...))
}

// getHealth handles GET /api/bench/suites/{id}/health. It reads the run
// store on every request; nothing is cached, so a new run shows at once.
func (h *SuiteHealthHandler) getHealth(ctx *fasthttp.RequestCtx) {
	if h.suites == nil {
		sendUnavailable(ctx, h.mode, "suite loader")
		return
	}
	if h.source == nil {
		sendUnavailable(ctx, h.mode, "run store")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "suite")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ids, err := h.suites.SuiteIDs(work)
	if err != nil {
		sendDepError(ctx, h.mode, "list suites", err)
		return
	}
	if !slices.Contains(ids, id) {
		sendNotFound(ctx, h.mode, "suite", id)
		return
	}
	s, err := h.suites.LoadSuite(work, id)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "load suite "+id+": "+err.Error())
		return
	}
	quarantined, err := readQuarantineFile(s.Root)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "read "+schemas.SuiteHealthFileName+" of "+id+": "+err.Error())
		return
	}
	out, err := taskhealth.Compute(work, h.source, s, quarantined, taskhealth.Options{Now: h.now})
	if err != nil {
		sendDepError(ctx, h.mode, "suite health "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
