package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

// AgentVersionEntry is one version in GET /agents/{id}/versions.
type AgentVersionEntry struct {
	schemas.AgentVersion
	Runs int `json:"runs"`
}

// AgentVersionList is GET /agents/{id}/versions.
type AgentVersionList struct {
	Agent    string              `json:"agent"`
	Builtin  bool                `json:"builtin,omitempty"`
	Versions []AgentVersionEntry `json:"versions"`
}

// AgentVersionDoc is GET /agents/{id}/versions/{n}.
type AgentVersionDoc struct {
	Version int    `json:"version"`
	Hash    string `json:"hash"`
	YAML    string `json:"yaml"`
}

// AgentVersionCreate is the body of POST /agents/{id}/versions.
type AgentVersionCreate struct {
	Note string `json:"note"`
}

// AgentVersionCreated is POST /agents/{id}/versions.
type AgentVersionCreated struct {
	schemas.AgentVersion
	Created bool `json:"created"`
}

type agentVersionsHandler struct {
	mode     schemas.Mode
	projects projectfs.Store
	index    ReadIndex
	library  string
}

func init() {
	RegisterOrgArea("agentversions", func(env OrgEnv, r *router.Router) {
		h := &agentVersionsHandler{mode: env.Mode, projects: env.Services.Projects, index: env.Services.Index, library: suite.LibraryDir()}
		r.GET(APIPrefix+"/agents/{id}/versions", env.R(h.list))
		r.GET(APIPrefix+"/agents/{id}/versions/{n}", env.R(h.get))
		r.POST(APIPrefix+"/agents/{id}/versions", env.W(h.create))
	})
}

// snapshotAgentSave records a just-saved agents/<rel> as an agent version,
// linked to the Files API version that save made (no second history: the
// version entry names that file version). It is best effort: the save
// already happened, and a document that does not decode as an agent is not
// versioned.
func snapshotAgentSave(ctx context.Context, store projectfs.Store, rel, id string, doc []byte, actor, note string) {
	if store == nil || !agentversions.ValidID(id) {
		return
	}
	_, _, _ = agentversions.Record(store.Root(), id, agentFileHistory(ctx, store, rel, doc, actor, note))
}

// agentFileHistory returns the Files API history of agents/<rel>, oldest
// first, ending with current: the newest file version when it holds current
// (linked by id), else current alone. Agent versions are backfilled from it
// when an agent has none yet (agentversions.Record).
func agentFileHistory(ctx context.Context, store projectfs.Store, rel string, current []byte, actor, note string) []agentversions.Source {
	sum := sha256.Sum256(current)
	cur := agentversions.Source{Doc: current, Actor: actor, Note: note, FileSHA256: hex.EncodeToString(sum[:])}
	fvs, err := store.Versions(ctx, projectfs.AreaAgents, rel)
	if err != nil || len(fvs) == 0 {
		return []agentversions.Source{cur}
	}
	if fvs[0].SHA256 == cur.FileSHA256 { // newest first
		cur.FileVersion, cur.At = fvs[0].ID, fvs[0].At
		if cur.Actor == "" {
			cur.Actor = fvs[0].Actor
		}
		if cur.Note == "" {
			cur.Note = fvs[0].Note
		}
		fvs = fvs[1:]
	}
	out := make([]agentversions.Source, 0, len(fvs)+1)
	for i := len(fvs) - 1; i >= 0; i-- {
		fv := fvs[i]
		if fv.SHA256 == "" { // a deletion
			continue
		}
		b, err := store.ReadVersion(ctx, projectfs.AreaAgents, rel, fv.ID)
		if err != nil {
			continue
		}
		out = append(out, agentversions.Source{Doc: b, At: fv.At, Actor: fv.Actor, Note: fv.Note, FileVersion: fv.ID, FileSHA256: fv.SHA256})
	}
	return append(out, cur)
}

// agentIDFromAgentsFile returns the agent id of a top-level agents-area
// file <id>.yaml (or .yml), or "" for any other path.
func agentIDFromAgentsFile(rel string) string {
	rel = strings.TrimPrefix(rel, "/")
	if strings.Contains(rel, "/") {
		return ""
	}
	ext := path.Ext(rel)
	if ext != ".yaml" && ext != ".yml" {
		return ""
	}
	id := strings.TrimSuffix(rel, ext)
	if !agentversions.ValidID(id) {
		return ""
	}
	return id
}

func (h *agentVersionsHandler) agentID(ctx *fasthttp.RequestCtx) (string, bool) {
	id, _ := ctx.UserValue("id").(string)
	if !agentversions.ValidID(id) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("agent id %q is not valid", id))
		return "", false
	}
	return id, true
}

func (h *agentVersionsHandler) ready(ctx *fasthttp.RequestCtx) bool {
	if h.projects == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"project files are not available on this server: start it with a project folder (serve -root) to version agents")
		return false
	}
	return true
}

