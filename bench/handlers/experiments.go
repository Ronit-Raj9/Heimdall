package handlers

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// GradeSourceOriginal selects each run's original grade.
const GradeSourceOriginal = "original"

// ExperimentsHandler serves planning, creation, launch, status, analysis and
// live events of experiments.
type ExperimentsHandler struct {
	mode     schemas.Mode
	index    ReadIndex
	analyzer Analyzer
	launcher Launcher
	catalog  CatalogSource
	write    lib.Middleware
	recorder *eventRecorder
	analyses sync.Map // exp id -> id of the last original-grade analysis this server computed
}

// NewExperimentsHandler creates the experiments handler. write guards the
// POST routes (the write token); any dependency may be nil. The catalog only
// marks baseline gates on the verdict page.
func NewExperimentsHandler(mode schemas.Mode, index ReadIndex, analyzer Analyzer, launcher Launcher, catalog CatalogSource, write lib.Middleware) *ExperimentsHandler {
	return &ExperimentsHandler{
		mode: mode, index: index, analyzer: analyzer, launcher: launcher, catalog: catalog, write: write,
		recorder: newEventRecorder(launcher),
	}
}

// RegisterRoutes registers the experiment routes.
func (h *ExperimentsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	p := APIPrefix + "/experiments"
	r.GET(p, lib.ChainMiddlewares(h.listExperiments, middlewares...))
	r.POST(p, lib.ChainMiddlewares(h.createExperiment, writeMW...))
	r.POST(p+"/plan", lib.ChainMiddlewares(h.planExperiment, writeMW...))
	r.POST(p+"/{id}/start", lib.ChainMiddlewares(h.startExperiment, writeMW...))
	r.GET(p+"/{id}", lib.ChainMiddlewares(h.getExperiment, middlewares...))
	r.GET(p+"/{id}/verdict", lib.ChainMiddlewares(h.getVerdict, middlewares...))
	r.GET(p+"/{id}/diagnostics", lib.ChainMiddlewares(h.getDiagnostics, middlewares...))
	r.GET(p+"/{id}/grid", lib.ChainMiddlewares(h.getGrid, middlewares...))
	r.GET(p+"/{id}/events", lib.ChainMiddlewares(h.getEvents, middlewares...))
	// Jobs area: cancel, resume and regrade run as jobs (experimentjobs.go).
	r.POST(p+"/{id}/cancel", lib.ChainMiddlewares(h.cancelExperiment, writeMW...))
	r.POST(p+"/{id}/resume", lib.ChainMiddlewares(h.resumeExperiment, writeMW...))
	r.POST(p+"/{id}/regrade", lib.ChainMiddlewares(h.regradeExperiment, writeMW...))
}

// Close ends every open event stream and subscription. The server calls it
// before shutdown so long-lived streams do not hold the shutdown open.
func (h *ExperimentsHandler) Close() {
	h.recorder.close()
}

// Planning defaults when the experiment leaves them out (DESIGN.md 16.6).
const (
	defaultAlpha     = 0.05
	defaultAssumedSD = 0.30
	mdePower         = 0.8
	// noPilotReason is why a plan's time and spend are unknown: nothing has
	// run yet to estimate them from.
	noPilotReason = "no pilot yet"
)

// listExperiments handles GET /api/bench/experiments.
func (h *ExperimentsHandler) listExperiments(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	out := schemas.ExperimentList{Experiments: []schemas.ExperimentStatus{}}
	for _, id := range h.index.Experiments() {
		if m, ok := h.index.Manifest(id); ok {
			st := h.status(m, h.index.Runs(id), false)
			// UI regroup (U2): the newest analysis's decision, when the index can read it.
			if vi, isVerdict := h.index.(VerdictIndex); isVerdict {
				if d, found := vi.LatestDecision(id); found {
					st.Decision = d
				}
			}
			out.Experiments = append(out.Experiments, st)
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// planExperiment handles POST /api/bench/experiments/plan. Nothing runs. A
// planner error is part of the plan (its errors list, HTTP 200), so the UI
// shows it beside the form; only an unreadable body is a 4xx.
func (h *ExperimentsHandler) planExperiment(ctx *fasthttp.RequestCtx) {
	exp, ok := h.decode(ctx)
	if !ok {
		return
	}
	m, err := h.planManifest(ctx, exp)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		sendDepError(ctx, h.mode, "plan experiment", err)
	case err != nil:
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, FailedPlan(exp, err))
	default:
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, BuildPlan(m))
	}
}

