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
	out := NormalizeCatalog(*src)
	out.Mode = h.mode
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// NormalizeCatalog returns c with every nil list and map made empty, so a
// bench root without an agents/ folder (or suites, or probes) answers [] and
// never null: the UI reads these as lists. The suites are copied, never
// changed in place, because c shares them with the cached catalog.
func NormalizeCatalog(c schemas.Catalog) schemas.Catalog {
	c.Suites = emptyIfNil(c.Suites)
	c.Probes = emptyIfNil(c.Probes)
	c.Agents = emptyIfNil(c.Agents)
	c.Metrics = emptyIfNil(c.Metrics)
	c.Gates = emptyIfNil(c.Gates)
	if c.Parts == nil {
		c.Parts = map[string]string{}
	}
	suites := make([]schemas.CatalogSuite, len(c.Suites))
	for i, s := range c.Suites {
		s.Tasks = emptyIfNil(s.Tasks)
		s.Probes = emptyIfNil(s.Probes)
		suites[i] = s
	}
	c.Suites = suites
	return c
}

// emptyIfNil turns a nil slice into an empty one.
func emptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
