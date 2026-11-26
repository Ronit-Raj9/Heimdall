package handlers

// Evaluations area (agent-first contract, section 3): one agent, or two arms
// for an A/B, across many benchmarks at once. POST /evaluations resolves the
// pack, drops benchmarks the agent cannot run, and builds ONE normal
// experiment per benchmark. Each child goes through the same path QuickRun
// uses: experiments/<name>.yaml written through the project store, then the
// launcher's Plan, Create and Start (local runs, or hosted jobs when the
// launcher is the job runner). The evaluation itself is a JSON file under
// .heimdall/evaluations/ (evalstore), and its status is read live from the
// children.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/agentversions"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/evalstore"
	"github.com/maximhq/bifrost/bench/faults"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/packs"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/redteam"
	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/simuser"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// Evaluation defaults and limits.
const (
	// The defaults are the Quick look preset (G4-16): 10 tasks once each.
	// More tasks narrow the margin faster than more trials, so a second
	// trial is opt-in.
	EvaluationDefaultTasks  = 10
	EvaluationDefaultTrials = 1
	EvaluationMaxTasks      = 50
	EvaluationMaxTrials     = 10
	EvaluationMaxBenchmarks = 40
	EvaluationMaxBudget     = 1000.0
	// EvaluationDefaultConcurrency is how many runs of one benchmark go at
	// once when the spec sets none; the org's worker slots still bound what
	// actually runs side by side. EvaluationMaxConcurrency bounds the spec.
	EvaluationDefaultConcurrency = 4
	EvaluationMaxConcurrency     = 16
	// evaluationLaunchTimeout bounds planning and starting every child.
	evaluationLaunchTimeout = 3 * time.Minute
)

// EvaluationList is GET /evaluations.
type EvaluationList struct {
	Evaluations []*schemas.Evaluation `json:"evaluations"`
}

func init() {
	RegisterOrgArea("evaluations", func(env OrgEnv, r *router.Router) {
		h := &evaluationsHandler{env: env, now: time.Now}
		p := APIPrefix + "/evaluations"
		r.GET(p, env.R(h.list))
		r.POST(p, env.W(h.create))
		r.GET(p+"/{id}", env.R(h.get))
		r.POST(p+"/{id}/cancel", env.W(h.cancel))
		// Run control (evaluationcontrol.go): pause, resume, rerun failures.
		r.POST(p+"/{id}/pause", env.W(h.pause))
		r.POST(p+"/{id}/resume", env.W(h.resume))
		r.POST(p+"/{id}/rerun-failures", env.W(h.rerunFailures))
	})
}

type evaluationsHandler struct {
	env OrgEnv
	now func() time.Time
}

// evalCompat is the compatibility check; tests replace it. An agent whose
// config is not in the project here (nil) is left to the planner.
var evalCompat = func(a *schemas.AgentConfig, s *schemas.Suite) (bool, []schemas.Capability, string) {
	if a == nil {
		return true, nil, ""
	}
	ok, missing, reason, _ := compat.Full(a, s)
	return ok, missing, reason
}

var evalArmIDRe = regexp.MustCompile(`^[A-Z][A-Za-z0-9]{0,15}$`)

func (h *evaluationsHandler) root() string {
	if p := h.env.Services.Projects; p != nil {
		return p.Root()
	}
	if h.env.Cfg != nil {
		return h.env.Cfg.Root
	}
	return ""
}

func (h *evaluationsHandler) store() *evalstore.Store { return evalstore.Open(h.root()) }

func (h *evaluationsHandler) fail(ctx *fasthttp.RequestCtx, status int, msg string) {
	t := lib.ErrTypeBadRequest
	switch status {
	case fasthttp.StatusServiceUnavailable:
		t = lib.ErrTypeUnavailable
	case fasthttp.StatusNotFound:
		t = lib.ErrTypeNotFound
	case fasthttp.StatusConflict:
		t = lib.ErrTypeConflict
	case fasthttp.StatusInternalServerError:
		t = lib.ErrTypeInternal
	}
	lib.SendError(ctx, h.env.Mode, status, t, msg)
}

// load reads the {id} evaluation of this org, or answers the error.
func (h *evaluationsHandler) load(ctx *fasthttp.RequestCtx) (*schemas.Evaluation, bool) {
	id, _ := ctx.UserValue("id").(string)
	if !evalstore.ValidID(id) {
		h.fail(ctx, fasthttp.StatusBadRequest, "evaluation id must look like ev_<letters and digits>")
		return nil, false
	}
	ev, err := h.store().Load(id)
	if err == nil && h.env.Services.OrgID != "" && ev.Org != "" && ev.Org != h.env.Services.OrgID {
		err = evalstore.ErrNotFound
	}
	if errors.Is(err, evalstore.ErrNotFound) {
		sendNotFound(ctx, h.env.Mode, "evaluation", id)
		return nil, false
	}
	if err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, err.Error())
		return nil, false
	}
	return ev, true
}

func (h *evaluationsHandler) list(ctx *fasthttp.RequestCtx) {
	evs, err := h.store().List()
	if err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "list evaluations: "+err.Error())
		return
	}
	out := EvaluationList{Evaluations: []*schemas.Evaluation{}}
	for _, ev := range evs {
		if h.env.Services.OrgID != "" && ev.Org != "" && ev.Org != h.env.Services.OrgID {
			continue
		}
		h.refresh(ev)
		h.livePaused(ev)
		out.Evaluations = append(out.Evaluations, ev)
	}
	h.backfillResults(out.Evaluations) // N161: results of evaluations that finished before they were stored (evalresult.go)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, plainServed(out))
}

func (h *evaluationsHandler) get(ctx *fasthttp.RequestCtx) {
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	h.refresh(ev)
	if h.fillResult(ev) { // N161: an evaluation that finished before results were stored (evalresult.go)
		_ = h.store().Save(ev)
	}
	h.livePaused(ev)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, plainServed(ev))
}

// cancel stops every child that still runs. A child that is not running is
// left as it is.
func (h *evaluationsHandler) cancel(ctx *fasthttp.RequestCtx) {
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	h.refresh(ev)
	if terminalEvaluation(ev.Status) {
		h.fail(ctx, fasthttp.StatusConflict, "evaluation "+ev.ID+" already ended ("+ev.Status+")")
		return
	}
	prev := ev.Status
	rc, _ := h.env.Services.Launcher.(RunController)
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	var errs []string
	for i := range ev.Children {
		c := &ev.Children[i]
		if c.ExperimentID == "" || terminalChild(c.Status) {
			continue
		}
		if rc != nil {
			if err := rc.Cancel(withPrincipal(work, ctx), c.ExperimentID); err != nil && !errors.Is(err, ErrNotRunning) {
				errs = append(errs, c.Benchmark+": "+err.Error())
				continue
			}
		}
		c.Status = string(schemas.ExperimentCancelled)
	}
	ev.Status = schemas.EvaluationCancelled
	h.notifyFinished(ev, prev) // evalnotify.go
	if err := h.store().Save(ev); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "save evaluation: "+err.Error())
		return
	}
	if len(errs) > 0 {
		h.fail(ctx, fasthttp.StatusBadGateway, "some experiments could not be cancelled: "+strings.Join(errs, "; "))
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, plainServed(ev))
}

