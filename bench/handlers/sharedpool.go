package handlers

// Shared runner pool and guardrails (web parity lane L03, F3): hosted
// platform routes over sharedpool and quota.
//
//   GET  /api/bench/pool                       org viewer: the org's limits, kill switch, pool status
//   GET  /api/bench/images                     org viewer: the org's custom image requests
//   POST /api/bench/images                     org member: request a digest-pinned image
//   GET  /api/bench/admin/images               server admin: every request (?status=)
//   POST /api/bench/admin/images/{id}/approve  server admin
//   POST /api/bench/admin/images/{id}/reject   server admin
//   GET  /api/bench/admin/quotas/{id}          server admin: one org's limits
//   PUT  /api/bench/admin/quotas/{id}          server admin: set one org's limits
//   GET  /api/bench/admin/killswitch           server admin
//   POST /api/bench/admin/killswitch           server admin: {"on": true}
//   GET  /api/bench/admin/pool/assignments     shared runner only (Bearer HEIMDALL_SHARED_RUNNER_SECRET)

import (
	"bytes"
	"context"
	"errors"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/quota"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/sharedpool"
	"github.com/valyala/fasthttp"
)

// SharedPoolHandler serves the shared pool and guardrail routes.
type SharedPoolHandler struct {
	mode   schemas.Mode
	quota  quota.Store
	images *sharedpool.ImageStore
	pool   *sharedpool.Service
	secret func() string
	audit  AuditFunc
	// orgExists checks an org id for the admin quota routes; nil accepts any.
	orgExists func(ctx context.Context, id string) bool
	// isAdmin decides server admin routes; nil uses auth.IsServerAdmin.
	isAdmin func(*auth.Principal) bool
}

// WithAdminCheck replaces the server admin check (tests).
func (h *SharedPoolHandler) WithAdminCheck(f func(*auth.Principal) bool) *SharedPoolHandler {
	h.isAdmin = f
	return h
}

// NewSharedPoolHandler returns the handler. pool may be nil (no shared
// runner: assignments answer 503); secret nil reads sharedpool.Secret.
func NewSharedPoolHandler(mode schemas.Mode, q quota.Store, images *sharedpool.ImageStore, pool *sharedpool.Service, audit AuditFunc) *SharedPoolHandler {
	return &SharedPoolHandler{mode: mode, quota: q, images: images, pool: pool, secret: sharedpool.Secret, audit: audit}
}

// WithSecret replaces the secret source (tests).
func (h *SharedPoolHandler) WithSecret(f func() string) *SharedPoolHandler { h.secret = f; return h }

// WithOrgCheck sets the org existence check of the admin quota routes.
func (h *SharedPoolHandler) WithOrgCheck(f func(ctx context.Context, id string) bool) *SharedPoolHandler {
	h.orgExists = f
	return h
}

// PoolStatus is the answer of GET /api/bench/pool.
type PoolStatus struct {
	SharedPoolConfigured bool         `json:"shared_pool_configured"` // the server runs a shared runner
	KillSwitch           bool         `json:"kill_switch"`
	Limits               quota.Limits `json:"limits"`
	Message              string       `json:"message"`
}

// ImageRequestBody is the body of POST /api/bench/images.
type ImageRequestBody struct {
	Image     string `json:"image"`
	SizeBytes int64  `json:"size_bytes"`
	Note      string `json:"note,omitempty"`
}

// ImageRequestList is a list of image requests.
type ImageRequestList struct {
	Images []sharedpool.ImageRequest `json:"images"`
}

// ImageDecisionBody is the optional body of approve and reject.
type ImageDecisionBody struct {
	Note string `json:"note,omitempty"`
}

// KillSwitchBody is the kill switch state.
type KillSwitchBody struct {
	On bool `json:"on"`
}

// OrgQuota is one org's limits for the admin.
type OrgQuota struct {
	OrgID  string       `json:"org_id"`
	Limits quota.Limits `json:"limits"`
}

