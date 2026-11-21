package handlers

// Project blobs: raw upload and download of project files bigger than the
// 1 MiB editor limit (up to projectfs.LargeFileCap of the area, at most
// lib.MaxUploadBytes). `bifrost-bench push` uses PUT for big files; the
// Files page uses GET to download them. PUT goes through the upload
// middleware chain (env.U) and honours If-Match like /project/files.

import (
	"fmt"
	"os"
	"path"
	"strconv"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
)

// ProjectBlobsPath is the raw large-file route, followed by {area}/{path}.
const ProjectBlobsPath = APIPrefix + "/project/blobs"

func init() {
	RegisterOrgArea("projectblobs", func(env OrgEnv, r *router.Router) {
		h := &projectFilesHandler{mode: env.Mode, store: env.Services.Projects}
		r.GET(ProjectBlobsPath+"/{area}/{path:*}", env.R(h.getBlob))
		r.PUT(ProjectBlobsPath+"/{area}/{path:*}", env.U(h.putBlob))
	})
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: ProjectBlobsPath + "/{area}/{path:*}", Tag: "project",
			Summary: "Download one project file's raw bytes, any size (ETag is its sha256)",
			Raw:     "application/octet-stream", Status: fasthttp.StatusOK},
		RouteDoc{Method: "PUT", Path: ProjectBlobsPath + "/{area}/{path:*}", Tag: "project",
			Summary: "Save a large file atomically (up to the area's large-file cap, at most 64 MiB); If-Match (sha256) gives 409 when the file changed; files over 1 MiB are not versioned",
			Auth:    AuthWrite, RawRequest: "application/octet-stream",
			Query:    []QueryDoc{{Name: "note", Description: "Short note stored with the version"}, {Name: "if_match", Description: "Expected sha256, same as the If-Match header"}},
			Response: ProjectFileSaved{}},
	)
}

func (h *projectFilesHandler) putBlob(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	a, rel, ok := h.pathParams(ctx)
	if !ok {
		return
	}
	lw, ok := h.store.(projectfs.LargeWriter)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotImplemented, lib.ErrTypeUnavailable,
			"this server's project store does not take large files: upload them as a zip on the Upload page")
		return
	}
	limit := projectfs.LargeFileCap(a)
	if limit > lib.MaxUploadBytes {
		limit = lib.MaxUploadBytes
	}
	body := ctx.PostBody()
	if int64(len(body)) > limit {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge,
			fmt.Sprintf("file is %d bytes; the %s area takes files up to %d bytes", len(body), a, limit))
		return
	}
	entry, err := lw.WriteLarge(ctx, a, rel, append([]byte(nil), body...), writeOpts(ctx))
	if err != nil {
		h.sendStoreError(ctx, rel, err)
		return
	}
	ctx.Response.Header.Set("ETag", strconv.Quote(entry.SHA256))
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ProjectFileSaved{Area: string(a), Path: entry.Path, Entry: &entry})
}

func (h *projectFilesHandler) getBlob(ctx *fasthttp.RequestCtx) {
	if !h.ready(ctx) {
		return
	}
	a, rel, ok := h.pathParams(ctx)
	if !ok {
		return
	}
	full, err := h.store.Resolve(a, rel)
	if err != nil {
		h.sendStoreError(ctx, rel, err)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		h.sendStoreError(ctx, rel, projectfs.ErrNotFound)
		return
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		h.sendStoreError(ctx, rel, projectfs.ErrInvalid)
		return
	}
	sum, _, err := hashRegularFile(full)
	if err == nil {
		ctx.Response.Header.Set("ETag", strconv.Quote(sum))
	}
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Content-Disposition", "attachment; filename="+strconv.Quote(path.Base(rel)))
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.SetContentType("application/octet-stream")
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetBodyStream(f, int(st.Size())) // fasthttp closes f after sending
}