// projectAgentFile reads agents/<id>.yaml (or .yml) of the project and
// returns it with its area-relative path.
func (h *agentVersionsHandler) projectAgentFile(ctx *fasthttp.RequestCtx, id string) ([]byte, string, bool) {
	for _, ext := range []string{".yaml", ".yml"} {
		work, cancel := workContext(ctx, readTimeout)
		b, _, err := h.projects.Read(work, projectfs.AreaAgents, id+ext)
		cancel()
		if err == nil {
			return b, id + ext, true
		}
	}
	return nil, "", false
}

// builtin returns the library file of id when the project has no agent of
// that name: such an agent is read-only with one synthetic v1.
func (h *agentVersionsHandler) builtin(ctx *fasthttp.RequestCtx, id string) string {
	if _, _, ok := h.projectAgentFile(ctx, id); ok {
		return ""
	}
	return agentversions.LibraryFile(h.library, id)
}

// runsByHash counts runs per agent hash (agentversions.AgentHash: the
// version identity, which leaves out the model and provider an evaluation
// sets per arm) over every experiment.
func (h *agentVersionsHandler) runsByHash() map[string]int {
	counts := map[string]int{}
	if h.index == nil {
		return counts
	}
	for _, exp := range h.index.Experiments() {
		m, _ := h.index.Manifest(exp)
		if m == nil {
			continue
		}
		armHash := map[string]string{}
		for id, arm := range m.Arms {
			if ah, err := agentversions.AgentHash(arm.Config); err == nil {
				armHash[id] = strings.TrimPrefix(ah, "sha256:")
			}
		}
		for _, rv := range h.index.Runs(exp) {
			if ah := armHash[rv.Slot.Arm]; ah != "" {
				counts[ah]++
			}
		}
	}
	return counts
}

func (h *agentVersionsHandler) list(ctx *fasthttp.RequestCtx) {
	id, ok := h.agentID(ctx)
	if !ok || !h.ready(ctx) {
		return
	}
	out := AgentVersionList{Agent: id, Versions: []AgentVersionEntry{}}
	var vs []schemas.AgentVersion
	if lf := h.builtin(ctx, id); lf != "" {
		_, v, err := agentversions.LibraryVersion(lf)
		if err != nil {
			sendDepError(ctx, h.mode, "read built-in agent", err)
			return
		}
		out.Builtin, vs = true, []schemas.AgentVersion{v}
	} else {
		var err error
		if vs, err = agentversions.List(h.projects.Root(), id); err != nil {
			sendDepError(ctx, h.mode, "list agent versions", err)
			return
		}
		if len(vs) == 0 {
			doc, rel, ok := h.projectAgentFile(ctx, id)
			if !ok {
				lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "agent "+id+" not found")
				return
			}
			// An agent saved before versioning: its versions come from the
			// Files API history of the file (agentFileHistory).
			if _, _, err := agentversions.Record(h.projects.Root(), id, agentFileHistory(context.Background(), h.projects, rel, doc, "", "")); err == nil {
				vs, _ = agentversions.List(h.projects.Root(), id)
			}
		}
	}
	counts := h.runsByHash()
	for _, v := range vs {
		out.Versions = append(out.Versions, AgentVersionEntry{AgentVersion: v, Runs: counts[strings.TrimPrefix(v.AgentHash, "sha256:")]})
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *agentVersionsHandler) get(ctx *fasthttp.RequestCtx) {
	id, ok := h.agentID(ctx)
	if !ok || !h.ready(ctx) {
		return
	}
	raw, _ := ctx.UserValue("n").(string)
	n, err := strconv.Atoi(strings.TrimPrefix(raw, "v"))
	if err != nil || n < 1 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("version %q: want a number from 1", raw))
		return
	}
	var doc []byte
	var v schemas.AgentVersion
	if lf := h.builtin(ctx, id); lf != "" {
		if n == 1 {
			doc, v, err = agentversions.LibraryVersion(lf)
		} else {
			err = agentversions.ErrNotFound
		}
	} else {
		doc, v, err = agentversions.Read(h.projects.Root(), id, n)
	}
	if errors.Is(err, agentversions.ErrNotFound) {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, fmt.Sprintf("agent %s has no version %d", id, n))
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "read agent version", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, AgentVersionDoc{Version: v.Version, Hash: v.Hash, YAML: string(doc)})
}

func (h *agentVersionsHandler) create(ctx *fasthttp.RequestCtx) {
	id, ok := h.agentID(ctx)
	if !ok || !h.ready(ctx) {
		return
	}
	var req AgentVersionCreate
	if len(ctx.PostBody()) > 0 {
		if err := abDecodeBody(ctx, &req); err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
			return
		}
	}
	doc, rel, ok := h.projectAgentFile(ctx, id)
	if !ok {
		if agentversions.LibraryFile(h.library, id) != "" {
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
				"agent "+id+" is built in and read-only: it has one version (v1); create your own agent from a template to version it")
			return
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "agent "+id+" not found")
		return
	}
	v, created, err := agentversions.Record(h.projects.Root(), id, agentFileHistory(context.Background(), h.projects, rel, doc, abActor(ctx), req.Note))
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "agents/"+id+".yaml: "+err.Error())
		return
	}
	status := fasthttp.StatusOK
	if created {
		status = fasthttp.StatusCreated
	}
	lib.SendJSON(ctx, h.mode, status, AgentVersionCreated{AgentVersion: v, Created: created})
}
