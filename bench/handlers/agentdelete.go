package handlers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Messages of DELETE /agents/{id} (lane F13). The UI shows them as they are.
const (
	agentDeleteBuiltinMsg = "Built-in agents cannot be deleted."
	agentDeleteRunningMsg = "This agent has evaluations still running. Cancel them first."
)

func init() {
	RegisterOrgArea("agentdelete", registerAgentDelete)
}

// registerAgentDelete registers DELETE /agents/{id}: it removes the agent's
// settings file, its image folder and its skills and tools folder. Saved
// versions (.heimdall/agent-versions/<id>/) and every run stay, so past
// results remain readable.
func registerAgentDelete(env OrgEnv, r *router.Router) {
	h := &agentDeleteHandler{env: env}
	r.DELETE(APIPrefix+"/agents/{id}", env.W(h.delete))
}

type agentDeleteHandler struct{ env OrgEnv }

func (h *agentDeleteHandler) fail(ctx *fasthttp.RequestCtx, status int, typ, msg string) {
	lib.SendError(ctx, h.env.Mode, status, typ, msg)
}

func (h *agentDeleteHandler) delete(ctx *fasthttp.RequestCtx) {
	ps := h.env.Services.Projects
	if ps == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "project files are not available on this server: start it with a project folder (bifrost-bench serve) or ask your server owner")
		return
	}
	id, _ := ctx.UserValue("id").(string)
	if !agentOpsNamePattern.MatchString(id) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("agent id %q: want letters, digits, '.', '-' or '_'", id))
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()

	rel, listed := agentDeleteFile(work, h.env.Services.Agents, ps, id)
	if rel == "" {
		if listed {
			h.fail(ctx, fasthttp.StatusConflict, lib.ErrTypeConflict, agentDeleteBuiltinMsg)
			return
		}
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, fmt.Sprintf("agent %q not found", id))
		return
	}

	evs, err := evalstore.Open(ps.Root()).List()
	if err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list evaluations: "+err.Error())
		return
	}
	if agentHasActiveEvaluation(evs, id) {
		h.fail(ctx, fasthttp.StatusConflict, lib.ErrTypeConflict, agentDeleteRunningMsg)
		return
	}

	opts := projectfs.WriteOpts{Actor: actor(ctx), Note: "delete agent " + id}
	if what, err := deleteAgentFiles(work, ps, id, rel, opts); err != nil {
		h.fsError(ctx, what, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// deleteAgentFiles removes agent id's settings file (agents-area path rel),
// its image folder and its skills and tools folder. Saved versions and runs
// stay. On failure it returns what failed ("delete agents/<path>") with the
// store error. DELETE /agents/{id} and the rename of POST /agents/{id}/copy
// both use it.
func deleteAgentFiles(work context.Context, ps projectfs.Store, id, rel string, opts projectfs.WriteOpts) (string, error) {
	if err := ps.Delete(work, projectfs.AreaAgents, rel, opts); err != nil {
		return "delete agents/" + rel, err
	}
	// The image folder and the skills and tools folder belong to this agent
	// only. "images" and "skills" are never an agent's own folder name.
	dirs := []string{"images/" + id}
	if id != "images" && id != "skills" {
		dirs = append(dirs, id)
	}
	for _, dir := range dirs {
		if err := ps.Delete(work, projectfs.AreaAgents, dir, opts); err != nil && !errors.Is(err, projectfs.ErrNotFound) {
			return "delete agents/" + dir, err
		}
	}
	return "", nil
}

// agentDeleteFile is the agents-area path of agent id's settings file in the
// org project ("" when the project has none) and whether the agent source
// lists the id at all (a listed agent without a project file comes from the
// built-in library).
func agentDeleteFile(ctx context.Context, src AgentSource, ps projectfs.Store, id string) (rel string, listed bool) {
	var candidates []string
	if src != nil {
		if files, err := src.AgentConfigs(ctx); err == nil {
			for _, f := range files {
				if f.Config != nil && f.Config.ID == id && strings.HasPrefix(f.Path, "agents/") {
					listed = true
					candidates = append(candidates, strings.TrimPrefix(f.Path, "agents/"))
				}
			}
		}
	}
	candidates = append(candidates, id+".yaml", id+".yml")
	for _, c := range candidates {
		if strings.Contains(c, "/") {
			continue
		}
		if _, _, err := ps.Read(ctx, projectfs.AreaAgents, c); err == nil {
			return c, true
		}
	}
	return "", listed
}

// agentHasActiveEvaluation reports whether a queued or running evaluation has
// an arm that runs agent id.
func agentHasActiveEvaluation(evs []*schemas.Evaluation, id string) bool {
	for _, ev := range evs {
		if ev == nil || (ev.Status != schemas.EvaluationQueued && ev.Status != schemas.EvaluationRunning) {
			continue
		}
		for _, arm := range ev.Spec.Arms {
			if arm.Agent == id {
				return true
			}
		}
	}
	return false
}

func (h *agentDeleteHandler) fsError(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case errors.Is(err, projectfs.ErrNotFound):
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": not found")
	case errors.Is(err, projectfs.ErrOutside), errors.Is(err, projectfs.ErrInvalid):
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, projectfs.ErrConflict):
		h.fail(ctx, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": changed on the server, reload and try again")
	default:
		h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, what+": "+err.Error())
	}
}
