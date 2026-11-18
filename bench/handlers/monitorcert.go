package handlers

// Eval integrity area (S2): monitor certification routes
// (docs/monitor-certification.md).

import (
	"context"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// MonitorCertSource reads stored monitor certificates. cmd/bifrost-bench
// satisfies it with monitorcert.CertStore.
type MonitorCertSource interface {
	List(ctx context.Context) (*schemas.MonitorCertList, error)
	Get(ctx context.Context, certID string) (*schemas.MonitorCertificate, error)
	Report(ctx context.Context, certID string) ([]byte, error)
}

// Monitor certification route docs, listed in APIRoutes.
var (
	MonitorCertListRouteDoc = RouteDoc{Method: "GET", Path: APIPrefix + "/monitors/certifications", Tag: "monitors",
		Summary: "Monitor certifications, newest first", Response: schemas.MonitorCertList{}}
	MonitorCertGetRouteDoc = RouteDoc{Method: "GET", Path: APIPrefix + "/monitors/certifications/{id}", Tag: "monitors",
		Summary: "One monitor certificate: labels, operating points, families, items", Response: schemas.MonitorCertificate{}}
	MonitorCertReportRouteDoc = RouteDoc{Method: "GET", Path: APIPrefix + "/monitors/certifications/{id}/report.md", Tag: "monitors",
		Summary: "A monitor certificate as Markdown", Raw: "text/markdown"}
)

// MonitorCertHandler serves stored monitor certificates. Certifying is a
// CLI command: it runs the monitor, which may be a container, and the API
// does not start containers on a read route.
type MonitorCertHandler struct {
	mode  schemas.Mode
	certs MonitorCertSource
}

// NewMonitorCertHandler creates the handler; certs may be nil, and the
// routes then answer 503.
func NewMonitorCertHandler(mode schemas.Mode, certs MonitorCertSource) *MonitorCertHandler {
	return &MonitorCertHandler{mode: mode, certs: certs}
}

// RegisterRoutes registers the monitor certification routes.
func (h *MonitorCertHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(MonitorCertListRouteDoc.Path, lib.ChainMiddlewares(h.list, middlewares...))
	r.GET(MonitorCertGetRouteDoc.Path, lib.ChainMiddlewares(h.get, middlewares...))
	r.GET(MonitorCertReportRouteDoc.Path, lib.ChainMiddlewares(h.report, middlewares...))
}

// list handles GET /api/bench/monitors/certifications.
func (h *MonitorCertHandler) list(ctx *fasthttp.RequestCtx) {
	if h.certs == nil {
		sendUnavailable(ctx, h.mode, "monitor certificates")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	list, err := h.certs.List(work)
	if err != nil {
		sendDepError(ctx, h.mode, "list monitor certificates", err)
		return
	}
	if list.Certifications == nil {
		list.Certifications = []schemas.MonitorCertSummary{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, list)
}

// get handles GET /api/bench/monitors/certifications/{id}.
func (h *MonitorCertHandler) get(ctx *fasthttp.RequestCtx) {
	if h.certs == nil {
		sendUnavailable(ctx, h.mode, "monitor certificates")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "monitor certificate")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	c, err := h.certs.Get(work, id)
	if err != nil {
		sendDepError(ctx, h.mode, "monitor certificate "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, c)
}

// report handles GET /api/bench/monitors/certifications/{id}/report.md.
func (h *MonitorCertHandler) report(ctx *fasthttp.RequestCtx) {
	if h.certs == nil {
		sendUnavailable(ctx, h.mode, "monitor certificates")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "monitor certificate")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	b, err := h.certs.Report(work, id)
	if err != nil {
		sendDepError(ctx, h.mode, "monitor certificate report "+id, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("text/markdown; charset=utf-8")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetBody(b)
}
