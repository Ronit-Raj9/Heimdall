package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// ReviewSource reads and writes human reviews of sealed runs
// (store/review.go). A review is a child artifact beside the sealed run and
// never edits it. Implementations find a run's experiment from its id.
type ReviewSource interface {
	ReviewQueue(ctx context.Context, expID, reason string) (*schemas.ReviewQueue, error)
	ReviewLog(ctx context.Context, expID string) (*schemas.ReviewLog, error)
	RunReviews(ctx context.Context, runID string) (*schemas.RunReviews, error)
	AddReview(ctx context.Context, req *schemas.ReviewRequest) (*schemas.Review, error)
}

// ReviewsHandler serves the review queue, the review history of a run and
// of an experiment, and records new reviews.
type ReviewsHandler struct {
	mode    schemas.Mode
	reviews ReviewSource
	write   lib.Middleware
}

// NewReviewsHandler creates the reviews handler. write guards the POST
// route; reviews may be nil, and its routes then answer 503.
func NewReviewsHandler(mode schemas.Mode, reviews ReviewSource, write lib.Middleware) *ReviewsHandler {
	return &ReviewsHandler{mode: mode, reviews: reviews, write: write}
}

// RegisterRoutes registers GET /experiments/{id}/reviews/queue, GET
// /experiments/{id}/reviews, GET /runs/{id}/reviews and POST /reviews.
func (h *ReviewsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	r.GET(APIPrefix+"/experiments/{id}/reviews/queue", lib.ChainMiddlewares(h.getQueue, middlewares...))
	r.GET(APIPrefix+"/experiments/{id}/reviews", lib.ChainMiddlewares(h.getLog, middlewares...))
	r.GET(APIPrefix+"/runs/{id}/reviews", lib.ChainMiddlewares(h.getRunReviews, middlewares...))
	r.POST(APIPrefix+"/reviews", lib.ChainMiddlewares(h.postReview, writeMW...))
}

// getQueue handles GET /api/bench/experiments/{id}/reviews/queue?reason=:
// the runs that need a person to look at them.
func (h *ReviewsHandler) getQueue(ctx *fasthttp.RequestCtx) {
	expID, ok := h.pathID(ctx, "experiment")
	if !ok {
		return
	}
	reason := strings.TrimSpace(string(ctx.QueryArgs().Peek("reason")))
	if reason != "" && !slices.Contains(schemas.ReviewReasons, reason) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			"reason must be one of "+strings.Join(schemas.ReviewReasons, ", "))
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	q, err := h.reviews.ReviewQueue(work, expID, reason)
	if err != nil {
		h.sendReviewError(ctx, "review queue of "+expID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, q)
}

// getLog handles GET /api/bench/experiments/{id}/reviews: the whole review
// chain of the experiment and whether it verifies.
func (h *ReviewsHandler) getLog(ctx *fasthttp.RequestCtx) {
	expID, ok := h.pathID(ctx, "experiment")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	l, err := h.reviews.ReviewLog(work, expID)
	if err != nil {
		h.sendReviewError(ctx, "reviews of "+expID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, l)
}

// getRunReviews handles GET /api/bench/runs/{id}/reviews: every review of
// the run, oldest first, and the one in effect.
func (h *ReviewsHandler) getRunReviews(ctx *fasthttp.RequestCtx) {
	runID, ok := h.pathID(ctx, "run")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	rr, err := h.reviews.RunReviews(work, runID)
	if err != nil {
		h.sendReviewError(ctx, "reviews of run "+runID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rr)
}

// postReview handles POST /api/bench/reviews: a schemas.ReviewRequest in,
// the recorded review out with 201. It is a write, so it needs the write
// token like every POST route, and it is refused with 403 unless the server
// runs in live mode: a recorded or synthetic run folder is a fixture whose
// results must not move. There is no user system; the reviewer name is
// whatever the request says.
func (h *ReviewsHandler) postReview(ctx *fasthttp.RequestCtx) {
	if h.reviews == nil {
		sendUnavailable(ctx, h.mode, "reviews")
		return
	}
	if h.mode != schemas.ModeLive {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"reviews are refused in "+string(h.mode)+" mode: the run folder is a fixture, not live results")
		return
	}
	var req schemas.ReviewRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse review request: "+err.Error())
		return
	}
	if msg := reviewRequestProblem(&req); msg != "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	rv, err := h.reviews.AddReview(work, &req)
	if err != nil {
		h.sendReviewError(ctx, "review of run "+req.RunID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, rv)
}

// pathID reads and checks the {id} path parameter, answering 503 when the
// handler has no review source and 400 for a malformed id.
func (h *ReviewsHandler) pathID(ctx *fasthttp.RequestCtx, what string) (string, bool) {
	if h.reviews == nil {
		sendUnavailable(ctx, h.mode, "reviews")
		return "", false
	}
	id, _ := ctx.UserValue("id").(string)
	if !validID(id) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+" id is not valid")
		return "", false
	}
	return id, true
}

// sendReviewError maps a review source error: bad input 400, an unknown
// run or experiment 404, a run whose seal does not verify or a review id
// that exists 409, anything else 500.
func (h *ReviewsHandler) sendReviewError(ctx *fasthttp.RequestCtx, what string, err error) {
	switch {
	case errors.Is(err, schemas.ErrReviewInput):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, schemas.ErrSealMismatch):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": "+err.Error())
	default:
		sendDepError(ctx, h.mode, what, err)
	}
}

// reviewRequestProblem names the first required field a review request
// lacks, or "" when the request can go to the store, which checks it fully.
func reviewRequestProblem(req *schemas.ReviewRequest) string {
	switch {
	case !validID(req.RunID):
		return "run_id is required"
	case req.ExpID != "" && !validID(req.ExpID):
		return "exp_id is not a valid id"
	case !req.Decision.Valid():
		return "decision must be pass, fail, invalid or flag_only"
	case strings.TrimSpace(req.Reason) == "":
		return "reason is required"
	case strings.TrimSpace(req.Reviewer) == "":
		return "reviewer is required"
	}
	return ""
}
