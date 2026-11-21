package handlers

// Suite authoring area (web parity L10): create a suite from a template, add
// tasks, read and write a suite's files (task.yaml, instruction, hidden tests,
// repo files) and upload a zip into a folder, all through the org's confined
// project store, and lint a suite synchronously. Lint and lock as jobs are the
// existing suite.lint and suite.lock kinds.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

// Suite authoring limits.
const (
	// suiteFilesLimit is the most entries one suite file listing returns.
	suiteFilesLimit = 5000
	// maxTaskTitle is the longest task title the create form accepts.
	maxTaskTitle = 120
)

// SuiteTemplate is one template a new suite can start from.
type SuiteTemplate struct {
	ID string `json:"id"`
	// Scenario templates write a suite with one complete sample task.
	Scenario bool   `json:"scenario"`
	Sample   string `json:"sample,omitempty"`
}

// SuiteTemplateList answers GET /suite-templates.
type SuiteTemplateList struct {
	Templates []SuiteTemplate `json:"templates"`
	Default   string          `json:"default"`
}

// CreateSuiteRequest is the body of POST /suites.
type CreateSuiteRequest struct {
	ID       string `json:"id"`
	Template string `json:"template"`
	// Image is an existing toolchain image; empty writes base/Dockerfile.
	Image string `json:"image,omitempty"`
}

// CreateSuiteResponse says what POST /suites wrote.
type CreateSuiteResponse struct {
	ID       string   `json:"id"`
	Template string   `json:"template"`
	Files    []string `json:"files"`
	// Sample is the task a scenario template wrote, if any.
	Sample string `json:"sample,omitempty"`
	// Visible is false when the server's suite loader reads another folder
	// than the project's benchmarks/ (a custom benchmarks_root).
	Visible bool   `json:"visible"`
	Next    string `json:"next"`
}

// CreateTaskRequest is the body of POST /suites/{id}/tasks.
type CreateTaskRequest struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
	// Kind is bugfix (default), feature, refactor or performance.
	Kind string `json:"kind,omitempty"`
	// Instruction is the task's instruction.md; empty writes a draft that
	// lint flags until it is rewritten.
	Instruction string `json:"instruction,omitempty"`
	License     string `json:"license,omitempty"`
}

// CreateTaskResponse says what POST /suites/{id}/tasks wrote.
type CreateTaskResponse struct {
	Suite    string   `json:"suite"`
	Task     string   `json:"task"`
	Template string   `json:"template"`
	Files    []string `json:"files"`
	Warnings []string `json:"warnings"`
}

// SuiteFileList answers GET /suites/{id}/files: paths relative to the suite folder.
type SuiteFileList struct {
	Suite     string            `json:"suite"`
	Files     []projectfs.Entry `json:"files"`
	Truncated bool              `json:"truncated"`
}

// SuiteFile answers GET /suites/{id}/files/{path}. Content is set for UTF-8
// text only; a binary file has Binary set and no content.
type SuiteFile struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	Binary  bool   `json:"binary"`
	Content string `json:"content,omitempty"`
}

// SuiteUploadResult answers POST /suites/{id}/upload.
type SuiteUploadResult struct {
	Suite string            `json:"suite"`
	Dir   string            `json:"dir"`
	Files []projectfs.Entry `json:"files"`
}

// SuiteLintReport answers GET /suites/{id}/lint.
type SuiteLintReport struct {
	Suite    string               `json:"suite"`
	Version  string               `json:"version"`
	Tasks    int                  `json:"tasks"`
	Probes   int                  `json:"probes"`
	Problems []suite.Problem      `json:"problems"`
	Errors   int                  `json:"errors"`
	Warnings int                  `json:"warnings"`
	LoadErr  string               `json:"load_error,omitempty"`
	Drift    []schemas.SuiteDrift `json:"lock_drift,omitempty"`
}

// suiteAuthoring serves the area. Every field may be nil.
type suiteAuthoring struct {
	mode     schemas.Mode
	projects projectfs.Store
	suites   SuiteSource
	benchDir string // the benchmarks folder of the server config; "" when unknown
}

