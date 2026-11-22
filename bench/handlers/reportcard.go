package handlers

// Report card area (agent-first lane A04): GET /evaluations/{id}/report and
// GET /agents/{id}/report-card build the report card of an evaluation from
// its child experiments' sealed runs (reportcard package). Evaluations are
// read from the org project's .heimdall/evaluations/<id>.json, the store
// lane A02 writes; this area only reads it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// ReportCardEvaluationsDir is where evaluations live inside a project.
const ReportCardEvaluationsDir = ".heimdall/evaluations"

// ReportCardEvaluations reads stored evaluations. Get returns an error
// wrapping schemas.ErrNotFound for an unknown id.
type ReportCardEvaluations interface {
	Get(ctx context.Context, id string) (*schemas.Evaluation, error)
	List(ctx context.Context) ([]schemas.Evaluation, error)
}

// AgentReportCard is GET /agents/{id}/report-card: the report of the
// evaluation it was built from, limited to the agent's arms. Report is nil
// when no evaluation ran the agent.
type AgentReportCard struct {
	Agent   string             `json:"agent"`
	Version int                `json:"version,omitempty"`
	Model   string             `json:"model,omitempty"`
	Report  *reportcard.Report `json:"report"`
}

// fileEvaluations reads <root>/.heimdall/evaluations/*.json.
type fileEvaluations struct{ dir string }

// NewFileEvaluations returns the evaluation reader over a project root.
func NewFileEvaluations(root string) ReportCardEvaluations {
	return fileEvaluations{dir: filepath.Join(root, filepath.FromSlash(ReportCardEvaluationsDir))}
}

func (f fileEvaluations) Get(_ context.Context, id string) (*schemas.Evaluation, error) {
	if !validID(id) {
		return nil, fmt.Errorf("evaluation %q: %w", id, schemas.ErrNotFound)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, id+".json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("evaluation %q: %w", id, schemas.ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var ev schemas.Evaluation
	if err := json.Unmarshal(data, &ev); err != nil {
		return nil, fmt.Errorf("evaluation %q: %w", id, err)
	}
	return &ev, nil
}

func (f fileEvaluations) List(ctx context.Context) ([]schemas.Evaluation, error) {
	entries, err := os.ReadDir(f.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []schemas.Evaluation
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if e.IsDir() || !ok {
			continue
		}
		ev, err := f.Get(ctx, id)
		if err != nil {
			continue // one unreadable file never hides the others
		}
		out = append(out, *ev)
	}
	return out, nil
}

type reportCardHandler struct {
	env   OrgEnv
	evals ReportCardEvaluations
}

func init() {
	RegisterOrgArea("reportcard", registerReportCard)
}

// registerReportCard registers the report card routes.
func registerReportCard(env OrgEnv, r *router.Router) {
	var evals ReportCardEvaluations
	if env.Cfg != nil && env.Cfg.Root != "" {
		evals = NewFileEvaluations(env.Cfg.Root)
	}
	h := &reportCardHandler{env: env, evals: evals}
	r.GET(APIPrefix+"/evaluations/{id}/report", env.R(h.getReport))
	r.GET(APIPrefix+"/agents/{id}/report-card", env.R(h.getAgentReportCard))
}

// options names benchmark capabilities from the suite track when the
// server can load suites, else from the id alone.
func (h *reportCardHandler) options(ctx context.Context) reportcard.Options {
	suites := h.env.Services.Suites
	if suites == nil {
		return reportcard.Options{}
	}
	cache := map[string]string{}
	faultMaps := map[string]map[string]string{}
	return reportcard.Options{CapabilityOf: func(benchmark string) string {
		if c, ok := cache[benchmark]; ok {
			return c
		}
		track := ""
		if s, err := suites.LoadSuite(ctx, benchmark); err == nil && s != nil {
			track = s.Track
		}
		c := reportcard.CapabilityFor(benchmark, track)
		cache[benchmark] = c
		return c
	}, FaultOf: func(benchmark, condition string) string {
		// An evaluation launched before children carried their fault map:
		// read the benchmark's fault and probe metadata, the same choice
		// faults.Conditions made at launch, never the condition's name.
		id, _, _ := strings.Cut(benchmark, "@")
		m, ok := faultMaps[id]
		if !ok {
			if s, err := suites.LoadSuite(ctx, id); err == nil && s != nil {
				m = faults.Map(faults.Names, s)
			}
			faultMaps[id] = m
		}
		if m == nil {
			return reportcard.DefaultFault(condition)
		}
		if f := m[condition]; f != faults.Clean {
			return f
		}
		return ""
	}}
}

// source is the run index as a leaderboard source, with the org's key
// prices costing runs of models the price snapshot does not list (N2).
func (h *reportCardHandler) source(ctx *fasthttp.RequestCtx, work context.Context) leaderboard.Source {
	s := h.env.Services
	var keys KeyLister
	if s.Keys != nil {
		keys = s.Keys
	}
	return PricedLeaderboardSource(s.Index, KeyPriceLookup(work, keys, quickOrgID(ctx, s.OrgID)))
}

func (h *reportCardHandler) ready(ctx *fasthttp.RequestCtx) bool {
	switch {
	case h.env.Services.Index == nil:
		sendUnavailable(ctx, h.env.Mode, "run index")
	case h.evals == nil:
		sendUnavailable(ctx, h.env.Mode, "evaluation store")
	default:
		return true
	}
	return false
}

// getReport handles GET /api/bench/evaluations/{id}/report.
func (h *reportCardHandler) getReport(ctx *fasthttp.RequestCtx) {
	id, ok := idParam(ctx, h.env.Mode, "id", "evaluation")
	if !ok || !h.ready(ctx) {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ev, err := h.evals.Get(work, id)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "evaluation", err)
		return
	}
	opt := h.options(work)
	var where []string // Y13: ?where=key:value narrows the report to one metadata slice
	for _, w := range ctx.QueryArgs().PeekMulti("where") {
		where = append(where, string(w))
	}
	if opt.Where, err = reportcard.ParseWhere(where); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	rep, err := reportcard.Build(work, h.source(ctx, work), ev, opt)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "report card", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, rep)
}

// getAgentReportCard handles GET
// /api/bench/agents/{id}/report-card?version=N&model=M: the report of the
// newest done evaluation that ran the agent (at that version and model when
// given), limited to the agent's arms.
func (h *reportCardHandler) getAgentReportCard(ctx *fasthttp.RequestCtx) {
	agent, ok := idParam(ctx, h.env.Mode, "id", "agent")
	if !ok || !h.ready(ctx) {
		return
	}
	args := ctx.QueryArgs()
	version := 0
	if raw := strings.TrimPrefix(strings.TrimSpace(string(args.Peek("version"))), "v"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("version %q is not a version number", raw))
			return
		}
		version = n
	}
	model := strings.TrimSpace(string(args.Peek("model")))
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	evals, err := h.evals.List(work)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "evaluations", err)
		return
	}
	out := AgentReportCard{Agent: agent, Version: version, Model: model}
	ev, labels := reportcard.SelectForAgent(evals, agent, version, model)
	if ev != nil {
		opt := h.options(work)
		opt.Arms = labels
		if out.Report, err = reportcard.Build(work, h.source(ctx, work), ev, opt); err != nil {
			sendDepError(ctx, h.env.Mode, "report card", err)
			return
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}
