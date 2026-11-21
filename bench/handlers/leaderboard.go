package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

// RegradeIndex is optionally implemented by a ReadIndex. It lists an
// experiment's regrades and reads its sealed runs with one as grade source,
// so the leaderboard can serve grade_source=latest. Without it every
// experiment reads its original grades.
type RegradeIndex interface {
	leaderboard.RegradeSource
}

// LeaderboardHandler serves the agent leaderboard: agent configurations
// ranked by verified success over every sealed run of one suite version
// under one condition, across experiments (docs/leaderboard.md).
type LeaderboardHandler struct {
	mode  schemas.Mode
	index ReadIndex
	now   func() time.Time

	// heldOutVisible serves the held-out board (split=held_out); false, the
	// default, refuses it, as the suites API refuses held-out task detail.
	heldOutVisible bool

	// root is the project folder, for the agent versions index read by
	// GET /leaderboard/agents; suites gives each benchmark's track. Both
	// optional.
	root   string
	suites SuiteSource
}

// WithAgentBoard sets the project root and suite source GET
// /leaderboard/agents reads versions and tracks from. Either may be empty.
func (h *LeaderboardHandler) WithAgentBoard(root string, suites SuiteSource) *LeaderboardHandler {
	h.root, h.suites = root, suites
	return h
}

// NewLeaderboardHandler creates the leaderboard handler. index may be nil.
func NewLeaderboardHandler(mode schemas.Mode, index ReadIndex) *LeaderboardHandler {
	return &LeaderboardHandler{mode: mode, index: index, now: time.Now}
}

// WithHeldOutVisible sets whether the held-out board is served (server
// config held_out.visible). The public board never counts a held-out run
// either way.
func (h *LeaderboardHandler) WithHeldOutVisible(visible bool) *LeaderboardHandler {
	h.heldOutVisible = visible
	return h
}

// refuseHeldOutBoard answers 404 for split=held_out unless held-out tasks are
// visible, and reports whether it did. Without the setting nothing about a
// held-out task leaves the server beyond its id and lock status, the board's
// per-task success rates and the experiments that ran it included.
func refuseHeldOutBoard(ctx *fasthttp.RequestCtx, mode schemas.Mode, visible bool, split string) bool {
	if visible || split != schemas.SplitHeldOut {
		return false
	}
	lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound,
		"the held-out board is not served (server config held_out.visible is false)")
	return true
}

// RegisterRoutes registers GET /leaderboard and GET /leaderboard/agents.
func (h *LeaderboardHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/leaderboard", lib.ChainMiddlewares(h.getLeaderboard, middlewares...))
	r.GET(APIPrefix+"/leaderboard/agents", lib.ChainMiddlewares(h.getAgentLeaderboard, middlewares...)) // A17: agent leaderboard (agentleaderboard.go)
}

// getLeaderboard handles GET
// /api/bench/leaderboard?suite=&condition=&scope=&synthetic=&grade_source=&k=.
// Absent parameters take the defaults: the newest suite with runs, clean,
// all, exclude, original, each entry's default k. A malformed one is a 400,
// as is a grade_source other than original or latest and a k that is not 2
// or more. A well-formed suite or condition with no runs is an empty board,
// not an error, so the picker can still list what exists.
func (h *LeaderboardHandler) getLeaderboard(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	args := ctx.QueryArgs()
	if refuseHeldOutBoard(ctx, h.mode, h.heldOutVisible, string(args.Peek("split"))) {
		return
	}
	k, err := queryK(string(args.Peek("k")))
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	q := leaderboard.Query{
		Suite:       string(args.Peek("suite")),
		Condition:   string(args.Peek("condition")),
		Scope:       string(args.Peek("scope")),
		Synthetic:   string(args.Peek("synthetic")),
		GradeSource: string(args.Peek("grade_source")),
		Split:       string(args.Peek("split")),     // Suite health area: held-out board (leaderboard/split.go)
		Customer:    string(args.Peek("customer")),  // simulated: the board of model-played customers (leaderboard/simuser.go)
		Simulator:   string(args.Peek("simulator")), // that board's simulator pin
		K:           k,
		Now:         h.now(),
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

// queryK parses the k parameter: absent is 0, each entry's default.
// leaderboard.Query.Normalize checks the range.
func queryK(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	k, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("k %q is not an integer", raw)
	}
	return k, nil
}

// LeaderboardSource adapts a ReadIndex to the leaderboard's Source. The
// verdict decisions come from the index when it implements VerdictIndex,
// and the regrades when it implements RegradeIndex. The CLI uses it too, so
// both read the run folder the same way.
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
// the API never serves: the board only looks in it for the scripted model,
// by the ledger's upstream label or, on older runs, its fingerprint.
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

// Regrades implements leaderboard.RegradeSource. An index without
// RegradeIndex has no regrades.
func (s leaderboardSource) Regrades(ctx context.Context, expID string) ([]leaderboard.Regrade, error) {
	if ri, ok := s.index.(RegradeIndex); ok {
		return ri.Regrades(ctx, expID)
	}
	return nil, nil
}

// VerifySeal implements leaderboard.SealSource through the index's
// SealVerifier. An index without one cannot verify and trusts the seal.
func (s leaderboardSource) VerifySeal(ctx context.Context, runID string) error {
	if sv, ok := s.index.(SealVerifier); ok {
		return sv.VerifySeal(ctx, runID)
	}
	return nil
}

// RegradedRuns implements leaderboard.RegradeSource. It is only asked for a
// regrade Regrades listed, so an index without RegradeIndex is never asked.
func (s leaderboardSource) RegradedRuns(ctx context.Context, expID, regradeID string) ([]schemas.SealedRun, error) {
	if ri, ok := s.index.(RegradeIndex); ok {
		return ri.RegradedRuns(ctx, expID, regradeID)
	}
	return nil, fmt.Errorf("regrade %s of experiment %s: %w", regradeID, expID, schemas.ErrNotFound)
}
