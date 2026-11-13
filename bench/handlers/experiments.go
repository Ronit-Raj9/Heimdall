package handlers

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

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
)

// EventsPoll answers GET /experiments/{id}/events without SSE: the events
// after ?since=<seq>, the cursor for the next poll, and the status. It is not
// in schemas/api.go; the UI's designed fallback polls GET /experiments/{id}.
type EventsPoll struct {
	ExpID     string                   `json:"exp_id"`
	Events    []schemas.LiveEvent      `json:"events"`
	Next      int                      `json:"next"`
	Truncated bool                     `json:"truncated"` // older events fell out of the window
	Status    schemas.ExperimentStatus `json:"status"`
}

// listExperiments handles GET /api/bench/experiments.
func (h *ExperimentsHandler) listExperiments(ctx *fasthttp.RequestCtx) {
	if h.index == nil {
		sendUnavailable(ctx, h.mode, "run index")
		return
	}
	out := schemas.ExperimentList{Experiments: []schemas.ExperimentStatus{}}
	for _, id := range h.index.Experiments() {
		if m, ok := h.index.Manifest(id); ok {
			out.Experiments = append(out.Experiments, BuildStatus(m, h.index.Runs(id)))
		}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// planExperiment handles POST /api/bench/experiments/plan. Nothing runs.
func (h *ExperimentsHandler) planExperiment(ctx *fasthttp.RequestCtx) {
	m, ok := h.plan(ctx)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, BuildPlan(m))
}

// createExperiment handles POST /api/bench/experiments: plan, then freeze the
// manifest and write the slots. Nothing runs until start.
func (h *ExperimentsHandler) createExperiment(ctx *fasthttp.RequestCtx) {
	m, ok := h.plan(ctx)
	if !ok {
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
// the experiment's status.
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
	if err := h.launcher.Start(work, id); err != nil {
		sendDepError(ctx, h.mode, "start experiment "+id, err)
		return
	}
	h.recorder.follow(id)
	st := schemas.ExperimentStatus{ExpID: id, SpentUSD: schemas.Unknown("not_aggregated", schemas.EvidenceGateway)}
	if h.index != nil {
		if m, ok := h.index.Manifest(id); ok {
			st = BuildStatus(m, h.index.Runs(id))
		}
	}
	st.State = schemas.ExperimentRunning
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, st)
}

// getExperiment handles GET /api/bench/experiments/{id}.
func (h *ExperimentsHandler) getExperiment(ctx *fasthttp.RequestCtx) {
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, BuildStatus(m, runs))
}

// getVerdict handles GET /api/bench/experiments/{id}/verdict?grade_source=.
// The gate checklist and config diff come from the run folder, not the
// analysis, so they show even when the analysis has nothing to say.
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
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, schemas.VerdictPayload{
		ExpID: m.ExpID, AnalysisID: a.AnalysisID, GradeSource: a.GradeSource, Suite: m.Experiment.Suite,
		Verdict: a.Verdict, Naive: a.Naive, Gates: gates, ConfigDiff: diff,
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
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, EventsPoll{
		ExpID: m.ExpID, Events: events, Next: next, Truncated: truncated, Status: BuildStatus(m, runs),
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
// Time and spend are not estimated and stay unknown. It is pure.
func BuildPlan(m *schemas.Manifest) schemas.ExperimentPlan {
	p := schemas.ExperimentPlan{
		Slots: len(m.Slots), Conditions: planConditions(m),
		EstWallS: schemas.Unknown("not_estimated", schemas.EvidenceOutcome),
		EstUSD:   schemas.Unknown("not_estimated", schemas.EvidenceGateway),
		Errors:   []string{}, Warnings: []string{},
	}
	pairs := map[string]bool{}
	for _, s := range m.Slots {
		if s.Counted {
			pairs[s.PairID] = true
		}
	}
	p.Pairs = len(pairs)
	alpha := cmp.Or(m.Experiment.Verdict.Alpha, defaultAlpha)
	p.SD, p.MDEAssumedSD = cmp.Or(m.Experiment.Verdict.AssumedSD, defaultAssumedSD), true
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

// plan decodes the experiment from the body and runs the launcher's planner.
func (h *ExperimentsHandler) plan(ctx *fasthttp.RequestCtx) (*schemas.Manifest, bool) {
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
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	m, err := h.launcher.Plan(work, exp)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "plan experiment: "+err.Error())
		return nil, false
	}
	if m == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "plan experiment: no manifest")
		return nil, false
	}
	return m, true
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
	case "", "application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml", "text/plain":
		dec := yaml.NewDecoder(bytes.NewReader(body))
		dec.KnownFields(true)
		if err := dec.Decode(&exp); err != nil {
			return nil, fasthttp.StatusBadRequest, fmt.Errorf("parse experiment YAML: %w", err)
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
	return a, true
}