// createExperiment handles POST /api/bench/experiments: plan, then freeze the
// manifest and write the slots. Nothing runs until start. A planner error is
// a 400 here: there is nothing to create.
func (h *ExperimentsHandler) createExperiment(ctx *fasthttp.RequestCtx) {
	exp, ok := h.decode(ctx)
	if !ok {
		return
	}
	m, err := h.planManifest(ctx, exp)
	if errors.Is(err, context.DeadlineExceeded) {
		sendDepError(ctx, h.mode, "plan experiment", err)
		return
	}
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "plan experiment: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := h.launcher.Create(work, m); err != nil {
		sendDepError(ctx, h.mode, "create experiment "+m.ExpID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, schemas.ExperimentCreated{
		ExpID: m.ExpID, ManifestHash: m.Hash, CreatedAt: m.CreatedAt,
	})
}

// startExperiment handles POST /api/bench/experiments/{id}/start and answers
// 202 with the experiment's full status. The launcher has just accepted the
// experiment, so the status counts it as active.
func (h *ExperimentsHandler) startExperiment(ctx *fasthttp.RequestCtx) {
	if h.launcher == nil {
		sendUnavailable(ctx, h.mode, "launcher")
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := h.launcher.Start(withPrincipal(work, ctx), id); err != nil { // Jobs area: the run job records who started it
		sendDepError(ctx, h.mode, "start experiment "+id, err)
		return
	}
	h.recorder.follow(id)
	// Without the experiment in the index only the id and the state are known.
	st := schemas.ExperimentStatus{ExpID: id, State: schemas.ExperimentRunning,
		SpentUSD: schemas.Unknown("not_indexed", schemas.EvidenceGateway)}
	if h.index != nil {
		if m, ok := h.index.Manifest(id); ok {
			st = h.status(m, h.index.Runs(id), true)
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, st)
}

// getExperiment handles GET /api/bench/experiments/{id}.
func (h *ExperimentsHandler) getExperiment(ctx *fasthttp.RequestCtx) {
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, h.status(m, runs, false))
}

// getVerdict handles GET /api/bench/experiments/{id}/verdict?grade_source=.
// The gate checklist and config diff come from the run folder, not the
// analysis, so they show even when the analysis has nothing to say. The
// release policy result comes from the analysis and is absent when the
// experiment has no policy.
func (h *ExperimentsHandler) getVerdict(ctx *fasthttp.RequestCtx) {
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	a, ok := h.analyze(ctx, m.ExpID)
	if !ok {
		return
	}
	var catalog *schemas.Catalog
	if h.catalog != nil {
		catalog = h.catalog.Catalog()
	}
	gates, err := GateSummaries(h.index, runs, catalog)
	if err != nil {
		sendDepError(ctx, h.mode, "summarize gates of "+m.ExpID, err)
		return
	}
	diff, err := ConfigDiff(m)
	if err != nil {
		sendDepError(ctx, h.mode, "config diff of "+m.ExpID, err)
		return
	}
	// The verdict carries the analysis's own mode (a smoke experiment is
	// synthetic on a live server), never a stronger label than its evidence
	// (audit 7-B-03).
	lib.SendJSON(ctx, analysisMode(h.mode, a), fasthttp.StatusOK, schemas.VerdictPayload{
		ExpID: m.ExpID, AnalysisID: a.AnalysisID, GradeSource: a.GradeSource, Suite: m.Experiment.Suite,
		Verdict: a.Verdict, Naive: a.Naive, Gates: gates, ConfigDiff: diff, Policy: a.Policy,
		ReviewedRuns: a.ReviewedRuns, ReviewChangedRuns: a.ReviewChangedRuns, // human review counts (docs/human-review.md)
		JudgeCalibration: a.JudgeCalibration, // W8 judge calibration (docs/judge-calibration.md)
	})
}

// getDiagnostics handles GET /api/bench/experiments/{id}/diagnostics.
func (h *ExperimentsHandler) getDiagnostics(ctx *fasthttp.RequestCtx) {
	m, _, ok := h.lookup(ctx)
	if !ok {
		return
	}
	a, ok := h.analyze(ctx, m.ExpID)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.DiagnosticsPayload{
		AnalysisID: a.AnalysisID, ExpID: m.ExpID, Diagnostics: a.Diagnostics, Meters: a.Meters, ArmMetrics: a.ArmMetrics,
	})
}

