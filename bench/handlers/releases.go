package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// ReleaseLog keeps the hash-chained release log and its evidence packs.
// cmd/bifrost-bench satisfies it with the release package.
type ReleaseLog interface {
	// Create records a release or refuses it. A refusal on the evidence (the
	// verdict, the policy, a seal) is an error implementing releaseRefusal.
	Create(ctx context.Context, req *schemas.ReleaseRequest) (*schemas.ReleaseRecord, error)
	List(ctx context.Context) (*schemas.ReleaseList, error)
	Get(ctx context.Context, releaseID string) (*schemas.ReleaseRecord, error)
	// Evidence returns a release's evidence pack, checked against its record.
	Evidence(ctx context.Context, releaseID string) ([]byte, error)
}

// releaseRefusal marks an error as a release refused on its evidence. The
// release package's error implements it, so handlers need not import it.
type releaseRefusal interface {
	ReleaseRefused() bool
}

// ReleasesHandler serves the release log: create, list, one record and its
// evidence pack. A release is a hash-chained record, not a signature.
type ReleasesHandler struct {
	mode     schemas.Mode
	releases ReleaseLog
	write    lib.Middleware
}

// NewReleasesHandler creates the releases handler. write guards the POST
// route; releases may be nil.
func NewReleasesHandler(mode schemas.Mode, releases ReleaseLog, write lib.Middleware) *ReleasesHandler {
	return &ReleasesHandler{mode: mode, releases: releases, write: write}
}

// RegisterRoutes registers the release routes.
func (h *ReleasesHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	p := APIPrefix + "/releases"
	r.POST(p, lib.ChainMiddlewares(h.createRelease, writeMW...))
	r.GET(p, lib.ChainMiddlewares(h.listReleases, middlewares...))
	r.GET(p+"/{id}", lib.ChainMiddlewares(h.getRelease, middlewares...))
	r.GET(p+"/{id}/evidence", lib.ChainMiddlewares(h.getEvidence, middlewares...))
}

// createRelease handles POST /api/bench/releases: a schemas.ReleaseRequest
// in, the record out with 201. A refusal is 409 with the reason.
func (h *ReleasesHandler) createRelease(ctx *fasthttp.RequestCtx) {
	if h.releases == nil {
		sendUnavailable(ctx, h.mode, "release log")
		return
	}
	var req schemas.ReleaseRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse release request: "+err.Error())
		return
	}
	switch {
	case !validID(req.ExpID):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "exp_id is required")
		return
	case req.AnalysisID != "" && !validID(req.AnalysisID):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "analysis_id is not a valid id")
		return
	}
	if isLocalCaller(ctx) && strings.TrimSpace(req.Approver) == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "approver is required")
		return
	}
	// Only an owner or admin may approve a release (audit N8). The policy
	// table says so for hosted routes; this check holds even if a route is
	// mounted without it. Local mode runs as owner.
	if !isLocalCaller(ctx) && !auth.PrincipalFrom(ctx).Can(auth.RoleAdmin) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only an org owner or admin may approve a release")
		return
	}
	// The approver is the signed-in caller; a body value is accepted for
	// compatibility and ignored outside local mode.
	approver, ok, why := signedActor(ctx, req.Approver)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, why)
		return
	}
	req.Approver = approver
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	rec, err := h.releases.Create(work, &req)
	if err != nil {
		var refused releaseRefusal
		if errors.As(err, &refused) && refused.ReleaseRefused() {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
			return
		}
		sendDepError(ctx, h.mode, "release "+req.ExpID, err)
		return
	}
	tenancy.SetCreated(ctx, rec.ReleaseID)
	out := *rec
	redactRelease(&out)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, &out)
}

// listReleases handles GET /api/bench/releases: every record in log order,
// oldest first, and whether the chain verifies.
func (h *ReleasesHandler) listReleases(ctx *fasthttp.RequestCtx) {
	if h.releases == nil {
		sendUnavailable(ctx, h.mode, "release log")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	list, err := h.releases.List(work)
	if err != nil {
		sendDepError(ctx, h.mode, "list releases", err)
		return
	}
	if list.Releases == nil {
		list.Releases = []schemas.ReleaseRecord{}
	}
	// Approvers are shown by name only, never an email or an id (K3).
	shown := *list
	shown.Releases = make([]schemas.ReleaseRecord, len(list.Releases))
	for i := range list.Releases {
		shown.Releases[i] = list.Releases[i]
		redactRelease(&shown.Releases[i])
	}
	list = &shown
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, list)
}

// getRelease handles GET /api/bench/releases/{id}.
func (h *ReleasesHandler) getRelease(ctx *fasthttp.RequestCtx) {
	if h.releases == nil {
		sendUnavailable(ctx, h.mode, "release log")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "release")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	rec, err := h.releases.Get(work, id)
	if err != nil {
		sendDepError(ctx, h.mode, "release "+id, err)
		return
	}
	out := *rec
	redactRelease(&out)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// getEvidence handles GET /api/bench/releases/{id}/evidence: the evidence
// pack as a zip attachment. A pack that no longer matches its record is an
// error, never served.
func (h *ReleasesHandler) getEvidence(ctx *fasthttp.RequestCtx) {
	if h.releases == nil {
		sendUnavailable(ctx, h.mode, "release log")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "release")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	b, err := h.releases.Evidence(work, id)
	if err != nil {
		sendDepError(ctx, h.mode, "evidence of release "+id, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("application/zip")
	ctx.Response.Header.Set(fasthttp.HeaderContentDisposition, `attachment; filename="`+id+`-evidence.zip"`)
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.SetBody(b)
}