func terminalChild(s string) bool {
	switch s {
	case string(schemas.ExperimentDone), string(schemas.ExperimentFailed), string(schemas.ExperimentCancelled), schemas.EvaluationChildSkipped:
		return true
	}
	return false
}

func terminalEvaluation(s string) bool {
	return s == schemas.EvaluationDone || s == schemas.EvaluationFailed || s == schemas.EvaluationCancelled
}

// evalRefreshChildren turns off reading child state from the run index;
// tests whose fixture index has unfinished slots turn it off.
var evalRefreshChildren = true

// refresh reads each child's live state from the run index and derives the
// evaluation's status; a changed status is saved.
func (h *evaluationsHandler) refresh(ev *schemas.Evaluation) {
	s := h.env.Services
	if h.startingOrStuck(ev) { // N182: children still starting in the background, or a restart lost them
		return
	}
	changed := false
	prev := ev.Status
	if s.Index != nil && evalRefreshChildren {
		eh := &ExperimentsHandler{mode: h.env.Mode, index: s.Index, launcher: s.Launcher}
		failed := 0
		for i := range ev.Children {
			c := &ev.Children[i]
			if c.ExperimentID == "" || c.Status == schemas.EvaluationChildSkipped {
				continue
			}
			m, ok := s.Index.Manifest(c.ExperimentID)
			if !ok {
				continue
			}
			runs := s.Index.Runs(c.ExperimentID)
			failed += failedRunCount(runs)
			st := eh.status(m, runs, false)
			if string(st.State) != c.Status && !(c.Status == string(schemas.ExperimentCancelled) && !terminalChild(string(st.State))) {
				c.Status, changed = string(st.State), true
			}
			if h.budgetFacts(c, m, st) { // G4-01: spend, cap and runs the cap left unstarted (evaluationbudget.go)
				changed = true
			}
		}
		if failed != ev.FailedRuns { // N183: Rerun failures is offered only when there is one
			ev.FailedRuns, changed = failed, true
		}
	}
	if next := evaluationStatus(ev); next != ev.Status {
		ev.Status, changed = next, true
	}
	if h.decideGate(ev) {
		changed = true
	}
	if prev != schemas.EvaluationDone && h.fillResult(ev) { // N161: stored once when it finishes (evalresult.go)
		changed = true
	}
	if h.notifyFinished(ev, prev) { // evalnotify.go
		changed = true
	}
	autoHarvestRedTeam(h.env, ev, time.Now()) // Z04: successful attacks become pending regressions (redteam.go)
	if changed {
		_ = h.store().Save(ev) // a failed save only delays the stored status
	}
}

// evalStartLimit is how long an evaluation may stay queued with no child
// experiment before refresh calls it lost (the launch itself is bounded by
// evaluationLaunchTimeout).
const evalStartLimit = 5 * time.Minute

// evalRestartProblem is the problem of an evaluation whose background start
// was lost.
const evalRestartProblem = "The server restarted while this evaluation was starting. Start it again."

// startingOrStuck handles a queued evaluation whose children are still
// starting in the background (N182): it is left as it is (true) while the
// start may still be running, and failed with evalRestartProblem (also
// true) once it is older than evalStartLimit with no child experiment.
func (h *evaluationsHandler) startingOrStuck(ev *schemas.Evaluation) bool {
	if ev.Status != schemas.EvaluationQueued {
		return false
	}
	for _, c := range ev.Children {
		if c.ExperimentID != "" {
			return false
		}
	}
	now := time.Now
	if h.now != nil {
		now = h.now
	}
	if now().Sub(ev.CreatedAt) > evalStartLimit {
		ev.Status, ev.Problem = schemas.EvaluationFailed, evalRestartProblem
		if ev.Launch != nil {
			ev.Launch.Current = ""
		}
		_ = h.store().Save(ev)
		return true
	}
	return ev.Launch != nil && ev.Launch.Started < ev.Launch.Of
}

// failedRunCount counts an experiment's counted runs that did not pass,
// plus its infra_error runs (N183).
func failedRunCount(runs []RunView) int {
	n := 0
	for _, rv := range countedRuns(runs) {
		if !rv.Sealed || rv.Run.Outcome == schemas.OutcomePass {
			continue
		}
		if rv.Slot.Counted || rv.Run.Outcome == schemas.OutcomeInfraError {
			n++
		}
	}
	return n
}

// decideGate judges the gate of an evaluation that just finished (lane X12,
// evalgate.go), once.
func (h *evaluationsHandler) decideGate(ev *schemas.Evaluation) bool {
	if ev.Spec.Gate == nil || ev.GateResult != nil || ev.Status != schemas.EvaluationDone {
		return false
	}
	evals, _ := h.store().List()
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	now := h.now
	if now == nil {
		now = time.Now
	}
	return gateEnv{env: h.env, root: h.root(), now: now}.decideGate(ctx, ev, evals)
}

// evaluationStatus is the evaluation's state from its children. Cancelled
// sticks. Running while any child is not finished; done when at least one
// child finished; failed when none did.
func evaluationStatus(ev *schemas.Evaluation) string {
	if ev.Status == schemas.EvaluationCancelled {
		return ev.Status
	}
	launched, done := 0, 0
	for _, c := range ev.Children {
		if c.Status == schemas.EvaluationChildSkipped {
			continue
		}
		launched++
		if !terminalChild(c.Status) {
			if c.Status == string(schemas.ExperimentCreatedState) && ev.Status == schemas.EvaluationQueued {
				continue
			}
			return schemas.EvaluationRunning
		}
		if c.Status == string(schemas.ExperimentDone) || c.BudgetStopped { // a budget stop keeps its finished runs: the report card shows them
			done++
		}
	}
	switch {
	case launched == 0:
		return schemas.EvaluationFailed
	case done > 0:
		return schemas.EvaluationDone
	case ev.Status == schemas.EvaluationQueued:
		return ev.Status
	}
	return schemas.EvaluationFailed
}

