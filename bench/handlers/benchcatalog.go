package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
)

// CatalogImportKind is the job kind that runs an importer for a catalog entry.
const CatalogImportKind = "catalog.import"

// BenchCatalogList is GET /benchmarks/catalog.
type BenchCatalogList struct {
	Entries    []BenchCatalogEntry `json:"entries"`
	SourceRoot bool                 `json:"source_available"` // the server found its bench folder
	Starter    []string             `json:"starter_agents"`
}

// BenchCatalogEntry is one catalog entry as the list shows it. Internal
// marks an entry that is Heimdall's own machinery, not a benchmark of
// agents (the improver suite): the catalog page leaves it out.
type BenchCatalogEntry struct {
	benchcatalog.Entry
	Internal bool `json:"internal,omitempty"`
}

// internalSuites are the suites the server runs for itself.
var internalSuites = map[string]bool{"heimdall-improver": true}

func catalogEntryView(e benchcatalog.Entry) BenchCatalogEntry {
	internal := internalSuites[e.SuiteID]
	for _, id := range e.Suites {
		internal = internal || internalSuites[id]
	}
	return BenchCatalogEntry{Entry: e, Internal: internal}
}

// BenchCatalogAdd is the body of POST /benchmarks/catalog/{id}/add. Copy
// entries need no body; importer entries need From.
type BenchCatalogAdd struct {
	From  string            `json:"from,omitempty"`
	URL   string            `json:"url,omitempty"` // public https URL fetched server side (instead of From)
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

// BenchCatalogDataset answers POST /benchmarks/catalog/{id}/dataset: the
// project path the uploaded data landed in, to pass as From.
type BenchCatalogDataset struct {
	Entry string `json:"entry"`
	From  string `json:"from"`
	Size  int    `json:"size"`
	Zip   bool   `json:"zip"`
}

type benchCatalogHandler struct {
	env    OrgEnv
	source func() benchcatalog.Source
}

func init() {
	RegisterOrgArea("benchcatalog", func(env OrgEnv, r *router.Router) {
		h := &benchCatalogHandler{env: env, source: func() benchcatalog.Source { return catalogSource(env) }}
		r.GET(APIPrefix+"/benchmarks/catalog", env.R(h.list))
		r.POST(APIPrefix+"/benchmarks/catalog/{id}/add", env.W(h.add))
		r.POST(APIPrefix+"/benchmarks/catalog/{id}/dataset", env.U(h.dataset))
	})
}

// catalogSource is the catalog an org sees. The Acme practice examples are
// developer content: local development and tests only, never a hosted org,
// and a hosted org never sees the practice suite (coding-core) either.
func catalogSource(env OrgEnv) benchcatalog.Source {
	return benchcatalog.Source{Root: benchcatalog.SourceRoot(), Examples: benchcatalog.DevContent(env.Services.Hosted), Hosted: env.Services.Hosted}
}

func (h *benchCatalogHandler) project() string {
	if h.env.Cfg == nil {
		return ""
	}
	return h.env.Cfg.Root
}

func (h *benchCatalogHandler) list(ctx *fasthttp.RequestCtx) {
	src := h.source()
	entries := []BenchCatalogEntry{}
	for _, e := range src.List(h.project()) {
		entries = append(entries, catalogEntryView(e))
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
	if target := strings.TrimSpace(body.Suite); isBuiltinSuite(h.env.Services.Suites, target) || (target == "" && isBuiltinSuite(h.env.Services.Suites, e.SuiteID)) {
		if target == "" {
			target = e.SuiteID
		}
		sendBuiltinReadOnly(ctx, mode, target) // built in already: run it, no org copy
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
	params := benchcatalog.ImportParams{Entry: id, From: body.From, URL: strings.TrimSpace(body.URL), Suite: body.Suite, Limit: body.Limit, IDs: body.IDs, Set: body.Set, Force: body.Force}
	if strings.TrimSpace(params.From) == "" && params.URL == "" {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"the import needs data: upload it (Upload dataset: "+e.UploadExpects+") or give a public https url to fetch")
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

// dataset handles POST /benchmarks/catalog/{id}/dataset: the multipart
// "file" part (or the raw body with ?filename=) is stored under
// uploads/datasets/<id>/ (a zip is extracted), up to lib.MaxUploadBytes.
func (h *benchCatalogHandler) dataset(ctx *fasthttp.RequestCtx) {
	mode := h.env.Mode
	_, p, ok := h.caller(ctx)
	if !ok {
		return
	}
	project := h.project()
	if project == "" {
		sendUnavailable(ctx, mode, "project folder")
		return
	}
	id, _ := ctx.UserValue("id").(string)
	e, found := h.source().Find(project, id)
	if !found {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "no catalog entry "+id+"; reload the catalog")
		return
	}
	if e.Kind != benchcatalog.KindImporter {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "entry "+id+" needs no dataset: use Add")
		return
	}
	store := h.env.Services.Projects
	if store == nil {
		lib.SendError(ctx, mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"project files are not available on this server: the project folder could not be opened")
		return
	}
	name, data, err := datasetBody(ctx)
	if err != nil {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if len(data) > lib.MaxUploadBytes {
		lib.SendError(ctx, mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge,
			fmt.Sprintf("dataset is larger than %d MiB: upload a smaller split, or use Fetch from URL for public data", lib.MaxUploadBytes>>20))
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	from, err := benchcatalog.SaveDataset(work, store, id, name, data, createdBy(p))
	if err != nil {
		status, typ := fasthttp.StatusBadRequest, lib.ErrTypeBadRequest
		switch {
		case errors.Is(err, projectfs.ErrTooLarge):
			status, typ = fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge
		case errors.Is(err, projectfs.ErrOutside), errors.Is(err, projectfs.ErrInvalid):
		default:
			if !strings.Contains(err.Error(), "zip") {
				status, typ = fasthttp.StatusInternalServerError, lib.ErrTypeInternal
			}
		}
		lib.SendError(ctx, mode, status, typ, "save dataset: "+err.Error())
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusOK, BenchCatalogDataset{Entry: id, From: from, Size: len(data), Zip: benchcatalog.IsZip(name, data)})
}

// datasetBody is the uploaded file: the multipart "file" part, else the raw
// body named by ?filename=.
func datasetBody(ctx *fasthttp.RequestCtx) (string, []byte, error) {
	mt, _, _ := mime.ParseMediaType(string(ctx.Request.Header.ContentType()))
	if mt == "multipart/form-data" {
		fh, err := ctx.FormFile("file")
		if err != nil {
			return "", nil, errors.New(`multipart upload needs the dataset in a form field named "file"`)
		}
		f, err := fh.Open()
		if err != nil {
			return "", nil, err
		}
		defer f.Close()
		data, err := io.ReadAll(io.LimitReader(f, lib.MaxUploadBytes+1))
		return fh.Filename, data, err
	}
	body := ctx.PostBody()
	if len(body) == 0 {
		return "", nil, errors.New("empty body: send the dataset file as a multipart field named \"file\"")
	}
	name := string(ctx.QueryArgs().Peek("filename"))
	if name == "" {
		name = "data"
	}
	return name, body, nil
}
