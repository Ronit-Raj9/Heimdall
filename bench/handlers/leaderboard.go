package handlers

import (
	"errors"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// VerdictIndex is optionally implemented by a ReadIndex. LatestDecision
// returns the verdict decision of the experiment's newest analysis, so the
// leaderboard can leave out experiments whose verdict is invalid. Without
// it no experiment is left out for its verdict.
type VerdictIndex interface {
	LatestDecision(expID string) (schemas.Decision, bool)
}

// LeaderboardHandler serves the agent leaderboard: agent configurations
// ranked by verified success over every sealed run of one suite version
// under one condition, across experiments (docs/leaderboard.md).
type LeaderboardHandler struct {
	mode  schemas.Mode
	index ReadIndex
	now   func() time.Time
}

// NewLeaderboardHandler creates the leaderboard handler. index may be nil.
func NewLeaderboardHandler(mode schemas.Mode, index ReadIndex) *LeaderboardHandler {
	return &LeaderboardHandler{mode: mode, index: index, now: time.Now}
}

// RegisterRoutes registers GET /leaderboard.
func (h *LeaderboardHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/leaderboard", lib.ChainMiddlewares(h.getLeaderboard, middlewares...))
}

// getLeaderboard handles GET /api/bench/leaderboard?suite=&condition=&scope=&synthetic=.
// Absent parameters take the defaults: the newest suite with runs, clean,
// all, exclude. A malformed one is a 400. A well-formed suite or condition
// with no runs is an empty board, not an error, so the picker can still
// list what exists.
func (h *LeaderboardHandler) getLeaderboard(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	args := ctx.QueryArgs()
	q := leaderboard.Query{
		Suite:     string(args.Peek("suite")),
		Condition: string(args.Peek("condition")),
		Scope:     string(args.Peek("scope")),
		Synthetic: string(args.Peek("synthetic")),
		Now:       h.now(),
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	lb, err := leaderboard.Build(work, LeaderboardSource(h.index), q)
	if errors.Is(err, leaderboard.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "leaderboard", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, lb)
}

// LeaderboardSource adapts a ReadIndex to the leaderboard's Source. The
// verdict decisions come from the index when it implements VerdictIndex.
// The CLI uses it too, so both read the run folder the same way.
func LeaderboardSource(index ReadIndex) leaderboard.Source {
	return leaderboardSource{index: index}
}

// leaderboardSource is the ReadIndex seen as a leaderboard.Source.
type leaderboardSource struct {
	index ReadIndex
}

// Experiments implements leaderboard.Source.
func (s leaderboardSource) Experiments() []string { return s.index.Experiments() }

// Manifest implements leaderboard.Source.
func (s leaderboardSource) Manifest(expID string) (*schemas.Manifest, bool) {
	return s.index.Manifest(expID)
}

// Runs implements leaderboard.Source.
func (s leaderboardSource) Runs(expID string) []leaderboard.Run {
	views := s.index.Runs(expID)
	out := make([]leaderboard.Run, 0, len(views))
	for _, rv := range views {
		out = append(out, leaderboard.Run{Slot: rv.Slot, Run: rv.Run, Sealed: rv.Sealed})
	}
	return out
}

// RunFile implements leaderboard.Source. It reads the proxy ledger, which
// the API never serves: the board only looks for the scripted model's
// fingerprint in it.
func (s leaderboardSource) RunFile(runID, name string) ([]byte, error) {
	return s.index.RunFile(runID, name)
}

// LatestDecision implements leaderboard.Source.
func (s leaderboardSource) LatestDecision(expID string) (schemas.Decision, bool) {
	if vi, ok := s.index.(VerdictIndex); ok {
		return vi.LatestDecision(expID)
	}
	return "", false
}
