package handlers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/clusters"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// ClustersHandler serves failure clusters: the non-passing verified runs
// of every experiment grouped by signature (docs/failure-clusters.md). It
// reads through the ReadIndex when that also implements clusters.Source;
// otherwise it answers 503.
//
// Hidden test names follow the run page's rule: a cluster names the failed
// hidden tests of sealed runs, the same names GET /runs/{id} already serves
// in the run's grade. Nothing is read from a suite's hidden test files, so
// the suite pages' rule (count hidden tests, never name them) is untouched.
type ClustersHandler struct {
	mode  schemas.Mode
	index ReadIndex
	now   func() time.Time
}

// NewClustersHandler creates the clusters handler. index may be nil.
func NewClustersHandler(mode schemas.Mode, index ReadIndex) *ClustersHandler {
	return &ClustersHandler{mode: mode, index: index, now: time.Now}
}

// RegisterRoutes registers GET /clusters and GET /clusters/{signature}.
func (h *ClustersHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	r.GET(APIPrefix+"/clusters", lib.ChainMiddlewares(h.getClusters, middlewares...))
	r.GET(APIPrefix+"/clusters/{signature}", lib.ChainMiddlewares(h.getCluster, middlewares...))
}

// getClusters handles GET
// /api/bench/clusters?suite=&agent=&exp=&since=&min=&synthetic=. exp may
// repeat or carry a comma list. A malformed parameter is a 400.
func (h *ClustersHandler) getClusters(ctx *fasthttp.RequestCtx) {
	rep, ok := h.build(ctx)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
}

// getCluster handles GET /api/bench/clusters/{signature} with the same
// filters: the cluster and its runs, or 404 when no run in the filtered set
// has that signature. min does not hide a cluster asked for by signature.
func (h *ClustersHandler) getCluster(ctx *fasthttp.RequestCtx) {
	sig, ok := idParam(ctx, h.mode, "signature", "cluster")
	if !ok {
		return
	}
	rep, ok := h.build(ctx)
	if !ok {
		return
	}
	d, err := clusters.Find(rep, sig)
	if err != nil {
		sendNotFound(ctx, h.mode, "cluster", sig)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, d)
}

// build parses the filters and builds the report, or answers the error and
// returns false.
func (h *ClustersHandler) build(ctx *fasthttp.RequestCtx) (*clusters.Report, bool) {
	src, isSource := h.index.(clusters.Source)
	if h.index == nil || !isSource {
		sendUnavailable(ctx, h.mode, "verified run index")
		return nil, false
	}
	q, err := clustersQuery(ctx.QueryArgs())
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return nil, false
	}
	q.Now = h.now()
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	rep, err := clusters.Build(work, src, q)
	if errors.Is(err, clusters.ErrBadQuery) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return nil, false
	}
	if err != nil {
		sendDepError(ctx, h.mode, "clusters", err)
		return nil, false
	}
	return rep, true
}

// clustersQuery reads the filter parameters. clusters.Build checks the
// values; only min's integer syntax is checked here.
func clustersQuery(args *fasthttp.Args) (clusters.Query, error) {
	q := clusters.Query{
		Suite:     string(args.Peek("suite")),
		Agent:     string(args.Peek("agent")),
		Since:     string(args.Peek("since")),
		Synthetic: string(args.Peek("synthetic")),
	}
	for _, v := range args.PeekMulti("exp") {
		q.Experiments = append(q.Experiments, string(v))
	}
	if raw := strings.TrimSpace(string(args.Peek("min"))); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return q, fmt.Errorf("min %q is not an integer", raw)
		}
		q.Min = n
	}
	return q, nil
}

// ClusterQueryDocs describes the filter parameters of both cluster routes
// for the OpenAPI description (APIRoutes).
var ClusterQueryDocs = []QueryDoc{
	{Name: "suite", Description: "Suite id or id@version"},
	{Name: "agent", Description: "Agent config id, or a config hash prefix of 6 or more characters"},
	{Name: "exp", Description: "Experiment id; repeat or comma-separate for several"},
	{Name: "since", Description: "Runs started at or after: RFC3339 time, date (2006-01-02) or age (24h, 7d)"},
	{Name: "min", Description: "Smallest cluster listed; default 0 (all)"},
	{Name: "synthetic", Description: "exclude (default), include or only synthetic runs"},
}