func (h *evaluationsHandler) create(ctx *fasthttp.RequestCtx) {
	s := h.env.Services
	if s.Projects == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "the project file store is not available on this server: an evaluation needs it to save its experiment files")
		return
	}
	if s.Launcher == nil {
		h.fail(ctx, fasthttp.StatusServiceUnavailable, "no experiment launcher on this server: hosted runs use the shared worker, ask your server owner to enable it")
		return
	}
	var spec schemas.EvaluationSpec
	dec := json.NewDecoder(strings.NewReader(string(ctx.PostBody())))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&spec); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, "parse evaluation: "+err.Error())
		return
	}
	work, cancel := workContext(ctx, evaluationLaunchTimeout)
	defer cancel()
	if err := h.normalize(work, &spec); err != nil {
		h.fail(ctx, statusOf(err), plainErr(err)) // N211: no internal "bad quickrun request" prefix
		return
	}
	if err := resolveEvaluationWorker(work, quickOrgID(ctx, s.OrgID), &spec); err != nil {
		h.fail(ctx, statusOf(err), plainErr(err))
		return
	}
	if platformRefusal != nil {
		if status, body, refused := platformRefusal(work, h, &spec); refused {
			lib.SendJSON(ctx, h.env.Mode, status, plainServed(body))
			return
		}
	}
	{ // G10 budgets, X01 own keys: refuse before anything is written
		var provs []string
		for _, a := range spec.Arms {
			provs = append(provs, a.Provider)
		}
		var keys KeySource
		if s.Keys != nil {
			keys = s.Keys
		}
		if err := ModelKeyRefusal(work, keys, quickOrgID(ctx, s.OrgID), provs, nil, s.Hosted); err != nil {
			if !SendModelKeyRefusal(ctx, h.env.Mode, err) {
				h.fail(ctx, fasthttp.StatusConflict, err.Error())
			}
			return
		}
	}
	// N187: every setup against every benchmark, before anything is saved.
	if why := h.checkArmsCompatible(work, &spec); why != "" {
		h.fail(ctx, fasthttp.StatusBadRequest, why)
		return
	}
	actor := auth.PrincipalFrom(ctx)
	who := ""
	if actor != nil {
		who = actor.Email
		if who == "" {
			who = actor.UserID
		}
	}
	ev := h.newEvaluation(spec, who, "")
	if err := h.store().Save(ev); err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, "save evaluation: "+err.Error())
		return
	}
	tenancy.SetCreated(ctx, ev.ID)
	// N182: answer at once; the children start in the background under the
	// caller's principal and the same launch timeout, and the evaluation is
	// saved after each one (Launch shows how far it got).
	bg, bgCancel := context.WithTimeout(context.WithoutCancel(context.Background()), evaluationLaunchTimeout)
	bg = withPrincipal(bg, ctx)
	answer := *ev
	if ev.Launch != nil {
		l := *ev.Launch // the background launch updates ev.Launch
		answer.Launch = &l
	}
	if !evalCreateInBackground {
		defer bgCancel()
		h.launchInBackground(bg, ev)
		answer = *ev
	} else {
		go func() {
			defer bgCancel()
			h.launchInBackground(bg, ev)
		}()
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, plainServed(&answer))
}

// evalCreateInBackground makes POST /evaluations answer before the children
// start (N182). The package's older tests read the children from the
// answer and turn it off (evaluations_hw13_test.go TestMain).
var evalCreateInBackground = true

// launchInBackground starts the children of a saved evaluation; a failure
// fails the evaluation with a plain problem, and a panic never takes the
// server down.
func (h *evaluationsHandler) launchInBackground(ctx context.Context, ev *schemas.Evaluation) {
	defer func() {
		if r := recover(); r != nil {
			ev.Status, ev.Problem = schemas.EvaluationFailed, fmt.Sprintf("Starting the evaluation failed: %v", r)
			_ = h.store().Save(ev)
		}
	}()
	if err := h.launchChildren(ctx, ctx, nil, ev); err != nil {
		ev.Status, ev.Problem = schemas.EvaluationFailed, plainErr(err)
		_ = h.store().Save(ev)
	}
}

// newEvaluation is a new queued evaluation of a normalized spec with no
// child started yet.
func (h *evaluationsHandler) newEvaluation(spec schemas.EvaluationSpec, who, scheduleName string) *schemas.Evaluation {
	now := h.now().UTC()
	return &schemas.Evaluation{
		ID:  "ev_" + now.Format("20060102_150405") + "_" + randHex(3),
		Org: h.env.Services.OrgID, Spec: spec, Status: schemas.EvaluationQueued,
		Children: []schemas.EvaluationChild{}, CreatedAt: now, CreatedBy: who, Schedule: scheduleName,
		Launch: &schemas.EvaluationLaunch{Of: len(spec.Benchmarks)},
	}
}

// launch saves a new evaluation of a normalized spec and starts one
// experiment per benchmark. start is the context experiments start under
// (it carries the caller's principal); created is told each new experiment.
func (h *evaluationsHandler) launch(work, start context.Context, created func(string), spec schemas.EvaluationSpec, who, scheduleName string) (*schemas.Evaluation, error) {
	ev := h.newEvaluation(spec, who, scheduleName)
	if err := h.store().Save(ev); err != nil {
		return nil, fmt.Errorf("save evaluation: %w", err)
	}
	if err := h.launchChildren(work, start, created, ev); err != nil {
		return nil, err
	}
	return ev, nil
}

// launchChildren starts one experiment per benchmark of a saved evaluation,
// saving the evaluation with its Launch progress before each child and
// after the last. An evaluation cancelled meanwhile stops starting children
// and cancels a child that started while it was cancelled.
func (h *evaluationsHandler) launchChildren(work, start context.Context, created func(string), ev *schemas.Evaluation) error {
	spec := ev.Spec
	agents := h.agentConfigs(work)
	budget := childBudget(spec)
	if ev.Launch == nil {
		ev.Launch = &schemas.EvaluationLaunch{}
	}
	ev.Launch.Of = len(spec.Benchmarks)
	for i, b := range spec.Benchmarks {
		if h.cancelledMeanwhile(ev) {
			return h.stopLaunch(start, ev)
		}
		ev.Launch.Started, ev.Launch.Current = i, b
		if err := h.store().Save(ev); err != nil {
			return fmt.Errorf("save evaluation: %w", err)
		}
		ev.Children = append(ev.Children, h.launchChild(work, start, created, ev, b, agents, budget, ev.CreatedBy))
	}
	ev.Launch.Started, ev.Launch.Current = len(spec.Benchmarks), ""
	if h.cancelledMeanwhile(ev) {
		return h.stopLaunch(start, ev)
	}
	ev.Status = evaluationStatus(&schemas.Evaluation{Status: schemas.EvaluationRunning, Children: ev.Children})
	if err := h.store().Save(ev); err != nil {
		return fmt.Errorf("save evaluation: %w", err)
	}
	return nil
}

// cancelledMeanwhile reports whether the stored evaluation was cancelled
// while its children were starting.
func (h *evaluationsHandler) cancelledMeanwhile(ev *schemas.Evaluation) bool {
	cur, err := h.store().Load(ev.ID)
	return err == nil && cur.Status == schemas.EvaluationCancelled
}

