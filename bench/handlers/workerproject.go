package handlers

// Org project transfer (PROD2 P0 isolation (a)): GET /workers/project hands
// a runner of the organization that organization's project (suites, agents,
// experiments, policies) as a gzip tar, so the shared runner needs no mount
// of the server's data volume and holds only the projects of the orgs it
// serves, each fetched with that org's own runner token. An If-None-Match
// with the current ETag answers 304.

import (
	"bufio"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/valyala/fasthttp"
)

// WorkerProjectPath is the route pattern.
const WorkerProjectPath = sharedpool.ProjectPath

type workerProjectHandler struct {
	mode  schemas.Mode
	orgID string
	root  string
}

func init() {
	RegisterOrgArea("workerproject", func(env OrgEnv, r *router.Router) {
		h := &workerProjectHandler{mode: env.Mode, orgID: env.Services.OrgID}
		if env.Cfg != nil {
			h.root = env.Cfg.Root
		}
		r.GET(WorkerProjectPath, env.R(h.get))
	})
}

func (h *workerProjectHandler) get(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	p := auth.PrincipalFrom(ctx)
	if p == nil || p.Kind != auth.KindRunner {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"only a runner token (hmr_...) can fetch the organization's project")
		return
	}
	if h.orgID == "" || p.OrgID != h.orgID {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"the runner token belongs to another organization")
		return
	}
	if h.root == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "no project folder for this organization")
		return
	}
	etag, err := sharedpool.ProjectETag(h.root)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "read the organization's project: "+err.Error())
		return
	}
	ctx.Response.Header.Set("ETag", etag)
	if string(ctx.Request.Header.Peek("If-None-Match")) == etag {
		ctx.SetStatusCode(fasthttp.StatusNotModified)
		return
	}
	ctx.SetContentType("application/gzip")
	ctx.SetStatusCode(fasthttp.StatusOK)
	root := h.root
	ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
		// A failure mid-stream truncates the archive; the runner's gzip
		// reader then fails and it keeps its previous copy.
		_ = sharedpool.WriteProject(w, root)
		_ = w.Flush()
	})
}
