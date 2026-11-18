package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// W8 judge calibration (docs/judge-calibration.md).

// CalibrationSource reads a judged suite's calibration state and labels and
// appends a label. A label is a row appended to the suite's labels.jsonl;
// nothing is ever replaced. Running the judge on the labels costs money and
// stays on the command line (`judge calibrate`), behind a forecast and a
// confirmation.
type CalibrationSource interface {
	CalibrationStatus(ctx context.Context, suiteID string) (*schemas.CalibrationStatus, error)
	CalibrationLabels(ctx context.Context, suiteID string) (*schemas.CalibrationLabels, error)
	AddCalibrationLabel(ctx context.Context, suiteID string, req *schemas.CalibrationLabelRequest) (*schemas.CalibrationItem, error)
}

// CalibrationHandler serves GET /suites/{id}/calibration, GET
// /suites/{id}/calibration/labels and POST /suites/{id}/calibration/labels.
type CalibrationHandler struct {
	mode  schemas.Mode
	src   CalibrationSource
	write lib.Middleware
}

// NewCalibrationHandler creates the handler. write guards the POST route;
// src may be nil, and every route then answers 503.
func NewCalibrationHandler(mode schemas.Mode, src CalibrationSource, write lib.Middleware) *CalibrationHandler {
	return &CalibrationHandler{mode: mode, src: src, write: write}
}

// RegisterRoutes registers the calibration routes.
func (h *CalibrationHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	r.GET(APIPrefix+"/suites/{id}/calibration", lib.ChainMiddlewares(h.getStatus, middlewares...))
	r.GET(APIPrefix+"/suites/{id}/calibration/labels", lib.ChainMiddlewares(h.getLabels, middlewares...))
	r.POST(APIPrefix+"/suites/{id}/calibration/labels", lib.ChainMiddlewares(h.postLabel, writeMW...))
}

// getStatus handles GET /api/bench/suites/{id}/calibration: the card's
// data (state, agreement, kappa, false-positive bound, set size, date,
// reminders).
func (h *CalibrationHandler) getStatus(ctx *fasthttp.RequestCtx) {
	id, ok := h.suiteID(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	st, err := h.src.CalibrationStatus(work, id)
	if err != nil {
		h.sendError(ctx, "calibration of suite "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, st)
}

// getLabels handles GET /api/bench/suites/{id}/calibration/labels: every
// row of labels.jsonl, oldest first.
func (h *CalibrationHandler) getLabels(ctx *fasthttp.RequestCtx) {
	id, ok := h.suiteID(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	l, err := h.src.CalibrationLabels(work, id)
	if err != nil {
		h.sendError(ctx, "calibration labels of suite "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, l)
}

// postLabel handles POST /api/bench/suites/{id}/calibration/labels: a
// CalibrationLabelRequest in, the appended item out with 201. It is a
// write, so it needs the write token, and it is refused with 403 outside
// live mode, like a review.
func (h *CalibrationHandler) postLabel(ctx *fasthttp.RequestCtx) {
	id, ok := h.suiteID(ctx)
	if !ok {
		return
	}
	if h.mode != schemas.ModeLive {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"calibration labels are refused in "+string(h.mode)+" mode")
		return
	}
	var req schemas.CalibrationLabelRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "parse label: "+err.Error())
		return
	}
	switch {
	case strings.TrimSpace(req.Task) == "":
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "task is required")
		return
	case strings.TrimSpace(req.Labeller) == "":
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "labeller is required")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	it, err := h.src.AddCalibrationLabel(work, id, &req)
	if err != nil {
		h.sendError(ctx, "label for suite "+id, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, it)
}

// suiteID reads the {id} path parameter, answering 503 without a source
// and 400 for a malformed id.
func (h *CalibrationHandler) suiteID(ctx *fasthttp.RequestCtx) (string, bool) {
	if h.src == nil {
		sendUnavailable(ctx, h.mode, "judge calibration")
		return "", false
	}
	id, _ := ctx.UserValue("id").(string)
	if !validID(id) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "suite id is not valid")
		return "", false
	}
	return id, true
}

// sendError maps a source error: a bad label 400, an unknown suite 404,
// anything else 500.
func (h *CalibrationHandler) sendError(ctx *fasthttp.RequestCtx, what string, err error) {
	if errors.Is(err, calibration.ErrInput) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
		return
	}
	sendDepError(ctx, h.mode, what, err)
}
