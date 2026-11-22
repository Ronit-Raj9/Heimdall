package handlers

// Drain mode (INFRA_I4 item 3a): an operator stops new leases before an
// upgrade and waits for the runs in flight to finish (drain/drain.go).
//
//   GET  /api/bench/admin/drain   drain state and leases in flight
//   POST /api/bench/admin/drain   {"on": true} stops new leases, {"on": false} resumes them
//
// Callers: the drain token in the X-Heimdall-Drain-Token header (written by
// the server at start, read by `bifrost-bench drain` in the same
// container), a server admin (hosted), or in local mode a caller that
// passes the write token check.

import (
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/drain"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// DrainRequest turns drain mode on or off.
type DrainRequest struct {
	On bool `json:"on"`
}

// DrainHandler serves the drain routes over the process-wide drain state.
type DrainHandler struct {
	mode    schemas.Mode
	hosted  bool
	write   lib.Middleware // local mode: the write token check, the fallback when no drain token is sent
	isAdmin func(*auth.Principal) bool
	logger  schemas.Logger
}

// NewDrainHandler builds the handler. write is the local write token check
// (nil: none); hosted turns the fallback off, so only the drain token or a
// server admin may call.
func NewDrainHandler(mode schemas.Mode, hosted bool, write lib.Middleware, logger schemas.Logger) *DrainHandler {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	return &DrainHandler{mode: mode, hosted: hosted, write: write, isAdmin: auth.IsServerAdmin, logger: logger}
}

// WithAdminCheck replaces auth.IsServerAdmin (tests).
func (h *DrainHandler) WithAdminCheck(f func(*auth.Principal) bool) *DrainHandler {
	h.isAdmin = f
	return h
}

// RegisterRoutes registers the drain routes.
func (h *DrainHandler) RegisterRoutes(r *router.Router, mw ...lib.Middleware) {
	p := APIPrefix + "/admin/drain"
	r.GET(p, lib.ChainMiddlewares(h.guard(h.get), mw...))
	r.POST(p, lib.ChainMiddlewares(h.guard(h.set), mw...))
}

func (h *DrainHandler) guard(f fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if tok := string(ctx.Request.Header.Peek(drain.TokenHeader)); tok != "" {
			if !drain.CheckToken(tok) {
				lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "the drain token does not match this server's")
				return
			}
			ctx.SetUserValue(drainActorKey, "drain token")
			f(ctx)
			return
		}
		if p := auth.PrincipalFrom(ctx); h.isAdmin != nil && h.isAdmin(p) {
			ctx.SetUserValue(drainActorKey, p.Email)
			f(ctx)
			return
		}
		if !h.hosted {
			ctx.SetUserValue(drainActorKey, "local")
			if h.write != nil && !ctx.IsGet() { // local reads are open like every local GET
				h.write(f)(ctx)
				return
			}
			f(ctx)
			return
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"server admins or the drain token only: run `heimdall drain` on the server, or add your email to HEIMDALL_SERVER_ADMINS")
	}
}

const drainActorKey = "drain_actor"

func (h *DrainHandler) get(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, drain.Get())
}

func (h *DrainHandler) set(ctx *fasthttp.RequestCtx) {
	var req DrainRequest
	if !decodeStrict(ctx, h.mode, &req, "drain") {
		return
	}
	who, _ := ctx.UserValue(drainActorKey).(string)
	var st drain.Status
	if req.On {
		st = drain.Start(who)
		h.logger.Warn("drain mode on: no new leases; runs in flight finish", "by", who, "in_flight", st.InFlight)
	} else {
		st = drain.Stop()
		h.logger.Info("drain mode off: leases are handed out again", "by", who)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, st)
}

func init() {
	p := APIPrefix + "/admin/drain"
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: p, Tag: "admin", Summary: "Drain mode state and leases in flight (drain token or server admin)", Response: drain.Status{}},
		RouteDoc{Method: "POST", Path: p, Tag: "admin", Summary: "Turn drain mode on (no new leases; runs in flight finish) or off (drain token or server admin)", Request: DrainRequest{}, Response: drain.Status{}},
	)
}