// RegisterRoutes implements server.RouteRegistrar.
func (h *SharedPoolHandler) RegisterRoutes(r *router.Router, mw ...lib.Middleware) {
	c := func(f fasthttp.RequestHandler) fasthttp.RequestHandler { return lib.ChainMiddlewares(f, mw...) }
	r.GET(APIPrefix+"/pool", c(h.member(auth.RoleViewer, h.status)))
	r.GET(APIPrefix+"/images", c(h.member(auth.RoleViewer, h.listOrgImages)))
	r.POST(APIPrefix+"/images", c(h.member(auth.RoleMember, h.requestImage)))
	a := APIPrefix + "/admin"
	r.GET(a+"/images", c(h.serverAdmin(h.listAllImages)))
	r.POST(a+"/images/{id}/approve", c(h.serverAdmin(h.decide(true))))
	r.POST(a+"/images/{id}/reject", c(h.serverAdmin(h.decide(false))))
	r.GET(a+"/quotas/{id}", c(h.serverAdmin(h.getQuota)))
	r.PUT(a+"/quotas/{id}", c(h.serverAdmin(h.setQuota)))
	r.GET(a+"/pool/assignments", c(h.assignments))
}

func (h *SharedPoolHandler) unavailable(ctx *fasthttp.RequestCtx) bool {
	if h.quota == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"quotas and the shared worker are part of the hosted platform (auth.mode: hosted)")
		return true
	}
	return false
}

// member admits a caller acting in an org with at least min.
func (h *SharedPoolHandler) member(min auth.Role, next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.unavailable(ctx) {
			return
		}
		p := auth.PrincipalFrom(ctx)
		if !p.IsAuthenticated() || p.OrgID == "" || p.Kind == auth.KindRunner {
			lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in first")
			return
		}
		if !p.Can(min) {
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "this needs the "+string(min)+" role in your organization")
			return
		}
		next(ctx)
	}
}

// serverAdmin admits only server admins (HEIMDALL_SERVER_ADMINS).
func (h *SharedPoolHandler) serverAdmin(next fasthttp.RequestHandler) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.unavailable(ctx) {
			return
		}
		check := h.isAdmin
		if check == nil {
			check = auth.IsServerAdmin
		}
		if !check(auth.PrincipalFrom(ctx)) {
			lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
				"server admins only: the server owner lists admin emails in "+auth.ServerAdminsEnv)
			return
		}
		next(ctx)
	}
}

func (h *SharedPoolHandler) record(ctx *fasthttp.RequestCtx, org, action, typ, id string, details map[string]any) {
	if h.audit != nil {
		h.audit(ctx, org, action, typ, id, details)
	}
}

func (h *SharedPoolHandler) internal(ctx *fasthttp.RequestCtx, what string, err error) {
	lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, what+": "+err.Error())
}

func (h *SharedPoolHandler) decode(ctx *fasthttp.RequestCtx, v any, optional bool) bool {
	body := bytes.TrimSpace(ctx.PostBody())
	if len(body) == 0 {
		if optional {
			return true
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "request body required")
		return false
	}
	if err := decodeJSONStrict(body, v); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse request: "+err.Error())
		return false
	}
	return true
}

func (h *SharedPoolHandler) status(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	org := auth.PrincipalFrom(ctx).OrgID
	l, err := h.quota.Get(work, org)
	if err != nil {
		h.internal(ctx, "read limits", err)
		return
	}
	kill, err := h.quota.KillSwitch(work)
	if err != nil {
		h.internal(ctx, "read kill switch", err)
		return
	}
	st := PoolStatus{SharedPoolConfigured: h.secret() != "" && h.pool != nil, KillSwitch: kill, Limits: l}
	switch {
	case kill:
		st.Message = "The server owner paused all new runs (kill switch). Running slots finish; new ones wait until it is lifted."
	case !st.SharedPoolConfigured:
		st.Message = "No shared worker on this server: start your own worker (Workers page) or ask your server owner to enable the shared worker."
	case !l.SharedPool:
		st.Message = "The shared worker is off for your organization: ask your server owner to enable it, or start your own worker."
	default:
		st.Message = "Your experiments run on the shared worker with the built-in agents and benchmarks."
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, st)
}

