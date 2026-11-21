package handlers

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

// This file is the Suite ops area (web parity lane L11): quarantine and
// restore one task of a suite from the website. The CLI equivalents are
// `suite quarantine` and `suite restore`; both only touch the suite's
// suite.health.json sidecar, so suite.lock and existing experiments stay
// valid. Long suite actions (admit, lint, lock, task from commit, tasks
// mine, quarantine flaky) are job kinds in cmd/bifrost-bench/jobs_suiteops.go.

// maxQuarantineReason bounds the reason a user types.
const maxQuarantineReason = 500

// QuarantineRequest is the body of POST /suites/{id}/tasks/{task}/quarantine.
type QuarantineRequest struct {
	Reason string `json:"reason"`
}

// QuarantineResult answers the quarantine and restore routes.
type QuarantineResult struct {
	Suite      string                  `json:"suite"`
	Task       string                  `json:"task"`
	Quarantine *schemas.TaskQuarantine `json:"quarantine,omitempty"` // set after quarantine
	Changed    bool                    `json:"changed"`              // false when restore found the task not quarantined
}

func init() {
	RegisterOrgArea("suiteops", func(env OrgEnv, r *router.Router) {
		h := &suiteOpsHandler{mode: env.Mode, suites: env.Services.Suites}
		r.POST(APIPrefix+"/suites/{id}/tasks/{task}/quarantine", env.W(h.quarantine))
		r.POST(APIPrefix+"/suites/{id}/tasks/{task}/restore", env.W(h.restore))
	})
}

type suiteOpsHandler struct {
	mode   schemas.Mode
	suites SuiteSource
}

// loadTask resolves the suite and task of the path, or answers and returns nil.
func (h *suiteOpsHandler) loadTask(ctx *fasthttp.RequestCtx) (*schemas.Suite, string) {
	if h.suites == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"suite loader is not available on this server: start serve with a benchmarks folder (benchmarks_root in bench.yaml)")
		return nil, ""
	}
	id, ok := idParam(ctx, h.mode, "id", "suite")
	if !ok {
		return nil, ""
	}
	taskID, ok := idParam(ctx, h.mode, "task", "task")
	if !ok {
		return nil, ""
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ids, err := h.suites.SuiteIDs(work)
	if err != nil {
		sendDepError(ctx, h.mode, "list suites", err)
		return nil, ""
	}
	if !slices.Contains(ids, id) {
		sendNotFound(ctx, h.mode, "suite", id)
		return nil, ""
	}
	s, err := h.suites.LoadSuite(work, id)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			"load suite "+id+": "+err.Error()+"; fix suite.yaml (run Lint on the Ops tab) and retry")
		return nil, ""
	}
	if s.TaskByID(taskID) == nil {
		sendNotFound(ctx, h.mode, "task", id+"/"+taskID)
		return nil, ""
	}
	return s, taskID
}

// quarantine handles POST /api/bench/suites/{id}/tasks/{task}/quarantine.
func (h *suiteOpsHandler) quarantine(ctx *fasthttp.RequestCtx) {
	var req QuarantineRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			`invalid body: want {"reason": "why the task is kept out of new plans"}: `+err.Error())
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"a reason is required: say why the task is kept out of new plans (for example: verifier flaky on timeouts)")
		return
	}
	if len(req.Reason) > maxQuarantineReason {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "reason is longer than 500 characters")
		return
	}
	s, taskID := h.loadTask(ctx)
	if s == nil {
		return
	}
	q := schemas.TaskQuarantine{Reason: req.Reason, Source: schemas.QuarantineManual}
	if t := s.TaskByID(taskID); t != nil {
		q.TaskVersion = t.Version
	}
	if err := suite.Quarantine(s, taskID, q); err != nil {
		sendDepError(ctx, h.mode, "quarantine "+s.ID+"/"+taskID, err)
		return
	}
	f, err := suite.ReadHealthFile(s.Root)
	if err == nil {
		if got, ok := f.Quarantined[taskID]; ok {
			q = got
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, QuarantineResult{Suite: s.ID, Task: taskID, Quarantine: &q, Changed: true})
}

// restore handles POST /api/bench/suites/{id}/tasks/{task}/restore.
func (h *suiteOpsHandler) restore(ctx *fasthttp.RequestCtx) {
	s, taskID := h.loadTask(ctx)
	if s == nil {
		return
	}
	ok, err := suite.Restore(s, taskID)
	if err != nil {
		sendDepError(ctx, h.mode, "restore "+s.ID+"/"+taskID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, QuarantineResult{Suite: s.ID, Task: taskID, Changed: ok})
}
