package handlers

import (
	"bytes"
	"encoding/json"
	"mime"
	"slices"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// AuditHandler turns a published per-task table into a verdict.
type AuditHandler struct {
	mode    schemas.Mode
	auditor Auditor
	write   lib.Middleware
}

// NewAuditHandler creates the audit handler. write guards the POST route;
// auditor may be nil.
func NewAuditHandler(mode schemas.Mode, auditor Auditor, write lib.Middleware) *AuditHandler {
	return &AuditHandler{mode: mode, auditor: auditor, write: write}
}

// RegisterRoutes registers POST /audit.
func (h *AuditHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	r.POST(APIPrefix+"/audit", lib.ChainMiddlewares(h.postAudit, writeMW...))
}

// postAudit handles POST /api/bench/audit. An application/json body is a
// schemas.AuditRequest; any other body is the CSV table. The auditor parses
// and checks the table, so its errors are the caller's input errors. The
// response is the Verdict plus mode.
func (h *AuditHandler) postAudit(ctx *fasthttp.RequestCtx) {
	if h.auditor == nil {
		sendUnavailable(ctx, h.mode, "auditor")
		return
	}
	body := ctx.PostBody()
	if len(bytes.TrimSpace(body)) == 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "request body must be the per-task table as CSV or JSON")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	var (
		v   *schemas.Verdict
		err error
	)
	if mediaType(ctx) == "application/json" {
		var req schemas.AuditRequest
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse audit request: "+err.Error())
			return
		}
		v, err = h.auditor.Audit(work, &req)
	} else {
		v, err = h.auditor.AuditCSV(work, bytes.Clone(body))
	}
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "audit table: "+err.Error())
		return
	}
	if v == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "audit table: no verdict")
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, v)
}

// mediaType returns the request's Content-Type without parameters, lower
// case, or "" when absent or malformed.
func mediaType(ctx *fasthttp.RequestCtx) string {
	mt, _, err := mime.ParseMediaType(string(ctx.Request.Header.ContentType()))
	if err != nil {
		return ""
	}
	return mt
}