func (h *SharedPoolHandler) listOrgImages(ctx *fasthttp.RequestCtx) {
	if h.images == nil {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ImageRequestList{Images: []sharedpool.ImageRequest{}})
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	list, err := h.images.List(work, auth.PrincipalFrom(ctx).OrgID, "")
	if err != nil {
		h.internal(ctx, "list image requests", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ImageRequestList{Images: list})
}

func (h *SharedPoolHandler) requestImage(ctx *fasthttp.RequestCtx) {
	if h.images == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "custom images are not enabled on this server")
		return
	}
	var req ImageRequestBody
	if !h.decode(ctx, &req, false) {
		return
	}
	if _, _, err := sharedpool.CheckImageRef(req.Image); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			err.Error()+"; find it with `docker inspect --format '{{index .RepoDigests 0}}' <image>`")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	p := auth.PrincipalFrom(ctx)
	l, err := h.quota.Get(work, p.OrgID)
	if err != nil {
		h.internal(ctx, "read limits", err)
		return
	}
	if req.SizeBytes <= 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"size_bytes is required: the image size from `docker image inspect --format '{{.Size}}' <image>`")
		return
	}
	if err := sharedpool.CheckImageSize(req.SizeBytes, l); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, err.Error()+": use a smaller image or ask your server owner to raise the limit")
		return
	}
	v, err := h.images.Request(work, p.OrgID, strings.TrimSpace(req.Image), req.SizeBytes, req.Note, p.ActorID())
	if err != nil {
		if strings.Contains(err.Error(), "note must be") {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
			return
		}
		h.internal(ctx, "request image", err)
		return
	}
	h.record(ctx, p.OrgID, "image.request", "image_request", v.ID, map[string]any{"image": v.Image, "size_bytes": v.SizeBytes})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, v)
}

func (h *SharedPoolHandler) listAllImages(ctx *fasthttp.RequestCtx) {
	if h.images == nil {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ImageRequestList{Images: []sharedpool.ImageRequest{}})
		return
	}
	status := string(ctx.QueryArgs().Peek("status"))
	switch status {
	case "", sharedpool.StatusPending, sharedpool.StatusApproved, sharedpool.StatusRejected:
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "status must be pending, approved or rejected")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	list, err := h.images.List(work, "", status)
	if err != nil {
		h.internal(ctx, "list image requests", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, ImageRequestList{Images: list})
}

func (h *SharedPoolHandler) decide(approve bool) fasthttp.RequestHandler {
	return func(ctx *fasthttp.RequestCtx) {
		if h.images == nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "custom images are not enabled on this server")
			return
		}
		id, ok := idParam(ctx, h.mode, "id", "image request")
		if !ok {
			return
		}
		var body ImageDecisionBody
		if !h.decode(ctx, &body, true) {
			return
		}
		work, cancel := workContext(ctx, readTimeout)
		defer cancel()
		cur, err := h.images.Get(work, id)
		if errors.Is(err, sharedpool.ErrNotFound) {
			sendNotFound(ctx, h.mode, "image request", id)
			return
		}
		if err != nil {
			h.internal(ctx, "read image request", err)
			return
		}
		if approve { // re-check at approval: the org's limit may have dropped
			if _, _, err := sharedpool.CheckImageRef(cur.Image); err != nil {
				lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
				return
			}
			l, err := h.quota.Get(work, cur.OrgID)
			if err != nil {
				h.internal(ctx, "read limits", err)
				return
			}
			if err := sharedpool.CheckImageSize(cur.SizeBytes, l); err != nil {
				lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, err.Error()+": raise the org's image size limit first")
				return
			}
		}
		var limits quota.Limits
		if approve {
			if limits, err = h.quota.Get(work, cur.OrgID); err != nil {
				h.internal(ctx, "read limits", err)
				return
			}
		}
		v, err := h.images.Decide(work, id, approve, auth.PrincipalFrom(ctx).ActorID(), body.Note)
		switch {
		case errors.Is(err, sharedpool.ErrNotPending):
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "this request was already "+cur.Status)
			return
		case err != nil && strings.Contains(err.Error(), "note must be"):
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
			return
		case err != nil:
			h.internal(ctx, "decide image request", err)
			return
		}
		if approve && !limits.AllowCustomImages { // J6: approving an org's image lets it run custom images
			limits.AllowCustomImages = true
			if err := h.quota.Set(work, cur.OrgID, limits, auth.PrincipalFrom(ctx).ActorID()); err != nil {
				h.internal(ctx, "allow custom images", err)
				return
			}
		}
		h.record(ctx, v.OrgID, "image."+v.Status, "image_request", v.ID, map[string]any{"image": v.Image})
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, v)
	}
}

