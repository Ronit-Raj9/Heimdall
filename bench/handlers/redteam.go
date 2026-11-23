package handlers

// Red team (Z04, docs/red-team.md): write an adaptive red-team benchmark for
// the org's agent, list the regressions successful attacks became, harvest
// an evaluation's successful attacks into regressions, and accept or reject
// one. Every write goes through the org's confined project store, like the
// suite authoring area; readiness checks stay the suite.admit job.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/redteam"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// RedTeamCatalog answers GET /redteam/catalog.
type RedTeamCatalog struct {
	Categories  []redteam.Category `json:"categories"`
	Strategies  []redteam.Strategy `json:"strategies"`
	Channels    []redteam.Channel  `json:"channels"`
	Suite       string             `json:"suite"`        // the default benchmark id
	MaxAttempts int                `json:"max_attempts"` // the default attempt cap
	Regressions string             `json:"regressions"`  // the regressions benchmark id
}

// RedTeamBenchmarkRequest is the body of POST /redteam/benchmark: the plan,
// and Replace to rewrite an existing red-team benchmark of the same id.
type RedTeamBenchmarkRequest struct {
	redteam.Plan
	Replace bool `json:"replace,omitempty"`
}

// RedTeamBenchmarkResult answers POST /redteam/benchmark.
type RedTeamBenchmarkResult struct {
	Suite    string         `json:"suite"`
	Tasks    []string       `json:"tasks"`
	Cells    []redteam.Cell `json:"cells"`
	Files    int            `json:"files"`
	Replaced bool           `json:"replaced"`
	Next     string         `json:"next"`
}

// RedTeamRegressions answers GET /redteam/regressions.
type RedTeamRegressions struct {
	Suite   string               `json:"suite"`
	Exists  bool                 `json:"exists"`
	Items   []redteam.ReviewItem `json:"items"`
	Pending int                  `json:"pending"`
}

// RedTeamHarvestRequest is the body of POST /redteam/regressions/harvest.
type RedTeamHarvestRequest struct {
	Evaluation string `json:"evaluation"`
}

// RedTeamReviewRequest is the body of POST /redteam/regressions/{task}/review.
type RedTeamReviewRequest struct {
	Decision string `json:"decision"` // accepted | rejected
	By       string `json:"by,omitempty"`
}

type redTeamHandler struct {
	env   OrgEnv
	evals ReportCardEvaluations
	now   func() time.Time
}

func init() {
	RegisterOrgArea("redteam", func(env OrgEnv, r *router.Router) {
		var evals ReportCardEvaluations
		if env.Cfg != nil && env.Cfg.Root != "" {
			evals = NewFileEvaluations(env.Cfg.Root)
		}
		h := &redTeamHandler{env: env, evals: evals, now: time.Now}
		r.GET(APIPrefix+"/redteam/catalog", env.R(h.catalog))
		r.POST(APIPrefix+"/redteam/benchmark", env.W(h.benchmark))
		r.GET(APIPrefix+"/redteam/regressions", env.R(h.regressions))
		r.POST(APIPrefix+"/redteam/regressions/harvest", env.W(h.harvest))
		r.POST(APIPrefix+"/redteam/regressions/{task}/review", env.W(h.review))
	})
}

func (h *redTeamHandler) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

// suiteDir resolves benchmarks/<id> in the org's project, creating the area.
func (h *redTeamHandler) suiteDir(ctx *fasthttp.RequestCtx, id string) (string, bool) {
	p := h.env.Services.Projects
	if p == nil {
		sendUnavailable(ctx, h.env.Mode, "project store")
		return "", false
	}
	if err := os.MkdirAll(filepath.Join(p.Root(), string(projectfs.AreaBenchmarks)), 0o755); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "create benchmarks folder: "+err.Error())
		return "", false
	}
	dir, err := p.Resolve(projectfs.AreaBenchmarks, id)
	if err != nil {
		h.bad(ctx, "benchmark "+id+": "+err.Error())
		return "", false
	}
	return dir, true
}

