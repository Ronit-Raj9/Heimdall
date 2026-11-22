package handlers

// Device login (RFC 8628, lane Z02): the server side of `heimdall login`.
// POST /auth/device/code and /auth/device/token are public (the device code
// is the credential, and the token endpoint answers RFC 8628 error codes);
// the lookup, approve and deny routes need a signed-in browser session.

import (
	"bytes"
	"net/url"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// Device login routes.
const (
	DeviceCodePath    = AuthPrefix + "/device/code"
	DeviceTokenPath   = AuthPrefix + "/device/token"
	DeviceLookupPath  = AuthPrefix + "/device"
	DeviceApprovePath = AuthPrefix + "/device/approve"
	DeviceDenyPath    = AuthPrefix + "/device/deny"
)

// DeviceHandler serves the device login routes over one DeviceFlow.
type DeviceHandler struct {
	mode schemas.Mode
	svc  *auth.Service
	flow *auth.DeviceFlow
}

// NewDeviceHandler creates the handler. svc nil (local mode) answers every
// route with 404: a local server has no accounts to log in to.
func NewDeviceHandler(mode schemas.Mode, svc *auth.Service) *DeviceHandler {
	h := &DeviceHandler{mode: mode, svc: svc}
	if svc != nil {
		h.flow = auth.NewDeviceFlow(svc)
	}
	return h
}

// RegisterRoutes registers the device login routes.
func (h *DeviceHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.POST(DeviceCodePath, lib.ChainMiddlewares(h.ready(h.postCode), middlewares...))
	r.POST(DeviceTokenPath, lib.ChainMiddlewares(h.ready(h.postToken), middlewares...))
	r.GET(DeviceLookupPath, lib.ChainMiddlewares(h.ready(h.getDevice), middlewares...))
	r.POST(DeviceApprovePath, lib.ChainMiddlewares(h.ready(h.postApprove), middlewares...))
	r.POST(DeviceDenyPath, lib.ChainMiddlewares(h.ready(h.postDeny), middlewares...))
}

func init() {
	OrgRouteDocs = append(OrgRouteDocs,
		RouteDoc{Method: "POST", Path: DeviceCodePath, Tag: "auth",
			Summary: "Start a device login (RFC 8628, heimdall login): a device code for the CLI and a short user code to approve in the browser; JSON or form body",
			Request: auth.DeviceCodeRequest{}, OptionalBody: true, Response: auth.DeviceCodeResponse{}},
		RouteDoc{Method: "POST", Path: DeviceTokenPath, Tag: "auth",
			Summary: "Poll a device login (RFC 8628 section 3.4): the API token once approved, else 400 with error authorization_pending, slow_down, access_denied or expired_token",
			Request: auth.DeviceTokenRequest{}, Response: auth.DeviceTokenResponse{}},
		RouteDoc{Method: "GET", Path: DeviceLookupPath, Tag: "auth",
			Summary: "A waiting device login by its user code, for the approval page (signed-in session)",
			Query:   []QueryDoc{{Name: "user_code", Description: "the code the CLI printed, like BCDF-GHJK"}}, Response: auth.DeviceInfo{}},
		RouteDoc{Method: "POST", Path: DeviceApprovePath, Tag: "auth",
			Summary: "Approve a device login: mints an API token in the session's current organization (scope evaluate by default, 90 days, revocable under Settings > API tokens)",
			Request: auth.DeviceDecision{}, Response: auth.DeviceApproved{}},
		RouteDoc{Method: "POST", Path: DeviceDenyPath, Tag: "auth",
			Summary: "Deny a device login: the CLI's next poll gets access_denied",
			Request: auth.DeviceDecision{}, Status: fasthttp.StatusNoContent},
	)
}

func (h *DeviceHandler) ready(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.flow == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
				"device login only exists in hosted mode; a local server needs no login: set HEIMDALL_TOKEN to its write token")
			return
		}
		ctx.Response.Header.Set("Cache-Control", "no-store")
		next(ctx)
	}
}

func (h *DeviceHandler) fail(ctx *fasthttp.RequestCtx, err error) {
	if h.svc != nil {
		h.svc.Fail(ctx, err)
		return
	}
	auth.SendError(ctx, h.mode, err)
}

// isForm reports a form-encoded body, the RFC 8628 wire format.
func isForm(ctx *fasthttp.RequestCtx) bool {
	return bytes.HasPrefix(bytes.ToLower(ctx.Request.Header.ContentType()), []byte("application/x-www-form-urlencoded"))
}

func formValues(ctx *fasthttp.RequestCtx) (url.Values, error) {
	return url.ParseQuery(string(ctx.PostBody()))
}

func (h *DeviceHandler) postCode(ctx *fasthttp.RequestCtx) {
	var req auth.DeviceCodeRequest
	switch {
	case isForm(ctx):
		v, err := formValues(ctx)
		if err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse form body: "+err.Error())
			return
		}
		req = auth.DeviceCodeRequest{ClientID: v.Get("client_id"), ClientName: v.Get("client_name"), Scope: v.Get("scope")}
	case len(bytes.TrimSpace(ctx.PostBody())) > 0:
		if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request body: "+err.Error())
			return
		}
	}
	out, err := h.flow.Start(req, h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *DeviceHandler) postToken(ctx *fasthttp.RequestCtx) {
	var req auth.DeviceTokenRequest
	if isForm(ctx) {
		v, err := formValues(ctx)
		if err != nil {
			lib.SendJSON(ctx, h.mode, fasthttp.StatusBadRequest, auth.DeviceTokenError{Code: auth.DeviceErrBadReq, Description: "parse form body: " + err.Error()})
			return
		}
		req = auth.DeviceTokenRequest{GrantType: v.Get("grant_type"), DeviceCode: v.Get("device_code"), ClientID: v.Get("client_id")}
	} else if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusBadRequest, auth.DeviceTokenError{Code: auth.DeviceErrBadReq, Description: "parse request body: " + err.Error()})
		return
	}
	out, derr := h.flow.Poll(req, h.svc.ClientIP(ctx))
	if derr != nil {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusBadRequest, derr)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *DeviceHandler) getDevice(ctx *fasthttp.RequestCtx) {
	code := strings.TrimSpace(string(ctx.QueryArgs().Peek("user_code")))
	if code == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "user_code is required")
		return
	}
	info, err := h.flow.Lookup(auth.PrincipalFrom(ctx), code)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, info)
}

func (h *DeviceHandler) decision(ctx *fasthttp.RequestCtx) (auth.DeviceDecision, bool) {
	var req auth.DeviceDecision
	if err := decodeJSONStrict(ctx.PostBody(), &req); err != nil || strings.TrimSpace(req.UserCode) == "" {
		msg := "user_code is required"
		if err != nil {
			msg = "parse request body: " + err.Error()
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
		return req, false
	}
	return req, true
}

func (h *DeviceHandler) postApprove(ctx *fasthttp.RequestCtx) {
	req, ok := h.decision(ctx)
	if !ok {
		return
	}
	w, cancel := workContext(ctx, authTimeout)
	defer cancel()
	out, err := h.flow.Approve(w, auth.PrincipalFrom(ctx), req.UserCode, h.svc.ClientIP(ctx))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

func (h *DeviceHandler) postDeny(ctx *fasthttp.RequestCtx) {
	req, ok := h.decision(ctx)
	if !ok {
		return
	}
	if err := h.flow.Deny(auth.PrincipalFrom(ctx), req.UserCode, h.svc.ClientIP(ctx)); err != nil {
		h.fail(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}
