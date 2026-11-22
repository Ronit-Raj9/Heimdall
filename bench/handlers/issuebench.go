// Issues to benchmark (lane G06): a run scans a connected GitHub repository
// for merged pull requests that close an issue, builds and validates each
// one and, once a person accepts it, adds it to the org's private benchmark.
// The pipeline is bench/issuebench/pipeline; the stages come from
// IssueBenchStages, which cmd/bifrost-bench sets when the scan, builder,
// validate and emit packages are wired. Without them the run routes still
// work and each step says it is not available yet.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// IssueBenchStages returns the pipeline stages for one org; nil gives every
// org a pipeline with no stages.
var IssueBenchStages func(env OrgEnv) pipeline.Stages

// IssueBenchDir is where an org's runs are kept, under its project root.
const IssueBenchDir = ".heimdall/issuebench"

var issueManagers sync.Map // run dir -> *pipeline.Manager; runs outlive router rebuilds

// IssueBenchManager returns the manager for runs under root, made once.
func IssueBenchManager(root string, stages func() pipeline.Stages) *pipeline.Manager {
	dir := filepath.Join(root, IssueBenchDir)
	if m, ok := issueManagers.Load(dir); ok {
		return m.(*pipeline.Manager)
	}
	var st pipeline.Stages
	if stages != nil {
		st = stages()
	}
	m, _ := issueManagers.LoadOrStore(dir, pipeline.NewManager(dir, st))
	return m.(*pipeline.Manager)
}

// IssueRunCreate is the body of POST /issuebench/runs.
type IssueRunCreate struct {
	Repo  string `json:"repo"`
	Limit int    `json:"limit,omitempty"`
	Since string `json:"since,omitempty"`
}

// IssueRunStart is the body of POST /issuebench/runs/{id}/start.
type IssueRunStart struct {
	Consent bool `json:"consent"`
}

// IssueRetry is the body of POST /issuebench/runs/{id}/issues/{issue}/retry.
type IssueRetry struct {
	Fix string `json:"fix"`
	pipeline.BuildOptions
}

// IssueRunList is GET /issuebench/runs.
type IssueRunList struct {
	Runs []pipeline.State `json:"runs"`
}

const issueBenchPrefix = APIPrefix + "/issuebench/runs"

func init() {
	RegisterOrgArea("issuebench", func(env OrgEnv, r *router.Router) {
		h := &issueBenchHandler{mode: env.Mode, m: IssueBenchManager(env.Cfg.Root, func() pipeline.Stages {
			if IssueBenchStages == nil {
				return pipeline.Stages{}
			}
			return IssueBenchStages(env)
		})}
		r.GET(issueBenchPrefix, env.R(h.list))
		r.POST(issueBenchPrefix, env.W(h.create))
		r.GET(issueBenchPrefix+"/{id}", env.R(h.get))
		r.POST(issueBenchPrefix+"/{id}/start", env.W(h.start))
		r.POST(issueBenchPrefix+"/{id}/cancel", env.W(h.cancel))
		r.POST(issueBenchPrefix+"/{id}/issues/{issue}/review", env.W(h.review))
		r.POST(issueBenchPrefix+"/{id}/issues/{issue}/retry", env.W(h.retry))
	})
}

type issueBenchHandler struct {
	mode schemas.Mode
	m    *pipeline.Manager
}

func (h *issueBenchHandler) fail(ctx *fasthttp.RequestCtx, err error) {
	switch {
	case errors.Is(err, pipeline.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "run or issue not found")
	case errors.Is(err, pipeline.ErrBadRequest), errors.Is(err, pipeline.ErrNoConsent):
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
	case errors.Is(err, pipeline.ErrBusy):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, err.Error())
	}
}

func (h *issueBenchHandler) decode(ctx *fasthttp.RequestCtx, v any) bool {
	if err := json.Unmarshal(ctx.PostBody(), v); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be JSON: "+err.Error())
		return false
	}
	return true
}

func (h *issueBenchHandler) list(ctx *fasthttp.RequestCtx) {
	runs, err := h.m.List()
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, IssueRunList{Runs: runs})
}

// create handles POST /issuebench/runs: a dry scan (no model calls) saved as
// a new run.
func (h *issueBenchHandler) create(ctx *fasthttp.RequestCtx) {
	var req IssueRunCreate
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	s, err := h.m.Scan(work, req.Repo, pipeline.ScanOptions{Limit: req.Limit, Since: req.Since})
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, s)
}

// get handles GET /issuebench/runs/{id}?since=N: the run with log lines
// after sequence N (the page polls it for the live log).
func (h *issueBenchHandler) get(ctx *fasthttp.RequestCtx) {
	s, err := h.m.Load(pathParam(ctx, "id"))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	if v := string(ctx.QueryArgs().Peek("since")); v != "" {
		since, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "since must be a number")
			return
		}
		kept := s.Log[:0]
		for _, l := range s.Log {
			if l.Seq > since {
				kept = append(kept, l)
			}
		}
		s.Log = kept
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, s)
}

func (h *issueBenchHandler) start(ctx *fasthttp.RequestCtx) {
	var req IssueRunStart
	if !h.decode(ctx, &req) {
		return
	}
	s, err := h.m.Start(pathParam(ctx, "id"), req.Consent)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, s)
}

func (h *issueBenchHandler) cancel(ctx *fasthttp.RequestCtx) {
	s, err := h.m.Cancel(pathParam(ctx, "id"))
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, s)
}

func (h *issueBenchHandler) issue(ctx *fasthttp.RequestCtx) (int, bool) {
	n, err := strconv.Atoi(pathParam(ctx, "issue"))
	if err != nil || n <= 0 {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "issue must be a positive number")
		return 0, false
	}
	return n, true
}

func (h *issueBenchHandler) review(ctx *fasthttp.RequestCtx) {
	n, ok := h.issue(ctx)
	if !ok {
		return
	}
	var req pipeline.Review
	if !h.decode(ctx, &req) {
		return
	}
	work, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	s, err := h.m.Review(work, pathParam(ctx, "id"), n, req)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, s)
}

func (h *issueBenchHandler) retry(ctx *fasthttp.RequestCtx) {
	n, ok := h.issue(ctx)
	if !ok {
		return
	}
	var req IssueRetry
	if !h.decode(ctx, &req) {
		return
	}
	s, err := h.m.Retry(pathParam(ctx, "id"), n, req.Fix, req.BuildOptions)
	if err != nil {
		h.fail(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, s)
}