// catalog handles GET /redteam/catalog.
func (h *redTeamHandler) catalog(ctx *fasthttp.RequestCtx) {
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, RedTeamCatalog{
		Categories: redteam.Categories(), Strategies: redteam.Strategies(), Channels: redteam.Channels(),
		Suite: redteam.DefaultSuite, MaxAttempts: redteam.DefaultMaxAttempts, Regressions: redteam.RegressionsSuite,
	})
}

// benchmark handles POST /redteam/benchmark: writes the red-team benchmark.
func (h *redTeamHandler) benchmark(ctx *fasthttp.RequestCtx) {
	var req RedTeamBenchmarkRequest
	if !decodeStrict(ctx, h.env.Mode, &req, "red-team plan") {
		return
	}
	plan := req.Plan
	if err := plan.Normalize(); err != nil {
		h.bad(ctx, err.Error())
		return
	}
	if isBuiltinSuite(h.env.Services.Suites, plan.Suite) {
		sendBuiltinReadOnly(ctx, h.env.Mode, plan.Suite)
		return
	}
	files, err := redteam.Suite(plan)
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
	dir, ok := h.suiteDir(ctx, plan.Suite)
	if !ok {
		return
	}
	replaced := false
	if _, err := os.Lstat(dir); err == nil {
		if !redteam.IsSuite(dir) {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "benchmark "+plan.Suite+" already exists and is not a red-team benchmark; pick another id")
			return
		}
		if !req.Replace {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "red-team benchmark "+plan.Suite+" already exists; send replace: true to rewrite it (its readiness checks run again)")
			return
		}
		old := dir + ".old-" + h.now().UTC().Format("20060102T150405")
		if err := os.Rename(dir, old); err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "replace benchmark: "+err.Error())
			return
		}
		if err := writeTreeAtomic(dir, files); err != nil {
			_ = os.Rename(old, dir)
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "write benchmark: "+err.Error())
			return
		}
		_ = os.RemoveAll(old) // the new folder is in place; a leftover old copy is only disk
		replaced = true
	} else if err := writeTreeAtomic(dir, files); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "write benchmark: "+err.Error())
		return
	}
	out := RedTeamBenchmarkResult{Suite: plan.Suite, Cells: plan.Cells(), Files: len(files), Replaced: replaced, Tasks: []string{},
		Next: "Run the readiness checks on " + plan.Suite + ", then evaluate your agent on it: attacks adapt with " + plan.AttackerProvider + "/" + plan.AttackerModel + " on your own key."}
	for _, c := range out.Cells {
		out.Tasks = append(out.Tasks, redteam.TaskID(c))
	}
	syncDatasetVersion(h.env.Services.Projects.Root(), plan.Suite, dir, actorName(ctx), "Red team")
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, out)
}

