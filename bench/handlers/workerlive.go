package handlers

import (
	"context"
	"regexp"

	"github.com/maximhq/bifrost/bench/environments"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livefeed"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Live screen of leased runs (Z03, livefeed). A worker posts the newest
// screen frame of its running browser or desktop run every couple of
// seconds; the frame goes to the organization's hub, display only, and the
// answer carries the inputs a person queued for the run when it is a debug
// run.

// LiveLeaseAPI is a WorkerAPI that can check a lease for a live frame
// (engine.Coordinator is one). Without it the route answers 503.
type LiveLeaseAPI interface {
	LiveLease(ctx context.Context, leaseID, workerID string) (debug bool, err error)
}

// liveFramePost is the body of POST /workers/leases/{lease_id}/live-frame.
type liveFramePost struct {
	WorkerID string `json:"worker_id"`
	schemas.LiveFrame
}

var liveRunIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// liveFrame handles POST /api/bench/workers/leases/{lease_id}/live-frame.
func (h *WorkersHandler) liveFrame(ctx *fasthttp.RequestCtx) {
	leaseID, ok := idParam(ctx, h.mode, "lease_id", "lease")
	if !ok {
		return
	}
	api, ok := h.api.(LiveLeaseAPI)
	if !ok {
		sendUnavailable(ctx, h.mode, "live screen")
		return
	}
	var req liveFramePost
	if !h.decode(ctx, &req) {
		return
	}
	switch {
	case !liveRunIDRe.MatchString(req.RunID) || !liveRunIDRe.MatchString(req.Service):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "run_id and service must be short ids")
		return
	case len(req.Data) == 0 || len(req.Data) > schemas.MaxLiveFrameBytes:
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "data must be one image of at most 640 KiB")
		return
	}
	mime := environments.ImageMime(req.Data)
	if mime == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "data must be a PNG or JPEG image")
		return
	}
	work, cancel := h.work(ctx, readTimeout)
	defer cancel()
	debug, err := api.LiveLease(work, leaseID, req.WorkerID)
	if err != nil {
		h.sendErr(ctx, "live frame", err)
		return
	}
	f := req.LiveFrame
	f.Mime, f.Debug, f.TakeoverOK = mime, debug, debug && f.TakeoverOK // the plan decides debug, not the worker
	hub := livefeed.For(h.orgID)
	hub.Publish(f)
	reply := schemas.LiveFrameReply{}
	if debug {
		reply.Inputs = hub.Take(f.RunID)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, reply)
}
