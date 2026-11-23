package handlers

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// Messages of POST /agents/{id}/copy (fix wave 2, N156). The UI shows them as they are.
const (
	agentCopyBadNameMsg = "Use lowercase letters, digits, '.', '_' or '-', starting with a letter or digit, up to 63 characters."
	agentCopySameMsg    = "Pick a different name."
	agentCopyBuiltinMsg = "Built-in agents cannot be copied. Create a new agent from a template instead."
)

// AgentCopyRequest is the body of POST /agents/{id}/copy. RemoveOriginal
// makes the copy a rename: the original agent is deleted after the copy is
// written.
type AgentCopyRequest struct {
	NewID          string `json:"new_id"`
	RemoveOriginal bool   `json:"remove_original,omitempty"`
}

func init() {
	RegisterOrgArea("agentcopy", registerAgentCopy)
}

// registerAgentCopy registers POST /agents/{id}/copy: it writes the agent's
// settings file under a new name (with its image folder and its skills and
// tools folder) and starts the copy at version 1. With remove_original it
// renames the agent: the original is deleted as DELETE /agents/{id} does.
// Past runs, results and saved versions keep the old name.
func registerAgentCopy(env OrgEnv, r *router.Router) {
	h := &agentCopyHandler{env: env}
	r.POST(APIPrefix+"/agents/{id}/copy", env.W(h.copy))
}

type agentCopyHandler struct{ env OrgEnv }

func (h *agentCopyHandler) fail(ctx *fasthttp.RequestCtx, status int, typ, msg string) {
	lib.SendError(ctx, h.env.Mode, status, typ, msg)
}

func (h *agentCopyHandler) copy(ctx *fasthttp.RequestCtx) {
	ps := h.env.Services.Projects
	if ps == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "project files are not available on this server: start it with a project folder or ask your server owner")
		return
	}
	id, _ := ctx.UserValue("id").(string)
	if !agentOpsNamePattern.MatchString(id) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("agent id %q: want letters, digits, '.', '-' or '_'", id))
		return
	}
	var req AgentCopyRequest
	if err := abDecodeBody(ctx, &req); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	newID := strings.TrimSpace(req.NewID)
	if !agentIDRe.MatchString(newID) {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, agentCopyBadNameMsg)
		return
	}
	if newID == id {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, agentCopySameMsg)
		return
	}
	if newID == "new" || newID == "diff" || newID == "templates" || newID == "config" || newID == "images" || newID == "skills" {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("The name %s is reserved. Pick another name.", newID))
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()

	src := h.env.Services.Agents
	rel, listed := agentDeleteFile(work, src, ps, id)
	if rel == "" {
		if listed {
			h.fail(ctx, fasthttp.StatusConflict, lib.ErrTypeConflict, agentCopyBuiltinMsg)
			return
		}
		h.fail(ctx, fasthttp.StatusNotFound, lib.ErrTypeNotFound, fmt.Sprintf("agent %q not found", id))
		return
	}
	if agentCopyTaken(work, src, ps, newID) {
		h.fail(ctx, fasthttp.StatusConflict, lib.ErrTypeConflict, fmt.Sprintf("An agent named %s already exists. Pick another name.", newID))
		return
	}
	if req.RemoveOriginal {
		evs, err := evalstore.Open(ps.Root()).List()
		if err != nil {
			h.fail(ctx, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list evaluations: "+err.Error())
			return
		}
		if agentHasActiveEvaluation(evs, id) {
			h.fail(ctx, fasthttp.StatusConflict, lib.ErrTypeConflict, agentDeleteRunningMsg)
			return
		}
	}

	doc, _, err := ps.Read(work, projectfs.AreaAgents, rel)
	if err != nil {
		h.fsError(ctx, "read agents/"+rel, err)
		return
	}
	newDoc, err := editAgentYAML(doc, func(root *yaml.Node) { agentCopyRename(root, id, newID) })
	if err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	newRel := newID + ".yaml"
	if _, err := decodeAgent(newDoc, "agents/"+newRel); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "the copied agent does not validate: "+err.Error())
		return
	}

	actor := abActor(ctx)
	opts := projectfs.WriteOpts{Actor: actor, Note: "agent copied from " + id}
	// Folders first, the YAML last: a half-written copy never shows up in the list.
	dirs := [][2]string{{"images/" + id, "images/" + newID}}
	if id != "images" && id != "skills" {
		dirs = append(dirs, [2]string{id, newID})
	}
	for _, d := range dirs {
		if what, err := agentCopyDir(work, ps, d[0], d[1], opts); err != nil {
			h.fsError(ctx, what, err)
			return
		}
	}
	e, err := ps.Write(work, projectfs.AreaAgents, newRel, newDoc, opts)
	if err != nil {
		h.fsError(ctx, "write agents/"+newRel, err)
		return
	}
	out := AgentConfigDoc{ID: newID, Path: "agents/" + newRel, YAML: string(newDoc), SHA256: e.SHA256}
	// Agent versions: the copy starts at v1. A failure is logged there; the copy itself stands.
	out.Version, out.VersionCreated, _ = snapshotAgentSave(work, ps, newRel, newID, newDoc, actor, "copied from "+id)

	if req.RemoveOriginal {
		delOpts := projectfs.WriteOpts{Actor: actor, Note: "rename agent " + id + " to " + newID}
		if what, err := deleteAgentFiles(work, ps, id, rel, delOpts); err != nil {
			h.fsError(ctx, what, err)
			return
		}
	}
	(&agentBuilder{mode: h.env.Mode, projects: ps}).sendDocVersion(ctx, fasthttp.StatusCreated, out)
}

