package handlers

import (
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// CatalogHandler serves the part catalog that drives the UI.
type CatalogHandler struct {
	mode    schemas.Mode
	catalog CatalogSource
}

// NewCatalogHandler creates the catalog handler. catalog may be nil.
func NewCatalogHandler(mode schemas.Mode, catalog CatalogSource) *CatalogHandler {
	return &CatalogHandler{mode: mode, catalog: catalog}
}

// RegisterRoutes registers GET /catalog.
func (h *CatalogHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/catalog", lib.ChainMiddlewares(h.getCatalog, middlewares...))
}

// getCatalog handles GET /api/bench/catalog. The catalog is copied before the
// mode is set, so the shared, read-only registry is never mutated.
func (h *CatalogHandler) getCatalog(ctx *fasthttp.RequestCtx) {
	if h.catalog == nil {
		sendUnavailable(ctx, h.mode, "catalog")
		return
	}
	src := h.catalog.Catalog()
	if src == nil {
		sendUnavailable(ctx, h.mode, "catalog")
		return
	}
	out := *src
	out.Mode = h.mode
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}
