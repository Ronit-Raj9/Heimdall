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
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/resultsindex"
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
	res, err := h.ix.Search(work, indexSearchSource{h.env.Services.Index}, q)
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
type indexSearchSource struct{ ix ReadIndex }

func (s indexSearchSource) SearchRuns(ctx context.Context) ([]resultsindex.SearchRun, error) {
	var out []resultsindex.SearchRun
	for _, exp := range s.ix.Experiments() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, ok := s.ix.Manifest(exp)
		if !ok || m == nil {
			continue
		}
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
			out = append(out, r)
		}
	}
	return out, nil
}

func (s indexSearchSource) RunFile(runID, name string) ([]byte, error) {
	return s.ix.RunFile(runID, name)
}

// SearchRouteDoc describes GET /search for the OpenAPI document.
var SearchRouteDoc = RouteDoc{
	Method: "GET", Path: APIPrefix + "/search", Tag: "runs",
	Summary:  "Search sealed runs by the text of their steps, tool calls, answer, gate reasons and grade reason (words, \"phrases\", tool:NAME)",
	Response: resultsindex.SearchResult{},
	Query: []QueryDoc{
		{Name: "q", Description: "Words (all must match), \"quoted phrases\", tool:NAME; agent:, version:, benchmark:, outcome: also accepted inline"},
		{Name: "agent", Description: "Agent config id"},
		{Name: "version", Description: "Agent version"},
		{Name: "benchmark", Description: "Benchmark (suite) id; a version suffix is ignored"},
		{Name: "outcome", Description: "Run outcome, e.g. fail"},
		{Name: "limit", Description: "Hits returned, default 50, at most 200"},
	},
}