func init() {
	RegisterOrgArea("suiteauthoring", func(env OrgEnv, r *router.Router) {
		h := &suiteAuthoring{mode: env.Mode, projects: env.Services.Projects, suites: env.Services.Suites}
		if env.Cfg != nil {
			h.benchDir = env.Cfg.BenchmarksRoot
			if h.benchDir != "" && !filepath.IsAbs(h.benchDir) && env.Cfg.Root != "" {
				h.benchDir = filepath.Join(env.Cfg.Root, h.benchDir)
			}
		}
		r.GET(APIPrefix+"/suite-templates", env.R(h.templates))
		r.POST(APIPrefix+"/suites", env.W(h.createSuite))
		r.POST(APIPrefix+"/suites/{id}/tasks", env.W(refuseBuiltin(env.Mode, h.suites, h.createTask)))
		r.GET(APIPrefix+"/suites/{id}/lint", env.R(h.lint))
		r.GET(APIPrefix+"/suites/{id}/files", env.R(h.listFiles))
		r.POST(APIPrefix+"/suites/{id}/upload", env.U(refuseBuiltin(env.Mode, h.suites, h.upload)))
		r.GET(APIPrefix+"/suites/{id}/files/{path:*}", env.R(h.readFile))
		r.PUT(APIPrefix+"/suites/{id}/files/{path:*}", env.W(refuseBuiltin(env.Mode, h.suites, h.writeFile)))
		r.DELETE(APIPrefix+"/suites/{id}/files/{path:*}", env.W(refuseBuiltin(env.Mode, h.suites, h.deleteFile)))
	})
}

func (h *suiteAuthoring) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

func (h *suiteAuthoring) needProjects(ctx *fasthttp.RequestCtx) bool {
	if h.projects == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"the project file store is not available on this server; start it with a project folder (bifrost-bench serve -config <project>/deploy/bench.yaml)")
		return false
	}
	return true
}

func actorName(ctx *fasthttp.RequestCtx) string {
	p := auth.PrincipalFrom(ctx)
	if p == nil {
		return ""
	}
	if p.Email != "" {
		return p.Email
	}
	return p.ActorID()
}

// refuseViewer answers 403 when the caller's role is below member: suite
// files include hidden tests and reference solutions. A principal with no
// role (local mode without auth) is allowed, as every local read is.
func (h *suiteAuthoring) refuseViewer(ctx *fasthttp.RequestCtx) bool {
	p := auth.PrincipalFrom(ctx)
	if p != nil && p.Role != "" && !p.Role.AtLeast(auth.RoleMember) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"suite files include hidden tests, so only members can read them; ask an admin of your organization for the member role")
		return true
	}
	return false
}

// sendStoreError maps a projectfs error to a status with a message saying what to do.
func (h *suiteAuthoring) sendStoreError(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case errors.Is(err, projectfs.ErrOutside):
		h.bad(ctx, what+": the path must stay inside the suite folder (no absolute paths, '..', .git or .heimdall)")
	case errors.Is(err, projectfs.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": not found")
	case errors.Is(err, projectfs.ErrTooLarge):
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeBadRequest,
			fmt.Sprintf("%s: over the %d KiB file limit; upload large files in a zip", what, projectfs.MaxFileBytes>>10))
	case errors.Is(err, projectfs.ErrConflict):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": the file changed since you opened it; reload it and apply your edit again")
	case errors.Is(err, projectfs.ErrInvalid):
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, projectfs.ErrNotImplemented):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotImplemented, lib.ErrTypeUnavailable, what+": not supported by this server yet; upload the files one by one")
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, what+": "+err.Error())
	}
}