// getGrid handles GET /api/bench/experiments/{id}/grid.
func (h *ExperimentsHandler) getGrid(ctx *fasthttp.RequestCtx) {
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, BuildGrid(m, runs))
}

// getEvents handles GET /api/bench/experiments/{id}/events. A client that
// accepts text/event-stream (EventSource does) gets server-sent events; any
// other client gets the polling answer for ?since=<seq>.
func (h *ExperimentsHandler) getEvents(ctx *fasthttp.RequestCtx) {
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	if bytes.Contains(ctx.Request.Header.Peek(fasthttp.HeaderAccept), []byte("text/event-stream")) {
		h.streamEvents(ctx, m.ExpID)
		return
	}
	since := 0
	if raw := string(ctx.QueryArgs().Peek("since")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "since must be a non-negative event sequence number")
			return
		}
		since = n
	}
	h.recorder.follow(m.ExpID)
	events, next, truncated := h.recorder.since(m.ExpID, since)
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.EventsPoll{
		ExpID: m.ExpID, Events: events, Next: next, Truncated: truncated, Status: h.status(m, runs, false),
	})
}

// streamEvents answers with server-sent events from the launcher.
func (h *ExperimentsHandler) streamEvents(ctx *fasthttp.RequestCtx, expID string) {
	if h.launcher == nil {
		sendUnavailable(ctx, h.mode, "live events")
		return
	}
	ch, cancel := h.launcher.Events(expID)
	if ch == nil {
		if cancel != nil {
			cancel()
		}
		sendUnavailable(ctx, h.mode, "live events")
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("text/event-stream; charset=utf-8")
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Accel-Buffering", "no")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	mode, stop := h.mode, h.recorder.stop
	ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
		streamSSE(w, mode, ch, cancel, stop)
	})
}

// BuildPlan sizes a planned manifest: slots, pairs, per-condition counts and
// the minimal detectable effect on the primary endpoint for its task count,
// with the experiment's alpha and assumed SD (power 0.8, as the verdict).
// Time and spend stay unknown until a pilot exists. It never carries the
// manifest. It is pure.
func BuildPlan(m *schemas.Manifest) schemas.ExperimentPlan {
	p := emptyPlan(&m.Experiment)
	p.Slots, p.Conditions = len(m.Slots), planConditions(m)
	pairs := map[string]bool{}
	for _, s := range m.Slots {
		if m.Experiment.AA && s.Arm == "A2" { // Eval integrity area (S2): an A/A slot is a pair id of its own, not an A/B pair (docs/noise-floor.md)
			continue // Eval integrity area (S2)
		} // Eval integrity area (S2)
		if s.Counted {
			pairs[s.PairID] = true
		}
	}
	p.Pairs = len(pairs)
	alpha := cmp.Or(m.Experiment.Verdict.Alpha, defaultAlpha)
	tasks := 0
	for _, c := range p.Conditions {
		if c.Condition == m.Experiment.Primary.Condition {
			tasks = c.Tasks
		}
	}
	mde := stats.MDE(tasks, p.SD, alpha, mdePower)
	if math.IsInf(mde, 0) || math.IsNaN(mde) {
		p.Warnings = append(p.Warnings, fmt.Sprintf("minimal detectable effect unknown: the primary condition %q has %d tasks; at least 2 are needed",
			m.Experiment.Primary.Condition, tasks))
		return p
	}
	p.MDE = mde * 100
	return p
}

