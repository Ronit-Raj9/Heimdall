package handlers

// Job priority (Z10, INFRA_I4 item 2): an admin moves a queued job of the
// organization up or down the claim order, e.g. a release gate's
// experiment. The claim is round robin across organizations first, so a
// priority reorders only among organizations with equally many running
// jobs of the kind: it cannot starve another organization.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// JobPriorityAPI is the part of the job runner that reorders a queued job
// (*jobs.Runner). A JobsAPI without it answers 503 on the route.
type JobPriorityAPI interface {
	SetPriority(ctx context.Context, orgID, id string, priority int) (*jobs.Job, error)
}

var _ JobPriorityAPI = (*jobs.Runner)(nil)

// JobPriority is the body of POST /jobs/{id}/priority.
type JobPriority struct {
	// Priority is -10 to 10: 0 normal, higher is claimed sooner.
	Priority int `json:"priority"`
}

// priority handles POST /jobs/{id}/priority: 200 with the job; 409 when the
// job already started or ended.
func (h *JobsHandler) priority(ctx *fasthttp.RequestCtx) {
	org, _, ok := h.org(ctx, auth.RoleAdmin)
	if !ok {
		return
	}
	api, ok := h.api.(JobPriorityAPI)
	if !ok {
		sendUnavailable(ctx, h.mode, "job priority")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "job")
	if !ok {
		return
	}
	var body JobPriority
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, `body must be {"priority": -10..10}: `+err.Error())
		return
	}
	if body.Priority < jobs.MinPriority || body.Priority > jobs.MaxPriority {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "priority must be -10 to 10")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := api.SetPriority(work, org, id, body.Priority)
	if err != nil {
		if errors.Is(err, jobs.ErrNotQueued) {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "set job priority: "+err.Error())
			return
		}
		h.sendError(ctx, "set job priority "+id, err)
		return
	}
	if h.audit != nil {
		h.audit(ctx, org, "job.priority", "job", j.ID, map[string]any{"kind": j.Kind, "priority": j.Priority})
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, j)
}
