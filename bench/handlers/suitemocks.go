package handlers

// Stateful mocks from the website: an OpenAPI file becomes a service folder
// (services/<id>/) of the benchmark that serves the same routes over HTTP
// and as MCP tools, over seeded state it exports for checks.yaml. Tasks add
// the returned services: entry and a seed to use it; the readiness check
// builds its image from the folder.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/valyala/fasthttp"
)

// SuiteMockRequest is the body of POST /suites/{id}/mocks.
type SuiteMockRequest struct {
	// ID is the service id: services/<id>/ in the benchmark.
	ID string `json:"id"`
	// Spec is the OpenAPI 3 file (YAML or JSON) as text.
	Spec string `json:"spec"`
	// FileName names the uploaded file; a .json name keeps it as JSON.
	FileName string `json:"file_name,omitempty"`
	// Replace overwrites an existing services/<id>/ (its recorded answers too).
	Replace bool `json:"replace,omitempty"`
	DryRun  bool `json:"dry_run,omitempty"`
}

// SuiteMockResult answers POST /suites/{id}/mocks.
type SuiteMockResult struct {
	Suite       string                   `json:"suite"`
	ID          string                   `json:"id"`
	Folder      string                   `json:"folder"`
	DryRun      bool                     `json:"dry_run"`
	Replaced    bool                     `json:"replaced"`
	Operations  []scaffold.MockOperation `json:"operations"`
	Collections []string                 `json:"collections"`
	Warnings    []string                 `json:"warnings"`
	ServiceYAML string                   `json:"service_yaml"`
	Seed        string                   `json:"seed"`
	Next        string                   `json:"next"`
}

func init() {
	RegisterOrgArea("suitemocks", func(env OrgEnv, r *router.Router) {
		h := &suiteBuild{&suiteAuthoring{mode: env.Mode, projects: env.Services.Projects, suites: env.Services.Suites}}
		r.POST(APIPrefix+"/suites/{id}/mocks", env.U(refuseBuiltin(env.Mode, h.suites, h.writeSuiteMock)))
	})
}

// writeSuiteMock handles POST /suites/{id}/mocks.
func (h *suiteBuild) writeSuiteMock(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	id, dir, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	var req SuiteMockRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"id","spec","file_name"?,"replace"?,"dry_run"?}: `+err.Error())
		return
	}
	if len(req.Spec) > scaffold.MaxMockSpecBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeBadRequest, "the OpenAPI file is over 4 MiB")
		return
	}
	if strings.TrimSpace(req.Spec) == "" {
		h.bad(ctx, "spec: paste or upload an OpenAPI 3 file")
		return
	}
	if len(req.FileName) > 200 || strings.ContainsAny(req.FileName, "/\\\n\r") {
		h.bad(ctx, "file_name: a plain file name of at most 200 characters")
		return
	}
	files, res, err := scaffold.MockFiles(id, req.ID, []byte(req.Spec), req.FileName)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	svcDir := filepath.Join(dir, "services", req.ID)
	_, statErr := os.Lstat(svcDir)
	exists := statErr == nil
	if exists && !req.Replace {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeBadRequest,
			"services/"+req.ID+" already exists in benchmark "+id+"; pick another id or replace it (replace: true), which drops its recorded answers")
		return
	}
	out := SuiteMockResult{Suite: id, ID: req.ID, Folder: "services/" + req.ID, DryRun: req.DryRun, Replaced: exists,
		Operations: res.Mock.Operations, Collections: res.Mock.Collections(), Warnings: res.Warnings,
		ServiceYAML: res.Service, Seed: res.Seed}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	if out.Collections == nil {
		out.Collections = []string{}
	}
	if req.DryRun {
		out.Next = "Check the operations, then create the mock."
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
		return
	}
	if exists {
		// Write the new folder beside the old one, then swap, so a failure keeps the old mock.
		staged := svcDir + ".replace"
		os.RemoveAll(staged)
		if err := writeTreeAtomic(staged, files); err != nil {
			h.sendStoreError(ctx, "write services/"+req.ID, err)
			return
		}
		old := svcDir + ".old"
		os.RemoveAll(old)
		if err := os.Rename(svcDir, old); err != nil {
			os.RemoveAll(staged)
			h.sendStoreError(ctx, "replace services/"+req.ID, err)
			return
		}
		if err := os.Rename(staged, svcDir); err != nil {
			_ = os.Rename(old, svcDir)
			os.RemoveAll(staged)
			h.sendStoreError(ctx, "replace services/"+req.ID, err)
			return
		}
		os.RemoveAll(old)
	} else if err := writeTreeAtomic(svcDir, files); err != nil {
		h.sendStoreError(ctx, "write services/"+req.ID, err)
		return
	}
	out.Next = "Add the services entry to each task that uses the mock and put its starting data in the task's seed/" + req.ID +
		".json, then run the readiness checks: they build the mock from services/" + req.ID + " and check every task against it."
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, &out)
}