// stopLaunch cancels the children started so far and saves the evaluation
// cancelled.
func (h *evaluationsHandler) stopLaunch(start context.Context, ev *schemas.Evaluation) error {
	rc, _ := h.env.Services.Launcher.(RunController)
	for i := range ev.Children {
		c := &ev.Children[i]
		if c.ExperimentID == "" || terminalChild(c.Status) {
			continue
		}
		if rc != nil {
			_ = rc.Cancel(start, c.ExperimentID)
		}
		c.Status = string(schemas.ExperimentCancelled)
	}
	ev.Status = schemas.EvaluationCancelled
	ev.Launch.Current = ""
	if err := h.store().Save(ev); err != nil {
		return fmt.Errorf("save evaluation: %w", err)
	}
	return nil
}

// LaunchEvaluation starts an evaluation of spec for the org of env, as POST
// /evaluations does, without a request: a schedule repeats evaluations with
// it. who is recorded as the creator and scheduleName on the evaluation.
// It stays synchronous: it returns once every child was handed over.
func LaunchEvaluation(ctx context.Context, env OrgEnv, spec schemas.EvaluationSpec, who, scheduleName string) (*schemas.Evaluation, error) {
	h := &evaluationsHandler{env: env, now: time.Now}
	s := env.Services
	if s.Projects == nil {
		return nil, errors.New("the project file store is not available on this server: an evaluation needs it to save its experiment files")
	}
	if s.Launcher == nil {
		return nil, errors.New("no experiment launcher on this server: hosted runs use the shared worker, ask your server owner to enable it")
	}
	work, cancel := context.WithTimeout(ctx, evaluationLaunchTimeout)
	defer cancel()
	if err := h.normalize(work, &spec); err != nil {
		return nil, err
	}
	if s.Keys != nil {
		var provs, ids []string
		for _, a := range spec.Arms {
			provs, ids = append(provs, a.Provider), append(ids, "")
		}
		if err := KeyBudgetRefusal(work, s.Keys, s.OrgID, provs, ids); err != nil {
			return nil, err
		}
	}
	return h.launch(work, ctx, nil, spec, who, scheduleName)
}

// normalize validates spec, fills defaults and resolves the pack into the
// benchmark list.
func (h *evaluationsHandler) normalize(ctx context.Context, spec *schemas.EvaluationSpec) error {
	spec.Name = strings.TrimSpace(spec.Name)
	if len(spec.Name) > 120 {
		return badf("name is longer than 120 characters")
	}
	if len(spec.Arms) == 0 {
		return badf("arms is empty: pick an agent")
	}
	if spec.Mode == "" {
		spec.Mode = schemas.EvaluationModeScore
		if len(spec.Arms) == 2 {
			spec.Mode = schemas.EvaluationModeModels
		}
	}
	if err := h.ensureBareModel(ctx, spec); err != nil { // G4-21: the baseline's files, before the arms are read
		return err
	}
	switch spec.Mode {
	case schemas.EvaluationModeScore:
		if len(spec.Arms) != 1 {
			return badf("mode score takes 1 arm, got %d", len(spec.Arms))
		}
	case schemas.EvaluationModeVersions, schemas.EvaluationModeModels, schemas.EvaluationModeOpponent:
		if len(spec.Arms) != 2 {
			return badf("mode %s takes 2 arms, got %d", spec.Mode, len(spec.Arms))
		}
	case schemas.EvaluationModeModelSwitch: // Z21: one agent on several models (modelswitch.go)
		if err := checkModelSwitchArms(spec.Arms); err != nil {
			return err
		}
	default:
		return badf("mode %q is not one of score, versions, models, opponent, model_switch", spec.Mode)
	}
	if spec.BifrostCache {
		if spec.Mode != schemas.EvaluationModeScore {
			return badf("Bifrost cache on vs off takes one agent (mode score): the server adds the cache-on arm")
		}
		if spec.Stream != nil {
			return badf("Bifrost cache on vs off cannot be combined with memory carried from task to task")
		}
	}
	for i := range spec.Arms {
		a := &spec.Arms[i]
		if a.Label == "" {
			a.Label = string(rune('A' + i))
		}
		if !evalArmIDRe.MatchString(a.Label) {
			return badf("setup label %q is not allowed: use a capital letter followed by letters or digits, for example A or B", a.Label)
		}
		if i == 1 && a.Label == spec.Arms[0].Label {
			return badf("the two arms have the same label %q", a.Label)
		}
		for j := 1; j < i; j++ {
			if spec.Arms[j].Label == a.Label {
				return badf("arms %d and %d have the same label %q", j+1, i+1, a.Label)
			}
		}
		a.Model, a.Provider = strings.TrimSpace(a.Model), strings.TrimSpace(a.Provider)
		if a.Model != "" && !quickModelRe.MatchString(a.Model) {
			return badf("arm %s: model %q is not a valid model name", a.Label, a.Model)
		}
		if a.Provider != "" && !quickNameRe.MatchString(a.Provider) {
			return badf("arm %s: provider %q is not a valid provider name", a.Label, a.Provider)
		}
		if a.Version < 0 {
			return badf("arm %s: version must be 1 or more", a.Label)
		}
		path, err := normalizeAgent(a.Agent)
		if err != nil {
			return err
		}
		a.Agent = strings.TrimSuffix(strings.TrimPrefix(path, "agents/"), ".yaml")
		doc, _, err := h.env.Services.Projects.Read(ctx, projectfs.AreaAgents, a.Agent+".yaml")
		if err != nil {
			if errors.Is(err, projectfs.ErrNotFound) {
				return fmt.Errorf("%w: agent config %s does not exist: register your agent on the Agents page first", errQuickMissing, path)
			}
			return fmt.Errorf("read agent config %s: %w", path, err)
		}
		if a.Model == "" { // J2: blank means the agent's own model, for agents that have one
			var cfg schemas.AgentConfig
			if yaml.Unmarshal(doc, &cfg) != nil || !utils.OwnsModel(&cfg) {
				return badf("arm %s: pick a model: agent %s runs on the model you give it", a.Label, a.Agent)
			}
			a.Provider = ""
		}
		if a.Version == 0 {
			a.Version, a.LatestAtLaunch = h.latestVersion(ctx, a.Agent, doc), true
			if a.Version == 0 {
				a.LatestAtLaunch = false // the config could not be versioned: the run still uses the current file
			}
		} else {
			a.LatestAtLaunch = false
		}
	}
	if len(spec.Arms) == 2 && spec.Arms[0].Agent == spec.Arms[1].Agent && spec.Arms[0].Version == spec.Arms[1].Version &&
		spec.Arms[0].Model == spec.Arms[1].Model && spec.Arms[0].Provider == spec.Arms[1].Provider {
		return badf("both sides run the same agent, version and model: change one of them")
	}
	if err := h.normalizeSplit(spec); err != nil {
		return err
	}
	if err := normalizeFirstLook(spec); err != nil { // lane Z01 (evaluationpreset.go)
		return err
	}
	if err := normalizeMCPRoute(spec); err != nil {
		return err
	}
	if spec.TasksPerBenchmark == 0 {
		spec.TasksPerBenchmark = EvaluationDefaultTasks
	}
	if spec.Trials == 0 {
		spec.Trials = EvaluationDefaultTrials
	}
	if spec.TasksPerBenchmark < 1 || spec.TasksPerBenchmark > EvaluationMaxTasks {
		return badf("tasks_per_benchmark must be between 1 and %d", EvaluationMaxTasks)
	}
	if spec.Trials < 1 || spec.Trials > EvaluationMaxTrials {
		return badf("trials must be between 1 and %d", EvaluationMaxTrials)
	}
	if spec.BudgetUSD < 0 || spec.BudgetUSD > EvaluationMaxBudget {
		return badf("budget_usd must be between 0 and %v", EvaluationMaxBudget)
	}
	if n := max(len(spec.Benchmarks), 1); spec.BudgetUSD > 0 && spec.BudgetUSD < EvaluationMinBenchmarkBudget*float64(n)-1e-9 {
		return badf("%s", budgetFloorMessage(n)) // N190: a share under a cent was rounded to 0, which meant no cap
	}
	if spec.Concurrency < 0 || spec.Concurrency > EvaluationMaxConcurrency {
		return badf("concurrency must be between 1 and %d (0 is the default, %d)", EvaluationMaxConcurrency, EvaluationDefaultConcurrency)
	}
	if err := normalizePreset(spec); err != nil {
		return err
	}
	if err := normalizeGate(spec); err != nil {
		return err
	}
	if spec.Stream != nil {
		if spec.Stream.FreshArm != "" {
			return badf("stream.fresh_arm is chosen by the evaluation: in score mode a no-memory control of the agent is added")
		}
		if err := spec.Stream.Validate(nil); err != nil {
			return badf("%v", err)
		}
	}
	if err := normalizeFailover(spec); err != nil {
		return err
	}
	fs := []string{faults.Clean}
	for _, f := range spec.Faults {
		if f = strings.TrimSpace(f); f != "" && !contains(fs, f) {
			fs = append(fs, f)
		}
	}
	spec.Faults = fs

	var benches []string
	for _, b := range spec.Benchmarks {
		if b = strings.TrimSpace(b); b != "" && !contains(benches, b) {
			if !quickSuiteRe.MatchString(b) {
				return badf("benchmark %q is not a valid benchmark id", b)
			}
			benches = append(benches, b)
		}
	}
	if spec.Pack = strings.TrimSpace(spec.Pack); spec.Pack != "" {
		var available []string
		if src := h.env.Services.Suites; src != nil {
			ids, err := src.SuiteIDs(ctx)
			if err != nil {
				return fmt.Errorf("list benchmarks: %w", err)
			}
			available = ids
		}
		got := packs.Resolve(spec.Pack, available)
		if len(got) == 0 {
			return badf("pack %q has no benchmark on this server", spec.Pack)
		}
		ready := h.packReady(ctx, got, evalSplit(spec.Split))
		if len(ready) == 0 {
			return badf("pack %q has no benchmark with a ready task yet", spec.Pack)
		}
		for _, b := range ready {
			if !contains(benches, b) {
				benches = append(benches, b)
			}
		}
	}
	if len(benches) == 0 {
		return badf("pick at least one benchmark or a pack")
	}
	if len(benches) > EvaluationMaxBenchmarks {
		return badf("at most %d benchmarks per evaluation, got %d", EvaluationMaxBenchmarks, len(benches))
	}
	spec.Benchmarks = benches
	return nil
}

