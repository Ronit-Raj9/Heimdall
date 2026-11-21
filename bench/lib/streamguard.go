package lib

import (
	"sync"
	"time"

	"github.com/valyala/fasthttp"
)

// streamGuardKey is the request user value holding a StreamGuard.
const streamGuardKey = "heimdall.stream_guard"

// StreamRecheckEvery is how often a StreamGuard asks its check again; in
// between it answers the last result.
const StreamRecheckEvery = 10 * time.Second

// SetStreamGuard attaches check to the request: a long-lived response (a
// server-sent event stream) calls StreamGuardOf's result before sending more
// data and ends when it answers false. The hosted dispatcher sets it to
// re-authorize the caller, so a member removed from a private organization
// stops receiving events from a stream opened before the removal.
func SetStreamGuard(ctx *fasthttp.RequestCtx, check func() bool) {
	ctx.SetUserValue(streamGuardKey, throttle(check, StreamRecheckEvery, time.Now))
}

// StreamGuardOf returns the request's stream guard, or one that always
// allows (local mode, tests). Read it in the handler: a body stream writer
// runs after the handler returned and must not touch the request context.
func StreamGuardOf(ctx *fasthttp.RequestCtx) func() bool {
	if g, ok := ctx.UserValue(streamGuardKey).(func() bool); ok && g != nil {
		return g
	}
	return func() bool { return true }
}

// throttle runs check at most once per every and caches a refusal for good.
func throttle(check func() bool, every time.Duration, now func() time.Time) func() bool {
	var (
		mu      sync.Mutex
		last    time.Time
		allowed = true
	)
	return func() bool {
		mu.Lock()
		defer mu.Unlock()
		if !allowed {
			return false
		}
		if t := now(); last.IsZero() || t.Sub(last) >= every {
			last = t
			allowed = check()
		}
		return allowed
	}
}
