package handlers

// Eval integrity area (S2): the compliance evidence pack of an experiment
// (docs/compliance.md).

import (
	"context"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// ComplianceSource builds an experiment's compliance pack and its Markdown
// rendering. cmd/bifrost-bench satisfies it with the compliance package.
type ComplianceSource interface {
	CompliancePack(ctx context.Context, expID string) (*schemas.CompliancePack, []byte, error)
}

// ComplianceRouteDoc describes GET /experiments/{id}/compliance.
var ComplianceRouteDoc = RouteDoc{
	Method: "GET", Path: APIPrefix + "/experiments/{id}/compliance", Tag: "analysis",
	Summary:  "Compliance evidence pack: executed tests mapped to OWASP and NIST ids, record-keeping, sealed evidence links; a download, not a certificate",
	Query:    []QueryDoc{{Name: "format", Description: "json (default) or md for the Markdown document"}},
	Response: schemas.CompliancePack{}, AlsoRaw: "text/markdown",
}

// ComplianceHandler serves compliance packs.
type ComplianceHandler struct {
	mode   schemas.Mode
	source ComplianceSource
}

// NewComplianceHandler creates the compliance handler; source may be nil.
func NewComplianceHandler(mode schemas.Mode, source ComplianceSource) *ComplianceHandler {
	return &ComplianceHandler{mode: mode, source: source}
}

// RegisterRoutes registers the compliance route.
func (h *ComplianceHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(ComplianceRouteDoc.Path, lib.ChainMiddlewares(h.getCompliance, middlewares...))
}

// getCompliance handles GET /api/bench/experiments/{id}/compliance: the pack
// as a JSON attachment, or as Markdown with format=md. Building it verifies
// every counted run's seal and the seals.log chain, and may analyze the
// experiment when it has no analysis yet.
func (h *ComplianceHandler) getCompliance(ctx *fasthttp.RequestCtx) {
	if h.source == nil {
		sendUnavailable(ctx, h.mode, "compliance packs")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	format := string(ctx.QueryArgs().Peek("format"))
	if format != "" && format != "json" && format != "md" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "format must be json or md")
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	pack, md, err := h.source.CompliancePack(work, id)
	if err != nil {
		sendDepError(ctx, h.mode, "compliance pack of "+id, err)
		return
	}
	if format == "md" {
		ctx.SetStatusCode(fasthttp.StatusOK)
		ctx.SetContentType("text/markdown; charset=utf-8")
		ctx.Response.Header.Set(fasthttp.HeaderContentDisposition, `attachment; filename="compliance-`+id+`.md"`)
		ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
		ctx.Response.Header.Set("Cache-Control", "no-store")
		ctx.SetBody(md)
		return
	}
	ctx.Response.Header.Set(fasthttp.HeaderContentDisposition, `attachment; filename="compliance-`+id+`.json"`)
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, pack)
}