// packReady keeps the pack benchmarks an evaluation can run: not the
// practice suite on a hosted server (benchcatalog.HidePracticeSuite), and
// with at least one ready task of the split (admittedTasks). A benchmark
// that does not load stays: the launch skips it with the reason. Benchmarks
// named one by one are never dropped here.
func (h *evaluationsHandler) packReady(ctx context.Context, ids []string, split string) []string {
	src := h.env.Services.Suites
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if benchcatalog.HidePracticeSuite(h.env.Services.Hosted, id) {
			continue
		}
		if src != nil {
			if st, err := src.LoadSuite(ctx, id); err == nil && st != nil && len(admittedTasks(st, split).IDs) == 0 {
				continue
			}
		}
		out = append(out, id)
	}
	return out
}

// latestVersion resolves "latest" for agent id at launch (N6): the current
// agents/<id>.yaml is recorded as a version when it is new (a save normally
// already did), and that version's number is returned; 0 when it cannot be
// versioned.
func (h *evaluationsHandler) latestVersion(ctx context.Context, id string, doc []byte) int {
	store := h.env.Services.Projects
	if store == nil || !agentversions.ValidID(id) {
		return 0
	}
	v, _, err := agentversions.Record(store.Root(), id, agentFileHistory(ctx, store, id+".yaml", doc, "", ""))
	if err != nil {
		return 0
	}
	return v.Version
}

var gateCapRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// normalizeGate checks the gate settings; a gate with no rule is dropped.
func normalizeGate(spec *schemas.EvaluationSpec) error {
	g := spec.Gate
	if g == nil {
		return nil
	}
	if g.BaselineVersion < 0 {
		return badf("gate: baseline_version must be 0 (the live version) or more")
	}
	if g.MaxRegression < 0 || g.MaxRegression > 1 {
		return badf("gate: max_regression must be between 0 and 1 (0.05 is 5 points)")
	}
	for c, v := range g.MinScore {
		if !gateCapRe.MatchString(c) {
			return badf("gate: %q is not a capability name", c)
		}
		if v < 0 || v > 1 {
			return badf("gate: min_score for %s must be between 0 and 1", c)
		}
	}
	if err := normalizeGateSlices(g); err != nil { // Y13: metadata slice rules (evalgate_slices.go)
		return err
	}
	if len(g.MinScore) == 0 && g.MaxRegression == 0 && len(g.Slices) == 0 {
		spec.Gate = nil
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// childBudget splits the evaluation budget over the benchmarks; with none
// set, each child gets the QuickRun default.
func childBudget(spec schemas.EvaluationSpec) float64 {
	if spec.BudgetUSD <= 0 || len(spec.Benchmarks) == 0 {
		return QuickRunDefaultBudget
	}
	return microDollars(spec.BudgetUSD / float64(len(spec.Benchmarks))) // N190: never rounded down to a 0 cap
}

// agentConfigs maps agents/<id>.yaml to its decoded config, when the server
// has an agent source.
func (h *evaluationsHandler) agentConfigs(ctx context.Context) map[string]*schemas.AgentConfig {
	out := map[string]*schemas.AgentConfig{}
	if h.env.Services.Agents == nil {
		return out
	}
	files, err := h.env.Services.Agents.AgentConfigs(ctx)
	if err != nil {
		return out
	}
	for _, f := range files {
		if f.Err == nil && f.Config != nil {
			out[f.Path] = f.Config
		}
	}
	return out
}

// launchChild builds, saves, plans, creates and starts the experiment of one
// benchmark. Every problem becomes a skipped child; it never fails the
// evaluation as a whole.
func (h *evaluationsHandler) launchChild(work context.Context, start context.Context, created func(string), ev *schemas.Evaluation, bench string, agents map[string]*schemas.AgentConfig, budget float64, who string) schemas.EvaluationChild {
	s := h.env.Services
	child := schemas.EvaluationChild{Benchmark: bench, BudgetUSD: budget}
	skip := func(format string, a ...any) schemas.EvaluationChild {
		child.Status, child.Skipped = schemas.EvaluationChildSkipped, fmt.Sprintf(format, a...)
		return child
	}
	spec := ev.Spec
	suiteRef := bench
	suiteID, _, _ := strings.Cut(bench, "@")
	var st *schemas.Suite
	if s.Suites != nil {
		loaded, err := s.Suites.LoadSuite(work, suiteID)
		if err != nil {
			if errors.Is(err, schemas.ErrNotFound) {
				return skip("benchmark %s does not exist on this server", suiteID)
			}
			return skip("load benchmark %s: %v", suiteID, err)
		}
		st = loaded
		if !strings.Contains(bench, "@") && st.Version != "" {
			suiteRef = suiteID + "@" + st.Version
		}
	}
	for _, a := range spec.Arms {
		if st == nil {
			continue // no suite loader here: the planner checks
		}
		if ok, missing, reason := evalCompat(agents["agents/"+a.Agent+".yaml"], st); !ok {
			if reason == "" {
				reason = compat.Reason(a.Agent, suiteID, missing)
			}
			return skip("%s", reason)
		}
	}
	tasks := schemas.TaskSelector{All: true}
	split := evalSplit(spec.Split)
	if st != nil {
		pick := admittedTasks(st, split)
		child.SkippedTasks = pick.Skipped
		if len(pick.IDs) == 0 {
			if len(pick.Skipped) > 0 {
				return skip("benchmark %s has no %s task left to run: every admitted task is quarantined", suiteID, splitWords(split))
			}
			if split == schemas.SplitHeldOut {
				return skip("benchmark %s has no admitted task in its held-out split", suiteID)
			}
			return skip("benchmark %s has no admitted task yet", suiteID)
		}
		ids, why := runnableTasks(evalArmConfigs(spec.Arms, agents), st, pick.IDs) // J5: only tasks every arm can produce the output of
		if len(ids) == 0 {
			return skip("agent cannot run benchmark %s: %s", suiteID, why)
		}
		tasks = schemas.TaskSelector{IDs: spreadPick(ids, spec.TasksPerBenchmark)}
	}
	// Benchmarks whose tasks bring their own image need the agent built on
	// each task image: derive that variant per arm, or skip.
	variants := map[string]taskImageVariant{}
	for _, a := range spec.Arms {
		v := deriveTaskImageAgent(agents["agents/"+a.Agent+".yaml"], st, tasks.IDs, agents, s.Projects.Root())
		if v.Skip != "" {
			return skip("%s", v.Skip)
		}
		variants[a.Label] = v
	}
	condIDs, fskips := faults.Conditions(spec.Faults, st)
	for _, fs := range fskips { // D10: the faults that do not apply here, for the pages
		child.FaultsSkipped = append(child.FaultsSkipped, schemas.FaultSkip{Fault: fs.Fault, Reason: fs.Reason})
	}
	if len(condIDs) == 0 || condIDs[0] != schemas.ConditionClean {
		condIDs = append([]string{schemas.ConditionClean}, condIDs...)
	}
	conds := make([]quickCondition, 0, len(condIDs))
	faultOf := faults.Map(spec.Faults, st)
	child.FaultOf = map[string]string{}
	for _, id := range condIDs {
		conds = append(conds, quickCondition{ID: id, Tasks: tasks, Trials: spec.Trials, Split: spec.Split})
		if f, ok := faultOf[id]; ok {
			child.FaultOf[id] = f
		}
	}

	slug := strings.NewReplacer(".", "-", "_", "-").Replace(suiteID)
	name := "eval-" + strings.TrimPrefix(ev.ID, "ev_") + "-" + slug
	name = strings.ReplaceAll(name, "_", "-")
	reserve := h.childReserve(work, spec, bench, agents, budget, evalArmCount(spec)*len(tasks.IDs)*spec.Trials*len(conds)) // N190: one run's expected cost
	f := quickFile{
		Name: name, Suite: suiteRef, Base: "agents/" + spec.Arms[0].Agent + ".yaml",
		Arms:        evalArms(spec),
		Primary:     schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:      []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions:  conds,
		Budget:      schemas.BudgetSpec{TotalUSD: budget, ReserveUSD: reserve},
		Verdict:     quickVerdict{Policy: "paired_signflip"},
		Seed:        ev.CreatedAt.Unix(),
		Concurrency: evaluationConcurrency(spec), ManifestMode: "agent_exposure",
	}
	f.Budget.Disclose = spec.DiscloseBudget
	if st != nil && st.Simulator != nil && st.Simulator.Provider != "" && redteam.IsSuite(st.Root) {
		// Z04: readiness checks play a red-team benchmark's seed attacks;
		// an evaluation runs the adaptive attacker on the pinned model.
		sim := *st.Simulator
		sim.Mode = schemas.SimulatorLLM
		if sim.MaxTurns == 0 {
			sim.MaxTurns = simuser.DefaultMaxTurns
		}
		if sim.PromptVersion == "" {
			sim.PromptVersion = simuser.PromptUserV1
		}
		f.Simulator = &sim
	}
	var variantNotes []string
	for _, a := range spec.Arms {
		v := variants[a.Label]
		if v.Set == nil {
			continue
		}
		if arm, ok := f.Arms[a.Label]; ok {
			f.Arms[a.Label] = withSet(arm, v.Set)
		}
		variantNotes = append(variantNotes, "arm "+a.Label+": "+v.Note)
	}
	// Score mode variants (D5, D6): the cache-on copy, the failover arm and
	// the no-memory control of the one arm, the first of them as arm B so
	// the paired verdict plans (withVariantArms).
	var fresh string
	f.Arms, fresh = withVariantArms(spec, f.Arms)
	if spec.Stream != nil {
		st := *spec.Stream
		st.FreshArm = fresh
		f.Stream = &st
	}
	if _, _, err := s.Projects.Read(work, projectfs.AreaPolicies, strings.TrimPrefix(quickRunPolicy, "policies/")); err == nil {
		f.Policy = quickRunPolicy
	}
	body, err := marshalQuickFile(f)
	if err == nil {
		body, err = appendEvalID(body, ev.ID)
	}
	if err == nil {
		body, err = appendRunnerPool(body, spec.Worker)
	}
	if err != nil {
		return skip("encode experiment: %v", err)
	}
	var notes []string
	for _, fs := range fskips {
		notes = append(notes, fs.Fault+": "+fs.Reason)
	}
	head := fmt.Sprintf("# Evaluation %s: benchmark %s, %d task(s) x %d trial(s).\n", ev.ID, suiteRef, len(tasks.IDs), spec.Trials)
	if len(notes) > 0 {
		head += "# Faults skipped: " + strings.Join(notes, "; ") + "\n"
	}
	for _, n := range variantNotes {
		head += "# Task images: " + n + "\n"
	}
	data := append([]byte(head), body...)
	exp, _, err := DecodeExperiment("application/yaml", data)
	if err != nil {
		return skip("built an unreadable experiment: %v", err)
	}
	m, err := s.Launcher.Plan(work, exp)
	if err == nil && m == nil {
		err = errors.New("the planner returned no manifest")
	}
	if err != nil {
		return skip("%s", planSkipReason(err))
	}
	if plan := BuildPlan(m); len(plan.Errors) > 0 {
		return skip("%s", planSentences(plan.Errors))
	}
	if err := os.MkdirAll(filepath.Join(s.Projects.Root(), "experiments"), 0o755); err != nil {
		return skip("create the experiments folder: %v", err)
	}
	if _, err := s.Projects.Write(work, projectfs.AreaExperiments, name+".yaml", data,
		projectfs.WriteOpts{Actor: who, Note: "evaluation " + ev.ID}); err != nil {
		return skip("save experiments/%s.yaml: %v", name, err)
	}
	if err := s.Launcher.Create(work, m); err != nil {
		return skip("create experiment %s: %v", m.ExpID, err)
	}
	if created != nil {
		created(m.ExpID)
	}
	child.ExperimentID, child.Status = m.ExpID, string(schemas.ExperimentCreatedState)
	prewarmImages(s.Launcher, m)                   // G4-04: pull or build the images now, not at the first run
	reportcard.ChildDatasetFromManifest(&child, m) // Y07: the benchmark version and tasks this child ran
	if err := s.Launcher.Start(start, m.ExpID); err != nil {
		child.Status, child.Skipped = string(schemas.ExperimentFailed), "start experiment (it was created; start it from its page): "+err.Error()
		return child
	}
	child.Status = string(schemas.ExperimentRunning)
	return child
}

// evalArmFor is armFor, or for a blank model (J2) an arm that sets no model
// and is labelled with the agent's own model, so the report card shows it
// as such and no cost is computed for a model the agent never called.
func evalArmFor(provider, model string) schemas.ArmSpec {
	if model == "" {
		return schemas.ArmSpec{Label: utils.OwnModelLabel, Set: map[string]any{}}
	}
	return armFor(&QuickRunModel{Provider: provider, Model: model})
}

// appendEvalID adds the evaluation_id line marshalQuickFile cannot carry.
func appendEvalID(body []byte, id string) ([]byte, error) {
	if !evalstore.ValidID(id) {
		return nil, fmt.Errorf("bad evaluation id %q", id)
	}
	return append(body, []byte("evaluation_id: "+id+"\n")...), nil
}

// evalControlArm is the arm id of a score-mode stream's no-memory control.
const evalControlArm = "control"

// evalStreamControl reports whether the evaluation adds a no-memory control
// arm: score mode (one arm) with memory carried from task to task.
func evalStreamControl(spec schemas.EvaluationSpec) bool {
	return len(spec.Arms) == 1 && spec.Stream != nil && spec.Stream.Memory == schemas.StreamMemoryCarry
}

// evalArmCount is the experiment arms one benchmark of spec runs: one per
// spec arm, plus the variant arms of a score evaluation (withVariantArms).
func evalArmCount(spec schemas.EvaluationSpec) int {
	arms, _ := withVariantArms(spec, evalArms(spec))
	return len(arms)
}

// streamControlArm is base without memory: the same agent and model,
// labelled "<base label> (no memory)", told apart by the control marker env
// var so the planner sees two configs while the version stays one.
func streamControlArm(base schemas.ArmSpec) schemas.ArmSpec {
	ctl := withSet(base, map[string]any{"env." + schemas.StreamControlEnv: "1"})
	ctl.Label = base.Label + schemas.StreamControlSuffix
	return ctl
}

// cacheOnArm is base with Bifrost's semantic cache engaged: direct mode
// (exact request match), keyed per task so one task's answers are never
// served to another, entries kept 1 hour. The planner refuses the arm when
// the gateway is not Bifrost or its semantic_cache plugin is not loaded
// without a default_cache_key (engine/bifrostfeatures.go).
func cacheOnArm(base schemas.ArmSpec) schemas.ArmSpec {
	on := withSet(base, map[string]any{"gateway.bifrost": map[string]any{
		"semantic_cache": map[string]any{
			"mode":  schemas.SemanticCacheDirect,
			"scope": schemas.SemanticCacheScopeTask,
			"ttl":   "1h",
		},
	}})
	on.Label = base.Label + schemas.EvaluationCacheSuffix
	return on
}

// evaluationPoolResolver resolves the worker an evaluation runs on (hosted
// bring-your-own-worker, byoworker.Service.EvaluationPool): the worker it
// names, else the org's default; "" is the shared worker. nil off the
// hosted platform.
var evaluationPoolResolver atomic.Value // func(ctx, orgID, worker string) (string, error)

// SetEvaluationPoolResolver installs the hosted worker resolver.
func SetEvaluationPoolResolver(f func(ctx context.Context, orgID, worker string) (string, error)) {
	evaluationPoolResolver.Store(f)
}

// EvaluationPool is the runner pool runs of org go to when they name
// worker (a worker's name, "shared", or "" for the org's default): the
// hosted resolver's answer, "" (the shared worker) without one. Improvement
// runs on a hosted server route their experiments with it.
func EvaluationPool(ctx context.Context, orgID, worker string) (string, error) {
	f, _ := evaluationPoolResolver.Load().(func(context.Context, string, string) (string, error))
	if f == nil || orgID == "" {
		return "", nil
	}
	return f(ctx, orgID, worker)
}

// resolveEvaluationWorker replaces spec.Worker with the runner pool the
// evaluation's experiments go to, so the saved evaluation records where it
// ran. Without a resolver (local mode) a named worker is refused.
func resolveEvaluationWorker(ctx context.Context, orgID string, spec *schemas.EvaluationSpec) error {
	spec.Worker = strings.TrimSpace(spec.Worker)
	f, _ := evaluationPoolResolver.Load().(func(context.Context, string, string) (string, error))
	if f == nil || orgID == "" {
		if spec.Worker != "" && spec.Worker != schemas.DefaultRunnerPool && spec.Worker != schemas.SharedRunnerPool {
			return badf("worker %q: choosing a worker needs the hosted platform; this server runs evaluations itself", spec.Worker)
		}
		spec.Worker = ""
		return nil
	}
	pool, err := f(ctx, orgID, spec.Worker)
	if err != nil {
		return badf("%v", err)
	}
	spec.Worker = pool
	return nil
}

// appendRunnerPool adds the runner block of an evaluation's worker; the
// shared worker ("") adds nothing, so those experiments hash as before.
func appendRunnerPool(body []byte, pool string) ([]byte, error) {
	if pool == "" {
		return body, nil
	}
	if !schemas.ValidPoolName(pool) {
		return nil, fmt.Errorf("bad worker pool %q", pool)
	}
	return append(body, []byte("runner:\n  pool: "+pool+"\n")...), nil
}

// evalArms turns the spec's arms into experiment arms, one per spec arm
// (withVariantArms adds a score evaluation's variants).
// Score mode is one arm scored on its own (no hidden second run): the
// planner plans it and the verdict gives a score with an interval.
func evalArms(spec schemas.EvaluationSpec) map[string]schemas.ArmSpec {
	out := map[string]schemas.ArmSpec{}
	base := spec.Arms[0].Agent
	for _, a := range spec.Arms {
		arm := bareModelArm(evalArmFor(a.Provider, a.Model), a.Agent, a.Provider, a.Model)
		agentLabel := a.Agent
		if a.Version > 0 {
			agentLabel += fmt.Sprintf(" v%d", a.Version)
		}
		arm.Label = agentLabel + " on " + arm.Label
		// A version resolved from "latest" at launch is the current file, so
		// the run reads the file itself; an explicitly pinned version reads
		// its frozen snapshot.
		pinned := a.Version > 0 && !a.LatestAtLaunch
		if a.Agent != base || pinned {
			arm.Agent = "agents/" + a.Agent + ".yaml"
			if pinned {
				arm.Agent += fmt.Sprintf("@v%d", a.Version)
			}
		}
		out[a.Label] = withSet(arm, mcpRouteSet(spec.MCPRoute))
	}
	return out
}

// normalizeMCPRoute keeps spec.MCPRoute empty for the direct route (so
// evaluations that never chose one hash as before) and refuses unknown
// routes.
func normalizeMCPRoute(spec *schemas.EvaluationSpec) error {
	switch spec.MCPRoute = strings.TrimSpace(spec.MCPRoute); spec.MCPRoute {
	case "", schemas.MCPRouteDirect:
		spec.MCPRoute = ""
	case schemas.MCPRouteBifrost, schemas.MCPRouteBifrostCodeMode:
	default:
		return badf("mcp_route must be %s, %s or %s", schemas.MCPRouteDirect, schemas.MCPRouteBifrost, schemas.MCPRouteBifrostCodeMode)
	}
	return nil
}

// mcpRouteSet is the arm override of an MCP route: gateway.bifrost.mcp for
// the Bifrost routes (the planner then serves the run's MCP servers through
// Bifrost's MCP gateway, engine/bifrostfeatures.go), nothing for direct.
func mcpRouteSet(route string) map[string]any {
	switch route {
	case schemas.MCPRouteBifrost:
		return map[string]any{"gateway.bifrost.mcp.code_mode": false}
	case schemas.MCPRouteBifrostCodeMode:
		return map[string]any{"gateway.bifrost.mcp.code_mode": true}
	}
	return nil
}

// normalizeSplit keeps spec.Split empty for a public evaluation and
// held_out for an explicit held-out one; anything else is refused.
func (h *evaluationsHandler) normalizeSplit(spec *schemas.EvaluationSpec) error {
	switch spec.Split = strings.TrimSpace(spec.Split); spec.Split {
	case "", schemas.SplitPublic:
		spec.Split = ""
	case schemas.SplitHeldOut:
	default:
		return badf("split must be %s or %s", schemas.SplitPublic, schemas.SplitHeldOut)
	}
	return nil
}

// taskPick is what an evaluation may run on a suite: the admitted task ids
// of the evaluation's split that are not quarantined, sorted, and the
// quarantined ones left out, each with its reason.
type taskPick struct {
	IDs     []string
	Skipped []schemas.EvaluationSkippedTask
}

// admittedTasks picks the tasks an evaluation may run on st: admitted (the
// planner refuses a draft task in a locked suite; every task of an unlocked
// suite), of the given split only (a held-out task never runs in a public
// evaluation), and not quarantined. A quarantined task is left out alone
// with its reason, so one flaky task never skips the whole benchmark.
func admittedTasks(st *schemas.Suite, split string) taskPick {
	var q map[string]schemas.TaskQuarantine
	if st.Root != "" {
		q, _ = readQuarantineFile(st.Root) // an unreadable sidecar: the planner refuses with the reason
	}
	var pick taskPick
	ids := suiteTaskIDs(st)
	sort.Strings(ids)
	for _, id := range ids {
		t := st.TaskByID(id) // nil when only the ids are loaded (a suite head): public, and the planner screens it again
		if st.Lock != nil && !lockAdmits(st, id) {
			continue
		}
		if taskSplitOf(t) != split {
			continue
		}
		if r, ok := q[id]; ok {
			reason := strings.TrimSpace(r.Reason)
			if reason == "" {
				reason = "quarantined"
			} else {
				reason = "quarantined: " + reason
			}
			pick.Skipped = append(pick.Skipped, schemas.EvaluationSkippedTask{Task: id, Reason: reason})
			continue
		}
		pick.IDs = append(pick.IDs, id)
	}
	return pick
}

// evalSplit is the spec's split, public when unset.
func evalSplit(s string) string {
	if s == schemas.SplitHeldOut {
		return schemas.SplitHeldOut
	}
	return schemas.SplitPublic
}

// splitWords names a split in plain words.
func splitWords(split string) string {
	if split == schemas.SplitHeldOut {
		return "held-out"
	}
	return "public"
}

// taskSplitOf is a task's split, public when unset.
func taskSplitOf(t *schemas.Task) string {
	if t == nil || strings.TrimSpace(t.Split) == "" {
		return schemas.SplitPublic
	}
	return t.Split
}

// spreadPick takes n ids spread evenly over the sorted list, so the tasks
// come from across the benchmark rather than its first few.
func spreadPick(ids []string, n int) []string {
	if n >= len(ids) {
		return append([]string(nil), ids...)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, ids[i*len(ids)/n])
	}
	return out
}
