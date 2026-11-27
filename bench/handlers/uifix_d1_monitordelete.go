package handlers

import (
	"context"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// Deleting a monitor test (safety rule test; UI fix wave D1, M7-23):
// DELETE /monitors/certifications/{id}, admin only (the policy table and
// the handler both check), audited by the dispatcher like every write.

// MonitorCertDeleter removes a stored certificate; monitorcert.CertStore
// implements it. A source without it answers 501.
type MonitorCertDeleter interface {
	Delete(ctx context.Context, certID string) error
}

// MonitorCertDeleteRouteDoc documents the delete route.
var MonitorCertDeleteRouteDoc = RouteDoc{Method: "DELETE", Path: APIPrefix + "/monitors/certifications/{id}", Tag: "monitors",
	Summary: "Delete a monitor certificate (safety rule test) and its report (admin). Its runs and comparisons are not touched", Auth: AuthWrite, Response: DeletedInfo{}}

func init() { APIRoutes = append(APIRoutes, MonitorCertDeleteRouteDoc) }

// WithWrite sets the middleware that guards DELETE (the write token).
func (h *MonitorCertHandler) WithWrite(write lib.Middleware) *MonitorCertHandler {
	h.write = write
	return h
}

// delete handles DELETE /api/bench/monitors/certifications/{id}.
func (h *MonitorCertHandler) delete(ctx *fasthttp.RequestCtx) {
	if !isLocalCaller(ctx) && !auth.PrincipalFrom(ctx).Can(auth.RoleAdmin) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only an org owner or admin may delete a monitor test")
		return
	}
	if h.certs == nil {
		sendUnavailable(ctx, h.mode, "monitor certificates")
		return
	}
	del, ok := h.certs.(MonitorCertDeleter)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotImplemented, lib.ErrTypeUnavailable, "This server cannot delete monitor tests.")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "monitor certificate")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := del.Delete(work, id); err != nil {
		sendDepError(ctx, h.mode, "delete monitor certificate "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, DeletedInfo{Deleted: id, Kind: "monitor_certification"})
}