// agentCopyTaken reports whether id is already used: a settings file, an
// image folder or a skills and tools folder in the project, or an agent the
// agent source lists (built-in ones too).
func agentCopyTaken(ctx context.Context, src AgentSource, ps projectfs.Store, id string) bool {
	if rel, listed := agentDeleteFile(ctx, src, ps, id); rel != "" || listed {
		return true
	}
	for _, rp := range []string{"images/" + id, id} {
		if _, _, err := ps.Read(ctx, projectfs.AreaAgents, rp); err == nil || errors.Is(err, projectfs.ErrInvalid) { // ErrInvalid: it is a folder
			return true
		}
	}
	return false
}

// agentCopyRename points a copied agent YAML at its new name: the top-level
// id, and every string value that names the old agent's folders
// (agents/images/<old>/..., agents/<old>/...) or its built image
// (bench/agent-<old>:tag).
func agentCopyRename(root *yaml.Node, oldID, newID string) {
	abSetPath(root, newID, "id")
	prefixes := [][2]string{
		{"agents/images/" + oldID + "/", "agents/images/" + newID + "/"},
		{"agents/" + oldID + "/", "agents/" + newID + "/"},
		{"bench/agent-" + oldID + ":", "bench/agent-" + newID + ":"},
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		if n.Kind == yaml.ScalarNode {
			for _, p := range prefixes {
				if strings.HasPrefix(n.Value, p[0]) {
					n.Value = p[1] + strings.TrimPrefix(n.Value, p[0])
					return
				}
			}
			return
		}
		for i, c := range n.Content {
			if n.Kind == yaml.MappingNode && i%2 == 0 {
				continue // keys stay
			}
			walk(c)
		}
	}
	walk(root)
}

// agentCopyDir copies every file under the agents-area folder from to the
// folder to. A missing source folder copies nothing. It returns what failed.
func agentCopyDir(ctx context.Context, ps projectfs.Store, from, to string, opts projectfs.WriteOpts) (string, error) {
	ents, err := ps.List(ctx, projectfs.AreaAgents, from, true)
	if errors.Is(err, projectfs.ErrNotFound) || errors.Is(err, projectfs.ErrInvalid) {
		return "", nil
	}
	if err != nil {
		return "list agents/" + from, err
	}
	for _, e := range ents {
		if e.Dir || !strings.HasPrefix(e.Path, from+"/") {
			continue
		}
		data, _, err := ps.Read(ctx, projectfs.AreaAgents, e.Path)
		if err != nil {
			return "read agents/" + e.Path, err
		}
		dst := path.Join(to, strings.TrimPrefix(e.Path, from+"/"))
		o := opts
		// A copied scan record stays bound to the identical copied files.
		o.System = projectfs.IsReserved(projectfs.AreaAgents, dst)
		if _, err := ps.Write(ctx, projectfs.AreaAgents, dst, data, o); err != nil {
			return "write agents/" + dst, err
		}
	}
	return "", nil
}

func (h *agentCopyHandler) fsError(ctx *fasthttp.RequestCtx, what string, err error) {
	(&agentDeleteHandler{env: h.env}).fsError(ctx, what, err)
}
