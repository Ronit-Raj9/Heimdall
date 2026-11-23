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

// MigrationPolicySource is a MigrationSource that can also build the report
// under a release rule set named in the request (the policy query): a file
// stem from the project's policies/ folder, "none" or "" for the
// comparison's own choice. A name that is not a rule set file wraps
// ErrNoRuleSet.
type MigrationPolicySource interface {
	MigrationWithPolicy(ctx context.Context, expID, gradeSource, policy string, q migration.Query) (*migration.Report, error)
}

// ErrNoRuleSet is a policy query that names no release rule set file.
var ErrNoRuleSet = errors.New("no release rule set")

// ValidRuleSetName says name can be a rule set file stem: letters, digits,
// dots, dashes and underscores, no path and no leading dot.
func ValidRuleSetName(name string) bool {
	if name == "" || len(name) > 100 || name[0] == '.' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return false
		}
	}
	return true
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
// condition, alpha, grade_source and policy (a rule set file stem or none;
// default the comparison's own choice).
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
		// An explicit alpha must be usable as given: zero would silently
		// become the default (migration.Build), so the report would not be
		// the one asked for (QC2-27-07).
		if !(a > 0 && a < 1) {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("alpha %q is not in (0, 1)", raw))
			return
		}
		q.Alpha = a
	}
	gradeSource := string(args.Peek("grade_source"))
	if gradeSource == "" {
		gradeSource = schemas.GradeSourceOriginal
	}
	ruleSet := strings.TrimSpace(string(args.Peek("policy")))
	noRuleSet := func() {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("No release rule set named %s.", ruleSet))
	}
	ps, withPolicy := h.source.(MigrationPolicySource)
	if ruleSet != "" && (!withPolicy || !ValidRuleSetName(ruleSet)) {
		noRuleSet()
		return
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	var rep *migration.Report
	var err error
	if ruleSet != "" {
		rep, err = ps.MigrationWithPolicy(work, id, gradeSource, ruleSet, q)
	} else {
		rep, err = h.source.Migration(work, id, gradeSource, q)
	}
	if errors.Is(err, ErrNoRuleSet) {
		noRuleSet()
		return
	}
	if errors.Is(err, migration.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "migration report", err)
		return
	}
	if rep.GradeSource == "" {
		rep.GradeSource = gradeSource
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
}
