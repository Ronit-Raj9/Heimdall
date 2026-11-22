package tenancy

import (
	"sync/atomic"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// RouteGate may refuse an authorized request of an org before its handler
// runs (lane Y16: plan limits while billing is on). It answers a refusal
// itself and returns false; true lets the request through. pattern is the
// router pattern the policy table keys on.
type RouteGate func(ctx *fasthttp.RequestCtx, mode schemas.Mode, orgID, method, pattern string) bool

var routeGate atomic.Pointer[RouteGate]

// SetRouteGate installs the dispatcher's gate; nil removes it.
func SetRouteGate(g RouteGate) {
	if g == nil {
		routeGate.Store(nil)
		return
	}
	routeGate.Store(&g)
}

// passGate runs the installed gate, if any.
func passGate(ctx *fasthttp.RequestCtx, mode schemas.Mode, o *Org, method, pattern string) bool {
	g := routeGate.Load()
	if g == nil || o == nil {
		return true
	}
	return (*g)(ctx, mode, o.ID, method, pattern)
}