// regressions handles GET /redteam/regressions.
func (h *redTeamHandler) regressions(ctx *fasthttp.RequestCtx) {
	p := h.env.Services.Projects
	if p == nil {
		sendUnavailable(ctx, h.env.Mode, "project store")
		return
	}
	out := RedTeamRegressions{Suite: redteam.RegressionsSuite, Items: []redteam.ReviewItem{}}
	dir, err := p.Resolve(projectfs.AreaBenchmarks, redteam.RegressionsSuite)
	if err == nil {
		if _, err := os.Stat(filepath.Join(dir, "suite.yaml")); err == nil {
			out.Exists = true
			f, err := redteam.LoadReview(dir)
			if err != nil {
				sendDepError(ctx, h.env.Mode, "regressions", err)
				return
			}
			out.Items = f.Items
		}
	}
	for _, it := range out.Items {
		if it.State == redteam.ReviewPending {
			out.Pending++
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// harvest handles POST /redteam/regressions/harvest.
func (h *redTeamHandler) harvest(ctx *fasthttp.RequestCtx) {
	var req RedTeamHarvestRequest
	if !decodeStrict(ctx, h.env.Mode, &req, "harvest") {
		return
	}
	req.Evaluation = strings.TrimSpace(req.Evaluation)
	if !validID(req.Evaluation) {
		h.bad(ctx, "evaluation: an evaluation id is needed")
		return
	}
	if h.evals == nil || h.env.Services.Index == nil {
		sendUnavailable(ctx, h.env.Mode, "evaluation store and run index")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ev, err := h.evals.Get(work, req.Evaluation)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "evaluation", err)
		return
	}
	dir, ok := h.suiteDir(ctx, redteam.RegressionsSuite)
	if !ok {
		return
	}
	res, err := harvestRedTeam(work, LeaderboardSource(h.env.Services.Index), ev, h.env.Services.Projects, dir, h.now())
	if err != nil {
		sendDepError(ctx, h.env.Mode, "harvest", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, res)
}

// harvestRedTeam runs redteam.Harvest with benchmark folders from the
// project store.
func harvestRedTeam(ctx context.Context, src leaderboard.Source, ev *schemas.Evaluation, projects projectfs.Store, regDir string, now time.Time) (*redteam.HarvestResult, error) {
	root := func(benchmark string) string {
		id, _, _ := strings.Cut(benchmark, "@")
		d, err := projects.Resolve(projectfs.AreaBenchmarks, id)
		if err != nil {
			return ""
		}
		return d
	}
	return redteam.Harvest(ctx, src, ev, root, regDir, now)
}

// review handles POST /redteam/regressions/{task}/review.
func (h *redTeamHandler) review(ctx *fasthttp.RequestCtx) {
	task := pathParam(ctx, "task")
	if !redteam.IsRegressionTask(task) {
		sendNotFound(ctx, h.env.Mode, "regression", task)
		return
	}
	var req RedTeamReviewRequest
	if !decodeStrict(ctx, h.env.Mode, &req, "review") {
		return
	}
	by, ok, msg := signedActor(ctx, req.By)
	if !ok {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, msg)
		return
	}
	dir, ok := h.suiteDir(ctx, redteam.RegressionsSuite)
	if !ok {
		return
	}
	item, err := redteam.Decide(dir, task, req.Decision, by, h.now())
	switch {
	case errors.Is(err, os.ErrNotExist):
		sendNotFound(ctx, h.env.Mode, "regression", task)
	case err != nil:
		h.bad(ctx, err.Error())
	default:
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, item)
	}
}

// autoHarvestRedTeam turns the successful attacks of a finished evaluation
// into pending regressions, once per evaluation (the evaluation refresh
// calls it). An evaluation with no red-team benchmark costs a stat per
// child. Errors only delay the harvest; POST /redteam/regressions/harvest
// runs it by hand.
func autoHarvestRedTeam(env OrgEnv, ev *schemas.Evaluation, now time.Time) {
	p := env.Services.Projects
	if ev == nil || ev.Status != schemas.EvaluationDone || p == nil || env.Services.Index == nil {
		return
	}
	any := false
	for _, c := range ev.Children {
		id, _, _ := strings.Cut(c.Benchmark, "@")
		if d, err := p.Resolve(projectfs.AreaBenchmarks, id); err == nil && c.ExperimentID != "" && redteam.IsSuite(d) {
			any = true
			break
		}
	}
	if !any {
		return
	}
	if err := os.MkdirAll(filepath.Join(p.Root(), string(projectfs.AreaBenchmarks)), 0o755); err != nil {
		return
	}
	dir, err := p.Resolve(projectfs.AreaBenchmarks, redteam.RegressionsSuite)
	if err != nil || redteam.Harvested(dir, ev.ID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	if _, err := harvestRedTeam(ctx, LeaderboardSource(env.Services.Index), ev, p, dir, now); err != nil {
		return
	}
	_ = redteam.MarkHarvested(dir, ev.ID) // a failed mark only means the next refresh harvests again, which adds nothing new
}
