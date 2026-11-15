package handlers

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/importers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// ImportList is GET /imports: every importer the binary links, sorted by
// name, as `bifrost-bench import list` prints them. It says what each
// importer converts and what a run of the imported suite needs; it never
// reads upstream data.
type ImportList struct {
	Importers []importers.Info `json:"importers"`
}

// ImportsHandler serves the importer catalog for the UI's imports page.
type ImportsHandler struct {
	mode schemas.Mode
	list func() []importers.Info
}

// NewImportsHandler creates the imports handler over the importer registry.
// The importers register themselves when cmd/bifrost-bench links them, so a
// binary that links none serves an empty list.
func NewImportsHandler(mode schemas.Mode) *ImportsHandler {
	return &ImportsHandler{mode: mode, list: importers.All}
}

// RegisterRoutes registers GET /imports.
func (h *ImportsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/imports", lib.ChainMiddlewares(h.getImports, middlewares...))
}

// getImports handles GET /api/bench/imports. Nil slices inside each entry
// are sent as empty arrays, so the UI never has to tell null from none.
func (h *ImportsHandler) getImports(ctx *fasthttp.RequestCtx) {
	infos := h.list()
	out := ImportList{Importers: make([]importers.Info, 0, len(infos))}
	for _, in := range infos {
		in.Benchmarks = nonNil(in.Benchmarks)
		in.Needs = nonNil(in.Needs)
		out.Importers = append(out.Importers, in)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// scenarioSummary fills the scenario fields of a suite summary and of each of
// its tasks for the suites page: track, grading, output, declared output
// artifacts, services and import record of the suite; image, services,
// sessions, output, skills and the impossible flag of each task. It lives
// here, beside the other UI-only payload, and is called once from
// SuitesHandler.summarize. A repository suite gets only its track and
// grading; every other field stays empty and is omitted, so the payload of a
// plain suite is unchanged.
func scenarioSummary(s *schemas.Suite, out *schemas.SuiteSummary) {
	out.Track = s.Track
	if out.Track == "" {
		out.Track = schemas.TrackCoding
	}
	out.Grading = s.Grading
	if out.Grading == "" {
		out.Grading = schemas.GradingVerified
	}
	if s.Output != nil {
		out.Output = s.Output.Kind
		if len(s.Output.Artifacts) > 0 {
			out.Artifacts = slices.Clone(s.Output.Artifacts)
		}
	}
	for _, sv := range s.Services {
		out.Services = append(out.Services, sv.ID)
	}
	if s.Imported != nil {
		rec := *s.Imported
		out.Imported = &rec
	}
	for i := range out.Tasks {
		if t := s.TaskByID(out.Tasks[i].ID); t != nil {
			taskScenario(t, &out.Tasks[i])
		}
	}
}

// taskScenario copies one task's scenario facts into its list entry. Service
// specs are cut to what the page shows: environment values, seed files,
// commands and export commands are left out, as the suites API never serves
// an environment value.
func taskScenario(t *schemas.Task, out *schemas.SuiteTask) {
	if t.Environment != nil {
		env := *t.Environment
		out.Environment = &env
	}
	for _, sv := range t.Services {
		out.Services = append(out.Services, publicService(sv))
	}
	if len(t.Sessions) > 0 {
		out.Sessions = slices.Clone(t.Sessions)
	}
	if t.Output != nil {
		o := *t.Output
		out.Output = &o
	}
	if t.Skills != "" {
		out.Skills = skillNames(t)
	}
	out.Impossible = t.Impossible
}

// publicService is a service spec without its environment, seed files,
// command or export commands.
func publicService(sv schemas.ServiceSpec) schemas.ServiceSpec {
	out := schemas.ServiceSpec{
		ID: sv.ID, Role: sv.Role, Image: sv.Image, Port: sv.Port,
		Proxy: sv.Proxy, Hidden: sv.Hidden,
	}
	if sv.Build != nil {
		b := *sv.Build
		out.Build = &b
	}
	if sv.MCP != nil {
		m := *sv.MCP
		out.MCP = &m
	}
	for _, ex := range sv.Export {
		out.Export = append(out.Export, schemas.StateExport{ID: ex.ID, Path: ex.Path})
	}
	return out
}

// skillNames lists the skills in a task's skills folder: each sub-folder
// holding a SKILL.md, or the folder itself when it holds one. A folder that
// cannot be read, escapes the task folder or lies in a hidden one is
// reported by its relative path alone, so the page still says skills are
// declared and never names anything hidden.
func skillNames(t *schemas.Task) []string {
	rel := t.Skills
	root, err := visiblePath(t.Dir, rel, hiddenRoots(t))
	if err != nil {
		return []string{rel}
	}
	if _, err := os.Stat(filepath.Join(root, "SKILL.md")); err == nil {
		return []string{filepath.Base(root)}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return []string{rel}
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "SKILL.md")); err == nil {
			out = append(out, e.Name())
		}
	}
	if len(out) == 0 {
		return []string{rel}
	}
	return out
}
