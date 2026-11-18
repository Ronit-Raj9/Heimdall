package handlers

// X2: GET /api/bench/runs/{id}/services and
// GET /api/bench/runs/{id}/services/{service}/log (docs/multi-container.md).

import (
	"errors"
	"io/fs"
	"regexp"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// serviceIDRE is a service id as the run folder names its log
// (services/<id>.log): one host-name label.
var serviceIDRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// RunServices is the response of GET /api/bench/runs/{id}/services: the
// services that ran beside the agent, whether each came up, and whether its
// log was captured (compose services, services/<id>.log in the run folder).
type RunServices struct {
	RunID    string       `json:"run_id"`
	Sealed   bool         `json:"sealed"`
	Services []RunService `json:"services"`
}

// RunService is one service of a run.
type RunService struct {
	ID      string `json:"id"`
	Role    string `json:"role,omitempty"`
	Image   string `json:"image"`
	ImageID string `json:"image_id,omitempty"`
	Ready   bool   `json:"ready"`
	Error   string `json:"error,omitempty"`
	Proxied bool   `json:"proxied,omitempty"`
	// LogBytes is the size of the captured log; nil when the run holds none
	// (a service that is not from a compose file, or a log that could not be
	// read).
	LogBytes *int `json:"log_bytes,omitempty"`
}

// serviceLogName is the run file of one service's log.
func serviceLogName(id string) string { return "services/" + id + ".log" }

// getRunServices handles GET /api/bench/runs/{id}/services. A run without
// services answers an empty list.
func (h *RunsHandler) getRunServices(ctx *fasthttp.RequestCtx) {
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	id := rv.Run.RunID
	var detail schemas.RunDetail
	if err := fillScenario(h.index, id, &detail); err != nil {
		sendDepError(ctx, h.mode, "read services of run "+id, err)
		return
	}
	out := RunServices{RunID: id, Sealed: rv.Sealed, Services: []RunService{}}
	for _, s := range detail.Services {
		rs := RunService{ID: s.ID, Role: s.Role, Image: s.Image, ImageID: s.ImageID, Ready: s.Ready, Error: s.Error, Proxied: s.Proxied}
		if serviceIDRE.MatchString(s.ID) {
			if data, err := h.index.RunFile(id, serviceLogName(s.ID)); err == nil {
				n := len(data)
				rs.LogBytes = &n
			}
		}
		out.Services = append(out.Services, rs)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// getRunServiceLog handles GET /api/bench/runs/{id}/services/{service}/log:
// one compose service's captured output, as plain text. Like raw evidence it
// is served only from a sealed run that verifies against its seal, because
// it is service output the agent may have influenced; the body is text with
// nosniff, never rendered as HTML.
func (h *RunsHandler) getRunServiceLog(ctx *fasthttp.RequestCtx) {
	svc := pathParam(ctx, "service")
	if !serviceIDRE.MatchString(svc) {
		sendNotFound(ctx, h.mode, "service log", svc)
		return
	}
	rv, ok := h.lookup(ctx)
	if !ok {
		return
	}
	runID := rv.Run.RunID
	if !rv.Sealed {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
			"run "+runID+" is not sealed: service logs are served only from a sealed run")
		return
	}
	sv, isVerifier := h.index.(SealVerifier)
	if !isVerifier {
		sendUnavailable(ctx, h.mode, "seal verifier")
		return
	}
	wctx, cancel := workContext(ctx, readTimeout)
	err := sv.VerifySeal(wctx, runID)
	cancel()
	if err != nil {
		if errors.Is(err, schemas.ErrSealMismatch) {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
				"run "+runID+" does not match its seal and its service logs are not served: "+err.Error())
			return
		}
		sendDepError(ctx, h.mode, "verify seal of run "+runID, err)
		return
	}
	data, err := h.index.RunFile(runID, serviceLogName(svc))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, schemas.ErrNotFound) {
			sendNotFound(ctx, h.mode, "service log", svc)
			return
		}
		sendDepError(ctx, h.mode, "read the log of service "+svc+" of run "+runID, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("text/plain; charset=utf-8")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Content-Type-Options", "nosniff")
	ctx.Response.Header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	ctx.SetBody(data)
}
