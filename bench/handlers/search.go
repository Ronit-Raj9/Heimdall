package handlers

// Cross-run trace search (X23): GET /search finds sealed runs by the text
// of their steps, tool calls, final answer, gate reasons and grade reason,
// with agent, version, benchmark and outcome filters
// (resultsindex/search.go). It reads the org's sealed runs through the read
// index; held-out tasks are never searched, so their transcripts cannot
// leak through a snippet.

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strconv"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/resultsindex"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

type searchHandler struct {
	env OrgEnv
	ix  *resultsindex.SearchIndex
}

func init() {
	RegisterOrgArea("search", func(env OrgEnv, r *router.Router) {
		h := &searchHandler{env: env, ix: resultsindex.NewSearchIndex()}
		r.GET(APIPrefix+"/search", env.R(h.search))
	})
}

// search handles GET /api/bench/search?q=&agent=&version=&benchmark=&outcome=&limit=.
func (h *searchHandler) search(ctx *fasthttp.RequestCtx) {
	if h.env.Services.Index == nil {
		sendUnavailable(ctx, h.env.Mode, "run index")
		return
	}
	args := ctx.QueryArgs()
	limit := 0
	if raw := string(args.Peek("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "limit must be a positive number")
			return
		}
		limit = n
	}
	filters := map[string]string{}
	for _, k := range []string{"agent", "version", "benchmark", "outcome"} {
		filters[k] = string(args.Peek(k))
	}
	q, err := resultsindex.ParseSearchQuery(string(args.Peek("q")), filters, limit)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	root := ""
	if h.env.Cfg != nil {
		root = h.env.Cfg.Root
	}
	res, err := h.ix.Search(work, indexSearchSource{ix: h.env.Services.Index, root: root}, q)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "search took too long; add a filter or a tool: term")
			return
		}
		sendDepError(ctx, h.env.Mode, "search", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, res)
}

// indexSearchSource lists the read index's sealed runs of public tasks.
// root is the org project root, where the agents' saved versions live.
type indexSearchSource struct {
	ix   ReadIndex
	root string
}

func (s indexSearchSource) SearchRuns(ctx context.Context) ([]resultsindex.SearchRun, error) {
	var out []resultsindex.SearchRun
	versions := newVersionIndex(s.root, suite.LibraryDir())
	for _, exp := range s.ix.Experiments() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, ok := s.ix.Manifest(exp)
		if !ok || m == nil {
			continue
		}
		armVersion := searchArmVersions(versions, m)
		for _, rv := range s.ix.Runs(exp) {
			if !rv.Sealed || slices.Contains(m.HeldOutTasks, rv.Slot.Task) {
				continue
			}
			r := resultsindex.SearchRun{RunID: rv.Run.RunID, Experiment: exp, Benchmark: cmp.Or(m.SuiteID, m.Experiment.Suite), Task: rv.Slot.Task,
				Agent: rv.Run.Agent.ConfigID, Version: rv.Run.Agent.AgentVersion, Outcome: string(rv.Run.Outcome), Started: rv.Run.Timing.Started,
				SealHash: rv.SealHash}
			if arm, ok := m.Arms[rv.Slot.Arm]; ok {
				r.Agent, r.Model = cmp.Or(arm.Config.ID, r.Agent), arm.Config.Model
			}
			if v := armVersion[rv.Slot.Arm]; v != "" {
				r.Version = v
			}
			out = append(out, r)
		}
	}
	return out, nil
}

// searchArmVersions is each arm's saved agent version, "v{N}", as the
// agent's Versions tab and the run page show it (N291): the version the
// evaluation recorded for the arm, else the saved version whose agent hash
// matches the arm's config. An arm with neither is left out, and its runs
// keep the config's own agent_version.
func searchArmVersions(v *versionIndex, m *schemas.Manifest) map[string]string {
	out := map[string]string{}
	for id, arm := range m.Arms {
		n, ok := v.fromEvaluation(m, id)
		if !ok {
			if ah, err := agentversions.AgentHash(arm.Config); err == nil {
				n = v.lookup(arm.Config.ID, ah)
			}
		}
		if n > 0 {
			out[id] = "v" + strconv.Itoa(n)
		}
	}
	return out
}

func (s indexSearchSource) RunFile(runID, name string) ([]byte, error) {
	return s.ix.RunFile(runID, name)
}

// SearchRouteDoc describes GET /search for the OpenAPI document.
var SearchRouteDoc = RouteDoc{
	Method: "GET", Path: APIPrefix + "/search", Tag: "runs",
	Summary:  "Search sealed runs by the text of their steps, tool calls and their arguments, trace events, model error messages, answer or final message, gate reasons and grade reason (words, \"phrases\", tool:NAME; a plain word that is a tool name also matches like tool:)",
	Response: resultsindex.SearchResult{},
	Query: []QueryDoc{
		{Name: "q", Description: "Words (all must match; a word that is a called tool's name matches that tool), \"quoted phrases\", tool:NAME; agent:, version:, benchmark:, outcome: also accepted inline"},
		{Name: "agent", Description: "Agent config id"},
		{Name: "version", Description: "Agent version as the agent's Versions tab shows it; v1, V1 and 1 are the same"},
		{Name: "benchmark", Description: "Benchmark (suite) id; a version suffix is ignored"},
		{Name: "outcome", Description: "Run outcome, e.g. fail"},
		{Name: "limit", Description: "Hits returned, default 50, at most 200"},
	},
}