// templates handles GET /suite-templates.
func (h *suiteAuthoring) templates(ctx *fasthttp.RequestCtx) {
	out := SuiteTemplateList{Default: scaffold.TemplatePythonPytest}
	for _, id := range scaffold.SuiteTemplates() {
		out.Templates = append(out.Templates, SuiteTemplate{ID: id, Scenario: scaffold.IsScenarioTemplate(id), Sample: scaffold.ScenarioSample(id)})
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// createSuite handles POST /suites: benchmarks/<id>/ from a template.
func (h *suiteAuthoring) createSuite(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	var req CreateSuiteRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"id","template","image"?}: `+err.Error())
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if isBuiltinSuite(h.suites, req.ID) {
		sendBuiltinReadOnly(ctx, h.mode, req.ID)
		return
	}
	if req.Template == "" {
		req.Template = scaffold.TemplatePythonPytest
	}
	if !scaffold.ValidID(req.ID) || len(req.ID) > 64 {
		h.bad(ctx, "suite id: use 1 to 64 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
		return
	}
	if strings.ContainsAny(req.Image, " \t\n\"'`$") {
		h.bad(ctx, "image: one image reference, for example python:3.12-slim")
		return
	}
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: req.ID, Template: req.Template, Image: req.Image})
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
	// A fresh project may have no benchmarks/ yet; the store resolves paths
	// against existing folders, so create the area first.
	if err := os.MkdirAll(filepath.Join(h.projects.Root(), string(projectfs.AreaBenchmarks)), 0o755); err != nil {
		h.sendStoreError(ctx, "create benchmarks folder", err)
		return
	}
	dir, err := h.projects.Resolve(projectfs.AreaBenchmarks, req.ID)
	if err != nil {
		h.sendStoreError(ctx, "suite "+req.ID, err)
		return
	}
	if _, err := os.Lstat(dir); err == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "suite "+req.ID+" already exists; pick another id or open it from Suites")
		return
	}
	if err := writeTreeAtomic(dir, files); err != nil {
		h.sendStoreError(ctx, "write suite "+req.ID, err)
		return
	}
	out := CreateSuiteResponse{ID: req.ID, Template: req.Template, Sample: scaffold.ScenarioSample(req.Template), Visible: h.visible(dir)}
	for _, f := range files {
		out.Files = append(out.Files, f.Path)
	}
	if out.Sample != "" {
		out.Next = "Open the sample task " + out.Sample + ", adapt it, then run Lint and Lock."
	} else {
		out.Next = "Add a task, upload its repo and hidden tests, then run Lint and Lock."
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, &out)
}

// visible reports whether dir is in the folder the suite loader reads.
func (h *suiteAuthoring) visible(dir string) bool {
	if h.benchDir == "" {
		return true
	}
	a, err1 := filepath.EvalSymlinks(filepath.Dir(dir))
	b, err2 := filepath.EvalSymlinks(h.benchDir)
	if err1 != nil || err2 != nil {
		return filepath.Clean(filepath.Dir(dir)) == filepath.Clean(h.benchDir)
	}
	return a == b
}

// writeTreeAtomic writes files into a hidden sibling of dir, then renames it
// into place, so a failure leaves nothing behind.
func writeTreeAtomic(dir string, files []scaffold.File) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(filepath.Dir(dir), ".new-"+filepath.Base(dir)+"-*")
	if err != nil {
		return err
	}
	if err := scaffold.WriteFiles(tmp, files, false); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	return nil
}

// suiteDirParam validates {id} and resolves the suite folder, answering on failure.
func (h *suiteAuthoring) suiteDirParam(ctx *fasthttp.RequestCtx) (string, string, bool) {
	id, ok := idParam(ctx, h.mode, "id", "suite")
	if !ok {
		return "", "", false
	}
	dir, err := h.projects.Resolve(projectfs.AreaBenchmarks, id)
	if err != nil {
		h.sendStoreError(ctx, "suite "+id, err)
		return "", "", false
	}
	if fi, err := os.Stat(filepath.Join(dir, suite.SuiteFile)); err != nil || !fi.Mode().IsRegular() {
		sendNotFound(ctx, h.mode, "suite", id)
		return "", "", false
	}
	return id, dir, true
}

// draftInstruction is the instruction a task gets when the form leaves it empty.
const draftInstruction = suite.EditMeMarker + `
<!--
Rewrite this file as the request a teammate would send: what is wrong or
missing, and how to tell when it is done. Say nothing about how the work is
checked. Delete this comment and the EDIT ME line when you are done.
-->

# Describe the task
`

const repoReadme = "Put the code the agent starts from in this folder (upload a zip of the repository).\n"