// FailedPlan is the plan answered when the planner refuses an experiment:
// nothing sized, the planner's error in errors. It is pure.
func FailedPlan(exp *schemas.Experiment, err error) schemas.ExperimentPlan {
	p := emptyPlan(exp)
	p.Errors = append(p.Errors, "plan experiment: "+err.Error())
	return p
}

// emptyPlan is a plan with nothing sized: the assumed SD, estimates unknown
// for want of a pilot, and empty, non-null lists.
func emptyPlan(exp *schemas.Experiment) schemas.ExperimentPlan {
	return schemas.ExperimentPlan{
		Conditions: []schemas.PlanCondition{},
		SD:         cmp.Or(exp.Verdict.AssumedSD, defaultAssumedSD), MDEAssumedSD: true,
		EstWallS: schemas.Unknown(noPilotReason, schemas.EvidenceOutcome),
		EstUSD:   schemas.Unknown(noPilotReason, schemas.EvidenceGateway),
		Errors:   []string{}, Warnings: []string{},
	}
}

// decode checks for a launcher and reads the experiment from the body, or
// answers the error.
func (h *ExperimentsHandler) decode(ctx *fasthttp.RequestCtx) (*schemas.Experiment, bool) {
	if h.launcher == nil {
		sendUnavailable(ctx, h.mode, "launcher")
		return nil, false
	}
	exp, status, err := DecodeExperiment(mediaType(ctx), ctx.PostBody())
	if err != nil {
		errType := lib.ErrTypeBadRequest
		if status == fasthttp.StatusUnsupportedMediaType {
			errType = lib.ErrTypeUnsupportedMedia
		}
		lib.SendError(ctx, h.mode, status, errType, err.Error())
		return nil, false
	}
	return exp, true
}

// planManifest runs the launcher's planner under the read deadline.
func (h *ExperimentsHandler) planManifest(ctx *fasthttp.RequestCtx, exp *schemas.Experiment) (*schemas.Manifest, error) {
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	m, err := h.launcher.Plan(work, exp)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("the planner returned no manifest")
	}
	return m, nil
}

// status derives an experiment's status with the facts BuildStatus cannot
// read itself: run costs, the launcher's activity and the newest analysis.
// started marks an experiment the launcher has just accepted.
func (h *ExperimentsHandler) status(m *schemas.Manifest, runs []RunView, started bool) schemas.ExperimentStatus {
	active := started
	if ar, ok := h.launcher.(ActivityReporter); ok && !active {
		active = ar.Active(m.ExpID)
	}
	return BuildStatus(m, runs, StatusFacts{Costs: runCosts(h.index, runs), Active: active, AnalysisID: h.latestAnalysis(m.ExpID),
		Cancelled: !active && h.cancelledFact(m.ExpID)}) // Jobs area: cancelled state (experimentjobs.go)
}

// latestAnalysis returns the newest analysis id of an experiment: from the
// index when it lists analyses, else the last one this server computed.
func (h *ExperimentsHandler) latestAnalysis(expID string) string {
	if ai, ok := h.index.(AnalysisIndex); ok {
		if id, found := ai.LatestAnalysis(expID); found {
			return id
		}
	}
	if id, ok := h.analyses.Load(expID); ok {
		if s, isString := id.(string); isString {
			return s
		}
	}
	return ""
}

// runCosts reads cost_usd from every sealed run's metrics.json. A run with
// no metrics.json gets no entry. A file that cannot be read or parsed, or
// carries no cost, gives an unknown: a bad file never zeroes the spend and
// never fails a status read.
func runCosts(index ReadIndex, runs []RunView) map[string]schemas.Value {
	out := map[string]schemas.Value{}
	for _, rv := range runs {
		if !rv.Sealed {
			continue
		}
		id := rv.Run.RunID
		data, found, err := readRunFile(index, id, FileMetrics)
		if err != nil {
			out[id] = schemas.Unknown("metrics_unreadable", schemas.EvidenceGateway)
			continue
		}
		if !found {
			continue
		}
		var metrics map[string]schemas.Value
		if err := json.Unmarshal(data, &metrics); err != nil {
			out[id] = schemas.Unknown("metrics_unreadable", schemas.EvidenceGateway)
			continue
		}
		c, ok := metrics[costMetric]
		if !ok {
			c = schemas.Unknown("cost_not_measured", schemas.EvidenceGateway)
		}
		out[id] = c
	}
	return out
}

