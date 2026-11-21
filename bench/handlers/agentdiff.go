package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentdiff"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// AgentDiffRouteDoc and AgentDiffVersionsRouteDoc describe the agent diff
// routes for the OpenAPI table (openapi.go).
var (
	AgentDiffRouteDoc = RouteDoc{
		Method: "GET", Path: APIPrefix + "/agents/diff", Tag: "agents",
		Summary:  "What changed between two versions of an agent config and which tasks flipped, over verified sealed runs (docs/agent-diff.md)",
		Response: agentdiff.Report{},
		Query: []QueryDoc{
			{Name: "a", Description: "Version A: config hash (or a prefix of 8 or more characters) or <exp_id>:<arm>"},
			{Name: "b", Description: "Version B, same forms as a"},
			{Name: "agent", Description: "With a and b empty: compare this agent id's two newest config hashes"},
			{Name: "suite", Description: "Suite id@version; default the newest suite both versions ran"},
			{Name: "condition", Description: "Condition; default clean"},
			{Name: "synthetic", Description: "1 to let synthetic runs in; default 0"},
		},
	}
	AgentDiffVersionsRouteDoc = RouteDoc{
		Method: "GET", Path: APIPrefix + "/agents/diff/versions", Tag: "agents",
		Summary: "Every agent config hash with the experiment arms that ran it, for the diff picker", Response: agentdiff.VersionList{},
	}
)

// AgentDiffHandler serves the agent diff (docs/agent-diff.md): what changed
// between two versions of an agent config and which tasks flipped, over
// verified sealed runs only. It needs an index that verifies seals; without
// one it answers 503 rather than count runs it could not check.
type AgentDiffHandler struct {
	mode     schemas.Mode
	index    ReadIndex
	contents agentdiff.ContentSource
}

// NewAgentDiffHandler creates the agent diff handler. index may be nil.
// root is the bench root notes files are read from for text diffs; empty
// compares them by hash only.
func NewAgentDiffHandler(mode schemas.Mode, index ReadIndex, root string) *AgentDiffHandler {
	return &AgentDiffHandler{mode: mode, index: index, contents: agentdiff.RootContents(root)}
}

// RegisterRoutes registers GET /agents/diff and GET /agents/diff/versions.
func (h *AgentDiffHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/agents/diff", lib.ChainMiddlewares(h.getDiff, middlewares...))
	r.GET(APIPrefix+"/agents/diff/versions", lib.ChainMiddlewares(h.getVersions, middlewares...))
}

// getDiff handles GET
// /api/bench/agents/diff?a=&b=&suite=&condition=&agent=&synthetic=. a and b
// are config hashes (or prefixes of 8 or more characters) or <exp_id>:<arm>;
// with both empty, agent picks that agent's two newest config hashes.
// synthetic=1 (or true) lets synthetic runs in. A selection the diff cannot
// answer, including two versions with no common task, is a 400 that says why.
func (h *AgentDiffHandler) getDiff(ctx *fasthttp.RequestCtx) {
	src, ok := h.source(ctx)
	if !ok {
		return
	}
	args := ctx.QueryArgs()
	q := agentdiff.Query{
		A: string(args.Peek("a")), B: string(args.Peek("b")), Agent: string(args.Peek("agent")),
		Suite: string(args.Peek("suite")), Condition: string(args.Peek("condition")),
	}
	switch v := strings.TrimSpace(string(args.Peek("synthetic"))); v {
	case "", "0", "false", "exclude":
	case "1", "true", "include":
		q.Synthetic = true
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "synthetic "+v+" is not 1 or 0")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	report, err := agentdiff.Diff(work, src, h.contents, q)
	if errors.Is(err, agentdiff.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if err != nil {
		sendDepError(ctx, h.mode, "agent diff", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, report)
}

// getVersions handles GET /api/bench/agents/diff/versions: every config hash
// the manifests resolve, newest first, for the version picker.
func (h *AgentDiffHandler) getVersions(ctx *fasthttp.RequestCtx) {
	src, ok := h.source(ctx)
	if !ok {
		return
	}
	list := agentdiff.Versions(src)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &list)
}

// AgentDiffSource adapts a ReadIndex to the agent diff's Source, verdict
// decisions included when the index implements VerdictIndex. It returns
// false when the index cannot verify seals (SealVerifier): the diff counts
// verified runs only. The CLI uses it too, so both read the run folder the
// same way.
func AgentDiffSource(index ReadIndex) (agentdiff.Source, bool) {
	sv, ok := index.(SealVerifier)
	if !ok {
		return nil, false
	}
	return agentDiffSource{index: index, seals: sv}, true
}

// source adapts the index, or answers 503 when there is none or it cannot
// verify seals.
func (h *AgentDiffHandler) source(ctx *fasthttp.RequestCtx) (agentdiff.Source, bool) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return nil, false
	}
	src, ok := AgentDiffSource(h.index)
	if !ok {
		sendUnavailable(ctx, h.mode, "seal verification")
		return nil, false
	}
	return src, true
}

// agentDiffSource is the ReadIndex seen as an agentdiff.Source.
type agentDiffSource struct {
	index ReadIndex
	seals SealVerifier
}

// Experiments implements agentdiff.Source.
func (s agentDiffSource) Experiments() []string { return s.index.Experiments() }

// Manifest implements agentdiff.Source.
func (s agentDiffSource) Manifest(expID string) (*schemas.Manifest, bool) {
	return s.index.Manifest(expID)
}

// Runs implements agentdiff.Source.
func (s agentDiffSource) Runs(expID string) []agentdiff.Run {
	views := s.index.Runs(expID)
	out := make([]agentdiff.Run, 0, len(views))
	for _, rv := range views {
		out = append(out, agentdiff.Run{Slot: rv.Slot, Run: rv.Run, Sealed: rv.Sealed, Superseded: rv.Superseded})
	}
	return out
}

// RunFile implements agentdiff.Source. The diff reads metrics.json,
// gates.json and the proxy ledger (for the scripted model) internally; none
// of their content is served.
func (s agentDiffSource) RunFile(runID, name string) ([]byte, error) {
	return s.index.RunFile(runID, name)
}

// VerifySeal implements agentdiff.Source through the index's SealVerifier.
func (s agentDiffSource) VerifySeal(ctx context.Context, runID string) error {
	return s.seals.VerifySeal(ctx, runID)
}

// LatestDecision implements agentdiff.DecisionSource when the index knows
// verdicts; without it no experiment is left out for its verdict.
func (s agentDiffSource) LatestDecision(expID string) (schemas.Decision, bool) {
	if vi, ok := s.index.(VerdictIndex); ok {
		return vi.LatestDecision(expID)
	}
	return "", false
}
