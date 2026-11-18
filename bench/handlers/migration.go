package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/migration"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// MigrationSource writes the model migration report of a sealed matrix
// experiment (docs/model-migration.md). cmd/bifrost-bench implements it
// over the store's verified sealed runs, the suite and the release policy.
type MigrationSource interface {
	// Migration builds the report. An unknown experiment wraps
	// schemas.ErrNotFound, a bad query migration.ErrBadQuery.
	Migration(ctx context.Context, expID, gradeSource string, q migration.Query) (*migration.Report, error)
}

// MigrationHandler serves the migration report.
type MigrationHandler struct {
	mode   schemas.Mode
	source MigrationSource
}

// NewMigrationHandler creates the handler; a nil source answers 503.
func NewMigrationHandler(mode schemas.Mode, source MigrationSource) *MigrationHandler {
	return &MigrationHandler{mode: mode, source: source}
}

// RegisterRoutes registers GET /experiments/{id}/migration.
func (h *MigrationHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/experiments/{id}/migration", lib.ChainMiddlewares(h.getMigration, middlewares...))
}

// getMigration handles GET /experiments/{id}/migration with the query
// parameters from (model or arm; default arm A), to (comma-separated),
// condition, alpha and grade_source.
func (h *MigrationHandler) getMigration(ctx *fasthttp.RequestCtx) {
	if h.source == nil {
		sendUnavailable(ctx, h.mode, "migration report")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	args := ctx.QueryArgs()
	q := migration.Query{From: string(args.Peek("from")), Condition: string(args.Peek("condition"))}
	for _, t := range strings.Split(string(args.Peek("to")), ",") {
		if t = strings.TrimSpace(t); t != "" {
			q.To = append(q.To, t)
		}
	}
	if raw := strings.TrimSpace(string(args.Peek("alpha"))); raw != "" {
		a, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("alpha %q is not a number", raw))
			return
		}
		q.Alpha = a
	}
	gradeSource := string(args.Peek("grade_source"))
	if gradeSource == "" {
		gradeSource = schemas.GradeSourceOriginal
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	rep, err := h.source.Migration(work, id, gradeSource, q)
	if errors.Is(err, migration.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "migration report", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
}
