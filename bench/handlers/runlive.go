package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/livefeed"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Live screen and human takeover of running browser and desktop runs (Z03,
// livefeed). The org's hub holds the newest frame of each run that shows
// its screen; the run page polls GET /runs/{id}/live and loads the image
// from GET /runs/{id}/live/frame. In a debug run a member may queue inputs
// with POST /runs/{id}/takeover; the run performs them, records each in
// human_actions.jsonl and is marked human_assisted. Frames are display
// only and are never stored. The hub is per organization, so a run id of
// another org is simply not live here.

// RunLiveStatus answers GET /runs/{id}/live.
type RunLiveStatus struct {
	RunID      string `json:"run_id"`
	Live       bool   `json:"live"`
	Service    string `json:"service,omitempty"`
	Mime       string `json:"mime,omitempty"`
	At         string `json:"at,omitempty"`
	Debug      bool   `json:"debug,omitempty"`
	TakeoverOK bool   `json:"takeover_ok,omitempty"`
	Pending    int    `json:"pending,omitempty"` // inputs waiting for the run
	FrameURL   string `json:"frame_url,omitempty"`
}

// TakeoverRequest is the body of POST /runs/{id}/takeover.
type TakeoverRequest struct {
	Action    string `json:"action"` // click | type | key | scroll
	X         int    `json:"x,omitempty"`
	Y         int    `json:"y,omitempty"`
	Text      string `json:"text,omitempty"`
	Combo     string `json:"combo,omitempty"`
	Direction string `json:"direction,omitempty"`
	// FrameW and FrameH are the size of the live frame x and y were read from.
	FrameW int `json:"frame_w,omitempty"`
	FrameH int `json:"frame_h,omitempty"`
}

type liveHandler struct {
	mode schemas.Mode
	hub  *livefeed.Hub
}

func init() {
	RegisterOrgArea("liveview", func(env OrgEnv, r *router.Router) {
		h := &liveHandler{mode: env.Mode, hub: livefeed.For(env.Services.OrgID)}
		p := APIPrefix + "/runs/{id}"
		r.GET(p+"/live", env.R(h.getLive))
		r.GET(p+"/live/frame", env.R(h.getLiveFrame))
		r.POST(p+"/takeover", env.W(h.postTakeover))
	})
	APIRoutes = append(APIRoutes,
		RouteDoc{Method: "GET", Path: APIPrefix + "/runs/{id}/live", Tag: "runs",
			Summary: "Whether a running browser or desktop run shows its screen, and whether a person may take control (debug runs only)", Response: RunLiveStatus{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/runs/{id}/live/frame", Tag: "runs",
			Summary: "The newest screen image of a running run (png or jpeg), display only", Raw: "image/jpeg"},
		RouteDoc{Method: "POST", Path: APIPrefix + "/runs/{id}/takeover", Tag: "runs",
			Summary: "Queue one input (click, type, key, scroll) for a live debug run; the run is marked human_assisted and kept out of every verdict",
			Auth:    AuthWrite, Request: TakeoverRequest{}, Status: fasthttp.StatusAccepted, Response: schemas.HumanAction{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/runs/{id}/recordings", Tag: "runs",
			Summary: "Recordings of a browser or desktop run (video, HAR, console) with the recorded services' tool calls for syncing", Response: RunRecordings{}},
		RouteDoc{Method: "GET", Path: APIPrefix + "/runs/{id}/recordings/{n}", Tag: "runs",
			Summary: "One recording file of a run, served in byte ranges (Range header) for video seeking", Raw: "video/webm"},
		RouteDoc{Method: "POST", Path: APIPrefix + "/workers/leases/{lease_id}/live-frame", Tag: "workers",
			Summary: "Post the newest screen frame of a leased browser or desktop run; the answer carries a person's queued inputs for a debug run",
			Auth:    AuthWorker, Request: liveFramePost{}, Response: schemas.LiveFrameReply{}},
	)
}

// runID reads and checks the {id} parameter.
func (h *liveHandler) runID(ctx *fasthttp.RequestCtx) (string, bool) {
	id := pathParam(ctx, "id")
	if !liveRunIDRe.MatchString(id) {
		sendNotFound(ctx, h.mode, "run", id)
		return "", false
	}
	return id, true
}

// getLive handles GET /api/bench/runs/{id}/live. A run that is not showing
// its screen answers 200 with live false.
func (h *liveHandler) getLive(ctx *fasthttp.RequestCtx) {
	id, ok := h.runID(ctx)
	if !ok {
		return
	}
	out := RunLiveStatus{RunID: id}
	if f, ok := h.hub.Frame(id); ok {
		out.Live, out.Service, out.Mime, out.Debug, out.TakeoverOK = true, f.Service, f.Mime, f.Debug, f.TakeoverOK
		out.At = f.At.UTC().Format(time.RFC3339Nano)
		out.Pending = h.hub.Pending(id)
		out.FrameURL = APIPrefix + "/runs/" + id + "/live/frame"
	}
	ctx.Response.Header.Set("Cache-Control", "no-store")
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// getLiveFrame handles GET /api/bench/runs/{id}/live/frame.
func (h *liveHandler) getLiveFrame(ctx *fasthttp.RequestCtx) {
	id, ok := h.runID(ctx)
	if !ok {
		return
	}
	f, ok := h.hub.Frame(id)
	if !ok {
		sendNotFound(ctx, h.mode, "live screen of run", id)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType(f.Mime)
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Security-Policy", "sandbox")
	ctx.SetBody(f.Data)
}

// postTakeover handles POST /api/bench/runs/{id}/takeover.
func (h *liveHandler) postTakeover(ctx *fasthttp.RequestCtx) {
	id, ok := h.runID(ctx)
	if !ok {
		return
	}
	var req TakeoverRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return
	}
	a := schemas.HumanAction{Action: req.Action, X: req.X, Y: req.Y, Text: req.Text, Combo: req.Combo, Direction: req.Direction, FrameW: req.FrameW, FrameH: req.FrameH}
	if p := auth.PrincipalFrom(ctx); p != nil {
		a.By = p.Email
		if a.By == "" {
			a.By = p.UserID
		}
	}
	queued, err := h.hub.Queue(id, a)
	switch {
	case err == nil:
		lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, queued)
	case errors.Is(err, livefeed.ErrNotLive):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "run "+id+": "+err.Error())
	case errors.Is(err, livefeed.ErrNotDebug), errors.Is(err, livefeed.ErrNoTakeover):
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "run "+id+": "+err.Error())
	case errors.Is(err, livefeed.ErrQueueFull):
		lib.SendError(ctx, h.mode, fasthttp.StatusTooManyRequests, lib.ErrTypeConflict, err.Error())
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
	}
}