// createTask handles POST /suites/{id}/tasks: tasks/<task>/ in the shape of
// the suite's template, added to suite.yaml.
func (h *suiteAuthoring) createTask(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	id, dir, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	var req CreateTaskRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"id","title"?,"kind"?,"instruction"?,"license"?}: `+err.Error())
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	if !scaffold.ValidID(req.ID) || len(req.ID) > 64 {
		h.bad(ctx, "task id: use 1 to 64 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit")
		return
	}
	if utf8.RuneCountInString(req.Title) > maxTaskTitle || strings.ContainsAny(req.Title, "\n\r") {
		h.bad(ctx, fmt.Sprintf("title: one line of at most %d characters", maxTaskTitle))
		return
	}
	if len(req.Instruction) > projectfs.MaxFileBytes {
		h.sendStoreError(ctx, "instruction", projectfs.ErrTooLarge)
		return
	}
	suitePath := filepath.Join(dir, suite.SuiteFile)
	suiteYAML, err := os.ReadFile(suitePath)
	if err != nil {
		h.sendStoreError(ctx, "read suite.yaml", err)
		return
	}
	taskDir := filepath.Join(dir, "tasks", req.ID)
	if _, err := os.Lstat(taskDir); err == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "task "+req.ID+" already exists in "+id+"; pick another id")
		return
	}
	tmpl := scaffold.DetectTemplate(suiteYAML)
	out := CreateTaskResponse{Suite: id, Task: req.ID, Template: tmpl, Warnings: []string{}}
	if scaffold.IsScenarioTemplate(tmpl) {
		res, err := scaffold.InitScenarioTask(scaffold.ScenarioTaskOptions{SuiteDir: dir, ID: req.ID, License: req.License})
		if err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "create task: "+err.Error())
			return
		}
		out.Warnings = append(out.Warnings, res.Warnings...)
		if req.Instruction != "" {
			if err := os.WriteFile(filepath.Join(taskDir, "instruction.md"), []byte(req.Instruction), 0o644); err != nil {
				out.Warnings = append(out.Warnings, "instruction not written: "+err.Error())
			}
		}
		out.Files = listRel(taskDir)
		lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, &out)
		return
	}
	controls, err := scaffold.TaskControls(tmpl)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			"create task: cannot tell which template suite.yaml follows; add a '# template: python-pytest' line (or node-test, go-test, generic-reward) to suite.yaml")
		return
	}
	meta, err := scaffold.TaskYAML(scaffold.TaskMeta{ID: req.ID, Title: req.Title, Kind: req.Kind, License: req.License, Source: "web editor"})
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
	meta = bytes.Replace(meta, []byte("# Written by `bifrost-bench task init` from web editor (..)."), []byte("# Written by the Heimdall web editor."), 1)
	meta = bytes.Replace(meta, []byte("created_by: task init"), []byte("created_by: web editor"), 1)
	// No commits back a web task: drop the empty commit fields the schema refuses.
	meta = dropEmptyKeys(meta, "base_commit", "reference_commit")
	instr := req.Instruction
	if strings.TrimSpace(instr) == "" {
		instr = draftInstruction
	}
	files := append([]scaffold.File{
		{Path: "task.yaml", Data: meta, Mode: 0o644},
		{Path: "instruction.md", Data: []byte(instr), Mode: 0o644},
		{Path: "repo/README.md", Data: []byte(repoReadme), Mode: 0o644},
		{Path: "solution/reference.diff", Mode: 0o644},
	}, controls...)
	if err := writeTreeAtomic(taskDir, files); err != nil {
		h.sendStoreError(ctx, "write task "+req.ID, err)
		return
	}
	edited, changed, err := scaffold.AddTaskID(suiteYAML, req.ID)
	if err == nil && changed {
		_, err = h.projects.Write(ctx, projectfs.AreaBenchmarks, id+"/"+suite.SuiteFile, edited,
			projectfs.WriteOpts{Actor: actorName(ctx), Note: "add task " + req.ID, IfMatch: shaOf(suiteYAML)})
		if err == nil {
			_ = os.Chmod(suitePath, 0o644)
		}
	}
	if err != nil {
		os.RemoveAll(taskDir)
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "add the task to suite.yaml: "+err.Error())
		return
	}
	for _, f := range files {
		out.Files = append(out.Files, f.Path)
	}
	out.Warnings = append(out.Warnings, "Upload the repository into repo/ and the hidden tests into verifier/tests/, then run Lint.")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, &out)
}

func listRel(dir string) []string {
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out
}

// lint handles GET /suites/{id}/lint: suite.Lint over the loaded suite.
// A load failure is a report with load_error, not an HTTP error, so the
// editor can show it next to the file that broke it.
func (h *suiteAuthoring) lint(ctx *fasthttp.RequestCtx) {
	if h.suites == nil {
		sendUnavailable(ctx, h.mode, "suite loader")
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
	out := SuiteLintReport{Suite: id, Problems: []suite.Problem{}}
	s, err := h.suites.LoadSuite(work, id)
	if err != nil {
		out.LoadErr = err.Error()
		out.Errors = 1
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
		return
	}
	out.Version, out.Tasks, out.Probes = s.Version, len(s.Tasks), len(s.Probes)
	for _, p := range suite.Lint(s) {
		out.Problems = append(out.Problems, p)
		if p.Severity == suite.SeverityError {
			out.Errors++
		} else {
			out.Warnings++
		}
	}
	if s.Lock != nil {
		if d, err := h.suites.LockDrift(work, s); err == nil {
			out.Drift = d
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// listFiles handles GET /suites/{id}/files?dir=.
func (h *suiteAuthoring) listFiles(ctx *fasthttp.RequestCtx) {
	if h.refuseViewer(ctx) || !h.needProjects(ctx) {
		return
	}
	id, _, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	sub := strings.Trim(string(ctx.QueryArgs().Peek("dir")), "/")
	rel := id
	if sub != "" {
		rel = id + "/" + sub
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ents, err := h.projects.List(work, projectfs.AreaBenchmarks, rel, true)
	if err != nil {
		h.sendStoreError(ctx, "list "+rel, err)
		return
	}
	out := SuiteFileList{Suite: id, Files: make([]projectfs.Entry, 0, len(ents))}
	for _, e := range ents {
		e.Path = strings.TrimPrefix(e.Path, id+"/")
		if strings.HasPrefix(path.Base(e.Path), ".new-") || strings.HasPrefix(path.Base(e.Path), ".pfs-") {
			continue
		}
		if len(out.Files) >= suiteFilesLimit {
			out.Truncated = true
			break
		}
		out.Files = append(out.Files, e)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// filePath returns the suite id and the area-relative path of {path}.
func (h *suiteAuthoring) filePath(ctx *fasthttp.RequestCtx) (string, string, string, bool) {
	if !h.needProjects(ctx) {
		return "", "", "", false
	}
	id, _, ok := h.suiteDirParam(ctx)
	if !ok {
		return "", "", "", false
	}
	p := strings.Trim(pathParam(ctx, "path"), "/")
	if p == "" {
		h.bad(ctx, "file path missing: /suites/{id}/files/<path inside the suite>")
		return "", "", "", false
	}
	return id, p, id + "/" + p, true
}

// readFile handles GET /suites/{id}/files/{path}.
func (h *suiteAuthoring) readFile(ctx *fasthttp.RequestCtx) {
	if h.refuseViewer(ctx) {
		return
	}
	_, p, rel, ok := h.filePath(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	data, ent, err := h.projects.Read(work, projectfs.AreaBenchmarks, rel)
	if err != nil {
		h.sendStoreError(ctx, p, err)
		return
	}
	out := SuiteFile{Path: p, Size: ent.Size, SHA256: ent.SHA256}
	if utf8.Valid(data) && !bytes.ContainsRune(data, 0) {
		out.Content = string(data)
	} else {
		out.Binary = true
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// writeFile handles PUT /suites/{id}/files/{path}: the raw body is the file.
// If-Match carries the sha256 the editor read, so a concurrent edit is a 409.
func (h *suiteAuthoring) writeFile(ctx *fasthttp.RequestCtx) {
	id, p, rel, ok := h.filePath(ctx)
	if !ok {
		return
	}
	if p == suite.LockFile {
		h.bad(ctx, "suite.lock is written by the Lock button (suite.lock job), not by hand")
		return
	}
	body := ctx.PostBody()
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ent, err := h.projects.Write(work, projectfs.AreaBenchmarks, rel, body, projectfs.WriteOpts{
		Actor: actorName(ctx), Note: "web editor", IfMatch: strings.Trim(string(ctx.Request.Header.Peek("If-Match")), `"`),
	})
	if err != nil {
		h.sendStoreError(ctx, p, err)
		return
	}
	fixMode(h.projects, rel)
	ent.Path = strings.TrimPrefix(ent.Path, id+"/")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &ent)
}

