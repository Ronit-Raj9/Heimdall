package handlers

import (
	"context"
	"errors"
	"time"

	"github.com/maximhq/bifrost/bench/billing"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Default deadlines for work a handler hands to a dependency.
const (
	readTimeout    = 30 * time.Second
	analyzeTimeout = 2 * time.Minute
)

// workContext bounds a dependency call by a deadline. It deliberately does
// not derive from *fasthttp.RequestCtx: its Done reads server state that
// Shutdown writes without a lock, a data race under -race. The server's
// graceful shutdown waits for in-flight calls instead.
func workContext(_ *fasthttp.RequestCtx, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// pathParam returns a router path parameter.
func pathParam(ctx *fasthttp.RequestCtx, name string) string {
	v, ok := ctx.UserValue(name).(string)
	if !ok {
		return ""
	}
	return v
}

// validID reports whether id is safe to hand to a dependency that may join it
// into a path: 1 to 128 characters of letters, digits, '-', '_' and '.', and
// not a dot segment.
func validID(id string) bool {
	if id == "" || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	for _, c := range id {
		ok := c == '-' || c == '_' || c == '.' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}

// idParam returns a validated path id, or answers 404 and returns false.
func idParam(ctx *fasthttp.RequestCtx, mode schemas.Mode, name, what string) (string, bool) {
	id := pathParam(ctx, name)
	if !validID(id) {
		sendNotFound(ctx, mode, what, id)
		return "", false
	}
	return id, true
}

// sendDepError maps a dependency error to a status: shared sentinel errors
// keep their meaning, anything else is a 500 with the error text.
func sendDepError(ctx *fasthttp.RequestCtx, mode schemas.Mode, what string, err error) {
	switch {
	case errors.Is(err, schemas.ErrNotFound):
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": "+err.Error())
	case errors.Is(err, ErrKeyOverBudget), errors.Is(err, ErrModelKeyMissing): // G10 budgets, X01 own keys (keybudget.go)
		lib.SendErrorCode(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, keyRefusalCode(err), what+": "+err.Error(), ModelKeysLink)
	case errors.Is(err, billing.ErrPlanLimit): // Y16: the org's plan does not cover it (billing on)
		SendPlanLimit(ctx, mode, err)
	case errors.Is(err, ErrKeyStoreUnavailable), errors.Is(err, ErrDailyCap): // X01: fail closed; daily caps
		lib.SendError(ctx, mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, what+": "+err.Error())
	case errors.Is(err, schemas.ErrExists):
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": "+err.Error())
	case errors.Is(err, schemas.ErrReadOnly):
		lib.SendError(ctx, mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, what+": "+err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		lib.SendError(ctx, mode, fasthttp.StatusGatewayTimeout, lib.ErrTypeUnavailable, what+": timed out")
	default:
		lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, what+": "+err.Error())
	}
}

// sendUnavailable answers a route whose dependency this server was not given,
// for example the launcher in recorded mode.
func sendUnavailable(ctx *fasthttp.RequestCtx, mode schemas.Mode, dep string) {
	lib.SendError(ctx, mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
		dep+" is not available on this server (mode "+string(mode)+")")
}

// sendNotFound answers an unknown id.
func sendNotFound(ctx *fasthttp.RequestCtx, mode schemas.Mode, what, id string) {
	lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+" not found: "+id)
}
