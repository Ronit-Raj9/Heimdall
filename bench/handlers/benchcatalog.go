package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// CatalogImportKind is the job kind that runs an importer for a catalog entry.
const CatalogImportKind = "catalog.import"

// BenchCatalogList is GET /benchmarks/catalog.
type BenchCatalogList struct {
	Entries    []benchcatalog.Entry `json:"entries"`
	SourceRoot bool                 `json:"source_available"` // the server found its bench folder
	Starter    []string             `json:"starter_agents"`
}

// BenchCatalogAdd is the body of POST /benchmarks/catalog/{id}/add. Copy
// entries need no body; importer entries need From.
type BenchCatalogAdd struct {
	From  string            `json:"from,omitempty"`
	Suite string            `json:"suite,omitempty"`
	Limit int               `json:"limit,omitempty"`
	IDs   []string          `json:"ids,omitempty"`
	Set   map[string]string `json:"set,omitempty"`
	Force bool              `json:"force,omitempty"`
}

// BenchCatalogAdded answers POST /benchmarks/catalog/{id}/add: Result for a
// copy (200), Job for an import (202).
type BenchCatalogAdded struct {
	Entry  string                  `json:"entry"`
	Kind   string                  `json:"kind"`
	Result *benchcatalog.AddResult `json:"result,omitempty"`
	Job    *jobs.Job               `json:"job,omitempty"`
}

type benchCatalogHandler struct {
	env    OrgEnv
	source func() benchcatalog.Source
}

func init() {
	RegisterOrgArea("benchcatalog", func(env OrgEnv, r *router.Router) {
		h := &benchCatalogHandler{env: env, source: func() benchcatalog.Source {
			return benchcatalog.Source{Root: benchcatalog.SourceRoot()}
		}}
		r.GET(APIPrefix+"/benchmarks/catalog", env.R(h.list))
		r.POST(APIPrefix+"/benchmarks/catalog/{id}/add", env.W(h.add))
	})
}

func (h *benchCatalogHandler) project() string {
	if h.env.Cfg == nil {
		return ""
	}
	return h.env.Cfg.Root
}

func (h *benchCatalogHandler) list(ctx *fasthttp.RequestCtx) {
	src := h.source()
	entries := src.List(h.project())
	if entries == nil {
		entries = []benchcatalog.Entry{}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, BenchCatalogList{Entries: entries, SourceRoot: src.Root != "", Starter: benchcatalog.StarterAgents})
}

// caller is the org and principal of the request; local mode has no auth
// middleware and is the single local org.
func (h *benchCatalogHandler) caller(ctx *fasthttp.RequestCtx) (string, *auth.Principal, bool) {
	p, ok := auth.Lookup(ctx)
	if !ok {
		org := h.env.Services.OrgID
		if org == "" {
			org = auth.LocalOrgID
		}
		return org, auth.Local(), true
	}
	if !p.IsAuthenticated() || p.OrgID == "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in to add benchmarks")
		return "", nil, false
	}
	if !p.Can(auth.RoleMember) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "adding a benchmark needs the member role; ask an org admin")
		return "", nil, false
	}
	return p.OrgID, p, true
}

func (h *benchCatalogHandler) add(ctx *fasthttp.RequestCtx) {
	mode := h.env.Mode
	org, p, ok := h.caller(ctx)
	if !ok {
		return
	}
	project := h.project()
	if project == "" {
		sendUnavailable(ctx, mode, "project folder")
		return
	}
	id, _ := ctx.UserValue("id").(string)
	src := h.source()
	e, found := src.Find(project, id)
	if !found {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no catalog entry "+id+"; reload the catalog")
		return
	}
	var body BenchCatalogAdd
	if b := bytes.TrimSpace(ctx.PostBody()); len(b) > 0 {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil {
			lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "bad body: "+err.Error())
			return
		}
	}
	if !e.Available {
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "entry "+id+" cannot be added on this server: "+e.Hint)
		return
	}
	if e.Kind != benchcatalog.KindImporter {
		res, err := src.Add(project, e)
		if err != nil {
			lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "add "+id+": "+err.Error())
			return
		}
		lib.SendJSON(ctx, mode, fasthttp.StatusOK, BenchCatalogAdded{Entry: id, Kind: e.Kind, Result: res})
		return
	}
	if h.env.Services.Jobs == nil {
		sendUnavailable(ctx, mode, "job runner")
		return
	}
	params := benchcatalog.ImportParams{Entry: id, From: body.From, Suite: body.Suite, Limit: body.Limit, IDs: body.IDs, Set: body.Set, Force: body.Force}
	if strings.TrimSpace(params.From) == "" {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"from is required: fetch the data on the server first ("+e.Source+") and give its path")
		return
	}
	raw, _ := json.Marshal(params)
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := h.env.Services.Jobs.Create(work, jobs.NewJob{OrgID: org, Kind: CatalogImportKind, Params: raw, CreatedBy: createdBy(p)})
	if err != nil {
		status, typ := fasthttp.StatusInternalServerError, lib.ErrTypeInternal
		if errors.Is(err, jobs.ErrInvalidParams) {
			status, typ = fasthttp.StatusBadRequest, lib.ErrTypeBadRequest
		}
		lib.SendError(ctx, mode, status, typ, "start import: "+err.Error())
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusAccepted, BenchCatalogAdded{Entry: id, Kind: e.Kind, Job: j})
}