// fixMode gives a written file the mode a task folder needs: scripts are
// executable, everything else readable by the grader's user.
func fixMode(ps projectfs.Store, rel string) {
	full, err := ps.Resolve(projectfs.AreaBenchmarks, rel)
	if err != nil {
		return
	}
	mode := fs.FileMode(0o644)
	if strings.HasSuffix(rel, ".sh") {
		mode = 0o755
	}
	_ = os.Chmod(full, mode)
}

// deleteFile handles DELETE /suites/{id}/files/{path}. suite.yaml itself
// cannot be deleted here.
func (h *suiteAuthoring) deleteFile(ctx *fasthttp.RequestCtx) {
	_, p, rel, ok := h.filePath(ctx)
	if !ok {
		return
	}
	if p == suite.SuiteFile {
		h.bad(ctx, "suite.yaml cannot be deleted from the editor")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := h.projects.Delete(work, projectfs.AreaBenchmarks, rel, projectfs.WriteOpts{
		Actor: actorName(ctx), Note: "web editor", IfMatch: strings.Trim(string(ctx.Request.Header.Peek("If-Match")), `"`),
	}); err != nil {
		h.sendStoreError(ctx, p, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

// upload handles POST /suites/{id}/upload?dir=: the raw body is a zip,
// extracted into dir of the suite (zip-slip safe, via the project store).
func (h *suiteAuthoring) upload(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	id, _, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	sub := strings.Trim(string(ctx.QueryArgs().Peek("dir")), "/")
	if sub == "" {
		h.bad(ctx, "dir is required: the folder inside the suite to extract into, for example tasks/t01/repo")
		return
	}
	body := ctx.PostBody()
	if len(body) == 0 {
		h.bad(ctx, "empty body: send the zip file as the request body")
		return
	}
	if len(body) > projectfs.MaxZipBytes {
		h.sendStoreError(ctx, "zip", projectfs.ErrTooLarge)
		return
	}
	if !bytes.HasPrefix(body, []byte("PK")) {
		h.bad(ctx, "not a zip file: compress the folder as .zip and upload that")
		return
	}
	work, cancel := context.WithTimeout(context.Background(), analyzeTimeout)
	defer cancel()
	ents, err := h.projects.ExtractZip(work, projectfs.AreaBenchmarks, id+"/"+sub, bytes.NewReader(body), int64(len(body)),
		projectfs.WriteOpts{Actor: actorName(ctx), Note: "web upload"})
	if err != nil {
		h.sendStoreError(ctx, "extract zip", err)
		return
	}
	out := SuiteUploadResult{Suite: id, Dir: sub, Files: make([]projectfs.Entry, 0, len(ents))}
	for _, e := range ents {
		e.Path = strings.TrimPrefix(e.Path, id+"/")
		out.Files = append(out.Files, e)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// decodeAuthoring decodes one JSON object, refusing unknown fields and trailing data.
func decodeAuthoring(body []byte, v any) error {
	if len(bytes.TrimSpace(body)) == 0 {
		return errors.New("empty body")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("trailing data after the JSON object")
	}
	return nil
}

func shaOf(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// dropEmptyKeys removes the "key:" lines of keys that have no value.
func dropEmptyKeys(src []byte, keys ...string) []byte {
	lines := strings.Split(string(src), "\n")
	out := lines[:0]
	for _, l := range lines {
		t := strings.TrimSpace(l)
		drop := false
		for _, k := range keys {
			if t == k+":" || t == k+": \"\"" {
				drop = true
			}
		}
		if !drop {
			out = append(out, l)
		}
	}
	return []byte(strings.Join(out, "\n"))
}