func (h *SharedPoolHandler) orgParam(ctx *fasthttp.RequestCtx, work context.Context) (string, bool) {
	id, ok := idParam(ctx, h.mode, "id", "organization")
	if !ok {
		return "", false
	}
	if h.orgExists != nil && !h.orgExists(work, id) {
		sendNotFound(ctx, h.mode, "organization", id)
		return "", false
	}
	return id, true
}

func (h *SharedPoolHandler) getQuota(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	id, ok := h.orgParam(ctx, work)
	if !ok {
		return
	}
	l, err := h.quota.Get(work, id)
	if err != nil {
		h.internal(ctx, "read limits", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, OrgQuota{OrgID: id, Limits: l})
}

func (h *SharedPoolHandler) setQuota(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	id, ok := h.orgParam(ctx, work)
	if !ok {
		return
	}
	var l quota.Limits
	if !h.decode(ctx, &l, false) {
		return
	}
	l = quota.Normalize(l)
	if err := h.quota.Set(work, id, l, auth.PrincipalFrom(ctx).ActorID()); err != nil {
		if errors.Is(err, quota.ErrInvalid) {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
			return
		}
		h.internal(ctx, "write limits", err)
		return
	}
	h.record(ctx, id, "quota.set", "org", id, map[string]any{"limits": l})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, OrgQuota{OrgID: id, Limits: l})
}

func (h *SharedPoolHandler) getKill(ctx *fasthttp.RequestCtx) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	on, err := h.quota.KillSwitch(work)
	if err != nil {
		h.internal(ctx, "read kill switch", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, KillSwitchBody{On: on})
}

func (h *SharedPoolHandler) setKill(ctx *fasthttp.RequestCtx) {
	var body KillSwitchBody
	if !h.decode(ctx, &body, false) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := h.quota.SetKillSwitch(work, body.On, auth.PrincipalFrom(ctx).ActorID()); err != nil {
		h.internal(ctx, "write kill switch", err)
		return
	}
	h.record(ctx, "", "platform.kill_switch", "platform", "kill_switch", map[string]any{"on": body.On})
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, body)
}

// assignments answers the shared runner. The policy is public: the handler
// checks the shared runner secret itself and the answer is never cached.
func (h *SharedPoolHandler) assignments(ctx *fasthttp.RequestCtx) {
	ctx.Response.Header.Set("Cache-Control", "no-store")
	presented, _ := strings.CutPrefix(string(ctx.Request.Header.Peek("Authorization")), "Bearer ")
	if err := sharedpool.CheckSecret(h.secret(), strings.TrimSpace(presented)); err != nil {
		if errors.Is(err, sharedpool.ErrDisabled) {
			lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, err.Error())
			return
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, err.Error())
		return
	}
	if h.pool == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, sharedpool.ErrDisabled.Error())
		return
	}
	runtime := string(ctx.QueryArgs().Peek("runtime"))
	if runtime != "" && runtime != quota.RuntimeRunc && runtime != quota.RuntimeRunsc {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "runtime must be runc or runsc")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.pool.Assignments(work, runtime)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "build assignments: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}
