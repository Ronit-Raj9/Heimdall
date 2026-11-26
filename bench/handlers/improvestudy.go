package handlers

import (
	"context"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/improve"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/valyala/fasthttp"
)

// Improvement studies (D5 A4.1): the whole loop run several times from
// version 0, tested together on one shared held-out split.

// ImproveStudySource creates, reads and stops studies. The improvement
// source implements it when the server can run studies.
type ImproveStudySource interface {
	CreateStudy(ctx context.Context, spec *improve.Spec, actor string) (*improve.StudyView, error)
	ListStudies(ctx context.Context) (*improve.StudyList, error)
	GetStudy(ctx context.Context, id string) (*improve.StudyView, error)
	StopStudy(ctx context.Context, id, actor string) (*improve.StudyView, error)
}

// RegisterStudyRoutes registers the study routes on the improvement handler.
func (h *ImproveHandler) RegisterStudyRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(append([]lib.Middleware(nil), middlewares...), h.write)
	r.POST(APIPrefix+"/improvement-studies", lib.ChainMiddlewares(h.postStudy, writeMW...))
	r.GET(APIPrefix+"/improvement-studies", lib.ChainMiddlewares(h.getStudies, middlewares...))
	r.GET(APIPrefix+"/improvement-studies/{id}", lib.ChainMiddlewares(h.getStudy, middlewares...))
	r.POST(APIPrefix+"/improvement-studies/{id}/stop", lib.ChainMiddlewares(h.postStudyStop, writeMW...))
}

func (h *ImproveHandler) studies(ctx *fasthttp.RequestCtx) (ImproveStudySource, bool) {
	if !h.ready(ctx) {
		return nil, false
	}
	src, ok := h.src.(ImproveStudySource)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusNotImplemented, lib.ErrTypeUnavailable, "This server cannot run improvement studies.")
		return nil, false
	}
	return src, true
}

func (h *ImproveHandler) studyID(ctx *fasthttp.RequestCtx) (string, bool) {
	id := pathParam(ctx, "id")
	if !improve.ValidStudyID(id) {
		sendNotFound(ctx, h.mode, "study", id)
		return "", false
	}
	return id, true
}

// postStudy handles POST /api/bench/improvement-studies (202).
func (h *ImproveHandler) postStudy(ctx *fasthttp.RequestCtx) {
	src, ok := h.studies(ctx)
	if !ok {
		return
	}
	spec, err := improve.ParseSpec(ctx.PostBody())
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if spec.Replicate.Loops < 2 || spec.Replicate.Loops > improve.MaxLoops {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "A study needs replicate loops 2 to 5.")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	s, err := src.CreateStudy(work, spec, actorOf(ctx))
	if err != nil {
		h.sendW4Err(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, s)
}

// getStudies handles GET /api/bench/improvement-studies.
func (h *ImproveHandler) getStudies(ctx *fasthttp.RequestCtx) {
	src, ok := h.studies(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	l, err := src.ListStudies(work)
	if err != nil {
		h.sendErr(ctx, "studies", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, l)
}

// getStudy handles GET /api/bench/improvement-studies/{id}.
func (h *ImproveHandler) getStudy(ctx *fasthttp.RequestCtx) {
	id, ok := h.studyID(ctx)
	if !ok {
		return
	}
	src, ok := h.studies(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	s, err := src.GetStudy(work, id)
	if err != nil {
		h.sendErr(ctx, "study", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, s)
}

// postStudyStop handles POST /api/bench/improvement-studies/{id}/stop.
func (h *ImproveHandler) postStudyStop(ctx *fasthttp.RequestCtx) {
	id, ok := h.studyID(ctx)
	if !ok {
		return
	}
	src, ok := h.studies(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	s, err := src.StopStudy(work, id, actorOf(ctx))
	if err != nil {
		h.sendW4Err(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, s)
}
