package server

import (
	"errors"
	"io/fs"
	"mime"
	"path"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// indexFile is the SPA shell every client-side route falls back to.
const indexFile = "index.html"

// UIHandler serves the built single-page UI from an fs.FS.
type UIHandler struct {
	mode schemas.Mode
	dist fs.FS
}

// NewUIHandler creates the UI handler. dist has index.html at its root.
func NewUIHandler(mode schemas.Mode, dist fs.FS) *UIHandler {
	return &UIHandler{mode: mode, dist: dist}
}

// RegisterRoutes registers the catch-all GET routes. The API routes are
// registered on the same router and win over the catch-all.
func (h *UIHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET("/", lib.ChainMiddlewares(h.serve, middlewares...))
	r.GET("/{filepath:*}", lib.ChainMiddlewares(h.serve, middlewares...))
}

// serve answers a static asset, or index.html for a client-side route. An
// /api path that reached the catch-all has no handler and gets JSON 404, not
// the shell. A missing asset (a path with an extension) is 404 too, so a
// broken script reference never receives HTML.
func (h *UIHandler) serve(ctx *fasthttp.RequestCtx) {
	reqPath := string(ctx.Path())
	if reqPath == "/api" || strings.HasPrefix(reqPath, "/api/") {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "route not found: "+reqPath)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+reqPath), "/")
	if name == "" {
		name = indexFile
	}
	for _, seg := range strings.Split(name, "/") {
		if strings.HasPrefix(seg, ".") {
			h.notFound(ctx, reqPath)
			return
		}
	}
	data, err := fs.ReadFile(h.dist, name)
	if err != nil && errors.Is(err, fs.ErrNotExist) && path.Ext(name) == "" {
		name = indexFile
		data, err = fs.ReadFile(h.dist, name)
	}
	if err != nil {
		h.notFound(ctx, reqPath)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	ctx.SetContentType(contentType)
	switch {
	case strings.HasPrefix(name, "assets/"):
		ctx.Response.Header.Set("Cache-Control", "public, max-age=31536000, immutable")
	case path.Ext(name) == ".html":
		ctx.Response.Header.Set("Cache-Control", "no-cache")
	default:
		ctx.Response.Header.Set("Cache-Control", "public, max-age=3600")
	}
	ctx.SetBody(data)
}

// notFound answers a missing UI file. Without index.html the UI was never
// built (ui/dist holds only .gitkeep until `npm run build`), and the answer
// says so.
func (h *UIHandler) notFound(ctx *fasthttp.RequestCtx, reqPath string) {
	msg := "not found: " + reqPath
	if _, err := fs.Stat(h.dist, indexFile); err != nil {
		msg = "UI not built: run `npm run build` in bench/ui; the API is under " + handlers.APIPrefix
	}
	lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, msg)
}
