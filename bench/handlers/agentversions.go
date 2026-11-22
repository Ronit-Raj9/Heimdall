package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
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
	// CreatedByName is how the author is shown (displayActor): the member's
	// display name, else the part of the email before "@". For a caller who
	// is not an org admin, an email in created_by is left out of the answer
	// (the stored version keeps it).
	CreatedByName string `json:"created_by_name,omitempty"`
}

// AgentVersionList is GET /agents/{id}/versions.
type AgentVersionList struct {
	Agent    string              `json:"agent"`
	Builtin  bool                `json:"builtin,omitempty"`
	Versions []AgentVersionEntry `json:"versions"`
	// Live is the agent's live version (agentLiveVersion): the last
	// released version, else the newest version holding the saved config.
	// A pull request is compared with it (0 when none). LiveSource says
	// which rule gave it (release or saved_config).
	Live       int    `json:"live,omitempty"`
	LiveSource string `json:"live_source,omitempty"`
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
	// YAML, when set, registers this agent config as a version without
	// changing the agent's saved config (a pull request's candidate, sent by
	// `bifrost-bench ci evaluate`). A config equal to any existing version
	// reuses that version.
	YAML string `json:"yaml,omitempty"`
}

// agentVersionYAMLMax caps a registered config.
const agentVersionYAMLMax = 256 << 10

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
	releases ReleaseLog // the live version rule reads the release log first
}

func init() {
	RegisterOrgArea("agentversions", func(env OrgEnv, r *router.Router) {
		h := &agentVersionsHandler{mode: env.Mode, projects: env.Services.Projects, index: env.Services.Index, library: suite.LibraryDir(), releases: env.Services.Releases}
		r.GET(APIPrefix+"/agents/{id}/versions", env.R(h.list))
		r.GET(APIPrefix+"/agents/{id}/versions/{n}", env.R(h.get))
		r.POST(APIPrefix+"/agents/{id}/versions", env.W(h.create))
	})
}

// snapshotAgentSave records a just-saved agents/<rel> as an agent version,
// linked to the Files API version that save made (no second history: the
// version entry names that file version). It is best effort: the save
// already happened, and a document that does not decode as an agent is not
// versioned. It returns the version current after the save and whether the
// save made it; a failure is logged and returned (0 when no version is
// known), never fatal to the save.
func snapshotAgentSave(ctx context.Context, store projectfs.Store, rel, id string, doc []byte, actor, note string) (int, bool, error) {
	if store == nil || !agentversions.ValidID(id) {
		return 0, false, nil
	}
	v, created, err := agentversions.Record(store.Root(), id, agentFileHistory(ctx, store, rel, doc, actor, note))
	if err != nil {
		log.Printf("agent %s: saved, but recording the agent version failed: %v", id, err)
		return v.Version, created, err
	}
	return v.Version, created, nil
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
	var names map[string]string
	if len(vs) > 0 {
		names = orgMemberNames(ctx)
	}
	for _, v := range vs {
		e := AgentVersionEntry{AgentVersion: v, Runs: counts[strings.TrimPrefix(v.AgentHash, "sha256:")]}
		name, keepRaw := displayActor(ctx, v.CreatedBy, names)
		if name != v.CreatedBy {
			e.CreatedByName = name
		}
		if !keepRaw && strings.Contains(v.CreatedBy, "@") {
			e.CreatedBy = ""
		}
		out.Versions = append(out.Versions, e)
	}
	live := agentLiveVersion(ctx, h.releases, h.projects, h.library, id)
	if live.Version == 0 && out.Builtin { // a built-in agent with no saved config of its own
		live.Version, live.Source = 1, LiveFromSavedConfig
	}
	out.Live, out.LiveSource = live.Version, live.Source
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
	var v schemas.AgentVersion
	var created bool
	var err error
	if req.YAML != "" {
		v, created, err = h.register(id, rel, doc, req, abActor(ctx))
	} else {
		v, created, err = agentversions.Record(h.projects.Root(), id, agentFileHistory(context.Background(), h.projects, rel, doc, abActor(ctx), req.Note))
	}
	if err != nil {
		what := "agents/" + id + ".yaml"
		if req.YAML != "" {
			what = "the registered config"
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
		return
	}
	status := fasthttp.StatusOK
	if created {
		status = fasthttp.StatusCreated
	}
	lib.SendJSON(ctx, h.mode, status, AgentVersionCreated{AgentVersion: v, Created: created})
}

// register adds req.YAML as a version of agent id, leaving agents/<id>.yaml
// as it is. An agent with no versions yet first gets its saved config's
// history, so the live version exists before the candidate.
func (h *agentVersionsHandler) register(id, rel string, current []byte, req AgentVersionCreate, actor string) (schemas.AgentVersion, bool, error) {
	if len(req.YAML) > agentVersionYAMLMax {
		return schemas.AgentVersion{}, false, fmt.Errorf("config is larger than %d KiB", agentVersionYAMLMax>>10)
	}
	root := h.projects.Root()
	if vs, err := agentversions.List(root, id); err == nil && len(vs) == 0 {
		if _, _, err := agentversions.Record(root, id, agentFileHistory(context.Background(), h.projects, rel, current, "", "")); err != nil {
			return schemas.AgentVersion{}, false, fmt.Errorf("saved config: %w", err)
		}
	}
	return agentversions.Register(root, id, []byte(req.YAML), actor, req.Note)
}