// DecodeExperiment reads an experiment from a request body: JSON for
// application/json (task selectors as {"all": true} or {"ids": [...]}),
// YAML for application/yaml, text/yaml, application/x-yaml, text/plain or no
// type. Unknown fields are errors in both, so a typo never plans silently.
// The int is the HTTP status for the error.
func DecodeExperiment(mediaType string, body []byte) (*schemas.Experiment, int, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, fasthttp.StatusBadRequest, errors.New("request body must be the experiment as YAML or JSON")
	}
	var exp schemas.Experiment
	switch mediaType {
	case "application/json":
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&exp); err != nil {
			return nil, fasthttp.StatusBadRequest, fmt.Errorf("parse experiment JSON: %w", err)
		}
		if err := dec.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
			return nil, fasthttp.StatusBadRequest, errors.New("parse experiment JSON: more than one JSON value in the body")
		}
	case "", "application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml", "text/plain":
		dec := yaml.NewDecoder(bytes.NewReader(body))
		dec.KnownFields(true)
		if err := dec.Decode(&exp); err != nil {
			return nil, fasthttp.StatusBadRequest, fmt.Errorf("parse experiment YAML: %w", err)
		}
		var extra yaml.Node
		if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, fasthttp.StatusBadRequest, errors.New("parse experiment YAML: more than one YAML document in the body")
		}
	default:
		return nil, fasthttp.StatusUnsupportedMediaType,
			fmt.Errorf("unsupported content type %q: send application/json or application/yaml", mediaType)
	}
	return &exp, fasthttp.StatusOK, nil
}

// lookup resolves the {id} experiment and its runs, or answers the error.
func (h *ExperimentsHandler) lookup(ctx *fasthttp.RequestCtx) (*schemas.Manifest, []RunView, bool) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return nil, nil, false
	}
	id, ok := idParam(ctx, h.mode, "id", "experiment")
	if !ok {
		return nil, nil, false
	}
	m, ok := h.index.Manifest(id)
	if !ok {
		sendNotFound(ctx, h.mode, "experiment", id)
		return nil, nil, false
	}
	return m, h.index.Runs(id), true
}

// analyze runs the analyzer for expID with ?grade_source= (default
// original), or answers the error.
func (h *ExperimentsHandler) analyze(ctx *fasthttp.RequestCtx, expID string) (*schemas.Analysis, bool) {
	if h.analyzer == nil {
		sendUnavailable(ctx, h.mode, "analyzer")
		return nil, false
	}
	source := strings.TrimSpace(string(ctx.QueryArgs().Peek("grade_source")))
	if source == "" {
		source = GradeSourceOriginal
	}
	if !validID(source) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "grade_source must be original or a regrade id")
		return nil, false
	}
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	a, err := h.analyzer.Analyze(work, expID, source)
	if err != nil {
		sendDepError(ctx, h.mode, "analyze experiment "+expID, err)
		return nil, false
	}
	if a == nil {
		sendNotFound(ctx, h.mode, "analysis", expID)
		return nil, false
	}
	if source == GradeSourceOriginal && a.AnalysisID != "" {
		h.analyses.Store(expID, a.AnalysisID)
	}
	return a, true
}

// analysisMode is the mode a response about analysis a is labeled with: the
// analysis's mode when it records one and the server is live, else the
// server's. It is never live unless both the server and the analysis are.
func analysisMode(server schemas.Mode, a *schemas.Analysis) schemas.Mode {
	if server == schemas.ModeLive && a != nil && a.Mode != "" {
		return a.Mode
	}
	return server
}
