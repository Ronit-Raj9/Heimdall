package handlers

// Run control (audit/RESEARCH_R1.md items 1, 2, 6, 7 and 8): pause and
// resume an experiment (drain: in-flight runs finish, nothing new starts),
// live controls (concurrency, spend cap, paused models) with a hash-chained
// log, cancel or requeue one run, and "Rerun failures" as an exploratory
// child experiment. The controls are files in the experiment's store folder
// (bench/runctl); the launcher writes them and a running scheduler reads
// them within a second.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
)

// RunOperator is optionally implemented by a Launcher: it reads and changes
// an experiment's run controls. Every change is attributed to the request's
// principal and appended to the experiment's controls.jsonl.
type RunOperator interface {
	// Controls returns the state and the verified log (oldest first); a log
	// that does not verify is returned with an error wrapping
	// runctl.ErrBrokenLog.
	Controls(ctx context.Context, expID string) (*runctl.State, []runctl.Entry, error)
	Pause(ctx context.Context, expID, reason string) (*runctl.State, error)
	// Unpause clears a pause and reports whether there was one.
	Unpause(ctx context.Context, expID, reason string) (*runctl.State, bool, error)
	PatchControls(ctx context.Context, expID string, p runctl.Patch) (*runctl.State, error)
	// CancelRun stops the slot's in-flight run (sealed aborted_infra, never
	// counted, no attempt used) and holds the slot until it is requeued.
	CancelRun(ctx context.Context, expID, slotID, runID, reason string) (*runctl.State, error)
	// RequeueRun runs the slot again: it lifts a cancel, or supersedes the
	// newest run when it is aborted_infra or infra_error. It starts the
	// experiment's run when none is active.
	RequeueRun(ctx context.Context, expID, slotID, reason string) (*runctl.State, error)
}

// RunControls is GET /experiments/{id}/controls.
type RunControls struct {
	ExpID  string        `json:"exp_id"`
	State  *runctl.State `json:"state"`
	Paused bool          `json:"paused"`
	// InFlight counts runs started and not yet finished: what a pause waits
	// for.
	InFlight int `json:"in_flight"`
	// Models are the models the experiment runs (what paused_models takes).
	Models []string `json:"models"`
	// Concurrency and TotalUSD are the experiment's frozen values, which the
	// live settings override when set.
	Concurrency int     `json:"concurrency"`
	TotalUSD    float64 `json:"total_usd"`
	// Policy is the experiment's effective error policy table.
	Policy []schemas.ErrorPolicyRow `json:"policy"`
	// Log is the newest entries of controls.jsonl, oldest first.
	Log         []runctl.Entry `json:"log"`
	LogEntries  int            `json:"log_entries"`
	LogVerified bool           `json:"log_verified"`
	LogProblem  string         `json:"log_problem,omitempty"`
	// RerunOf names the parent of an exploratory rerun of failures.
	RerunOf string `json:"rerun_of,omitempty"`
	// Exploratory is true for a rerun of failures: its result decides
	// nothing and never joins the parent's verdict or a leaderboard.
	Exploratory bool `json:"exploratory"`
}

// RunControlRequest is the body of pause, resume, cancel and requeue.
type RunControlRequest struct {
	Reason string `json:"reason,omitempty"`
}

// RerunFailuresRequest is the body of POST /experiments/{id}/rerun-failures.
type RerunFailuresRequest struct {
	// Outcomes selects the counted runs to rerun; default every outcome that
	// is not pass.
	Outcomes []schemas.Outcome `json:"outcomes,omitempty"`
	Reason   string            `json:"reason,omitempty"`
}

// RerunFailuresResult is the answer of a rerun of failures.
type RerunFailuresResult struct {
	ParentID     string `json:"parent_id"`
	ExperimentID string `json:"experiment_id"`
	Tasks        int    `json:"tasks"`
	Slots        int    `json:"slots"`
	// ConfigChanged lists the arms whose agent config differs from the
	// parent's frozen one: the child plans today's files.
	ConfigChanged []string `json:"config_changed,omitempty"`
	Exploratory   bool     `json:"exploratory"`
	Started       bool     `json:"started"`
	StartError    string   `json:"start_error,omitempty"`
}

const maxControlLog = 200

func (h *ExperimentsHandler) operator(ctx *fasthttp.RequestCtx) (RunOperator, bool) {
	op, ok := h.launcher.(RunOperator)
	if !ok || h.launcher == nil {
		sendUnavailable(ctx, h.mode, "run controls (job runner)")
		return nil, false
	}
	return op, true
}

// decodeOptional decodes a JSON body into v when there is one.
func decodeOptional(ctx *fasthttp.RequestCtx, mode schemas.Mode, v any, shape string) bool {
	body := bytes.TrimSpace(ctx.PostBody())
	if len(body) == 0 {
		return true
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be "+shape+": "+err.Error())
		return false
	}
	return true
}

// sendControlError maps a run control error to its status.
func sendControlError(ctx *fasthttp.RequestCtx, mode schemas.Mode, what string, err error) {
	switch {
	case errors.Is(err, runctl.ErrInvalid):
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, runctl.ErrConflict):
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": "+err.Error())
	case errors.Is(err, fs.ErrNotExist):
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": "+err.Error())
	default:
		sendJobError(ctx, mode, what, err)
	}
}

// controlsView builds GET /controls for m.
func (h *ExperimentsHandler) controlsView(ctx context.Context, op RunOperator, m *schemas.Manifest, runs []RunView) (*RunControls, error) {
	st, log, err := op.Controls(ctx, m.ExpID)
	out := &RunControls{ExpID: m.ExpID, Models: manifestModels(m), Concurrency: m.Experiment.Concurrency,
		TotalUSD: m.Experiment.Budget.TotalUSD, Policy: schemas.ErrorPolicyTable(m.Experiment.ErrorPolicy),
		RerunOf: m.Experiment.RerunOf, Exploratory: m.Experiment.Exploratory(), LogVerified: true, Log: []runctl.Entry{}}
	switch {
	case errors.Is(err, runctl.ErrBrokenLog):
		out.LogVerified, out.LogProblem = false, err.Error()
	case err != nil:
		return nil, err
	}
	if st == nil {
		st = &runctl.State{Settings: runctl.Settings{Mode: runctl.ModeRunning}}
	}
	out.State, out.Paused = st, st.Paused()
	out.LogEntries = len(log)
	if len(log) > maxControlLog {
		log = log[len(log)-maxControlLog:]
	}
	if log != nil {
		out.Log = log
	}
	for _, rv := range runs {
		if !rv.Sealed {
			out.InFlight++
		}
	}
	return out, nil
}

// manifestModels lists the distinct models the arms run, sorted.
func manifestModels(m *schemas.Manifest) []string {
	out := []string{}
	for _, a := range m.Arms {
		if a.Config.Model != "" && !slices.Contains(out, a.Config.Model) {
			out = append(out, a.Config.Model)
		}
	}
	slices.Sort(out)
	return out
}

// getControls handles GET /experiments/{id}/controls.
func (h *ExperimentsHandler) getControls(ctx *fasthttp.RequestCtx) {
	op, ok := h.operator(ctx)
	if !ok {
		return
	}
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	out, err := h.controlsView(work, op, m, runs)
	if err != nil {
		sendControlError(ctx, h.mode, "read controls of "+m.ExpID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// patchControls handles PATCH /experiments/{id}/controls: live concurrency,
// spend cap and paused models. 200 with the controls.
func (h *ExperimentsHandler) patchControls(ctx *fasthttp.RequestCtx) {
	op, ok := h.operator(ctx)
	if !ok {
		return
	}
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	var p runctl.Patch
	if !decodeOptional(ctx, h.mode, &p, `{"concurrency"?, "total_usd"?, "paused_models"?, "reason"?}`) {
		return
	}
	if err := p.Validate(manifestModels(m)); err != nil {
		sendControlError(ctx, h.mode, "change controls of "+m.ExpID, err)
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if _, err := op.PatchControls(withPrincipal(work, ctx), m.ExpID, p); err != nil {
		sendControlError(ctx, h.mode, "change controls of "+m.ExpID, err)
		return
	}
	out, err := h.controlsView(work, op, m, runs)
	if err != nil {
		sendControlError(ctx, h.mode, "read controls of "+m.ExpID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, out)
}

// pauseExperiment handles POST /experiments/{id}/pause: drain. In-flight
// runs finish; no new run starts until resume. 202 with the controls.
func (h *ExperimentsHandler) pauseExperiment(ctx *fasthttp.RequestCtx) {
	op, ok := h.operator(ctx)
	if !ok {
		return
	}
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	var req RunControlRequest
	if !decodeOptional(ctx, h.mode, &req, `{"reason"?}`) {
		return
	}
	if st := h.status(m, runs, false); st.State != schemas.ExperimentRunning {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "experiment "+m.ExpID+" is not running ("+string(st.State)+"): only a running experiment can be paused")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if _, err := op.Pause(withPrincipal(work, ctx), m.ExpID, req.Reason); err != nil {
		sendControlError(ctx, h.mode, "pause experiment "+m.ExpID, err)
		return
	}
	out, err := h.controlsView(work, op, m, runs)
	if err != nil {
		sendControlError(ctx, h.mode, "read controls of "+m.ExpID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, out)
}

// runTarget resolves {run} to a run of the {id} experiment.
func (h *ExperimentsHandler) runTarget(ctx *fasthttp.RequestCtx, m *schemas.Manifest) (*RunView, bool) {
	runID, ok := idParam(ctx, h.mode, "run", "run")
	if !ok {
		return nil, false
	}
	rv, found := h.index.Run(runID)
	if !found || rv.Slot.ExpID != m.ExpID && rv.Run.ExpID != m.ExpID {
		sendNotFound(ctx, h.mode, "run of experiment "+m.ExpID, runID)
		return nil, false
	}
	return rv, true
}

// cancelRun handles POST /experiments/{id}/runs/{run}/cancel: stop one
// in-flight run. It is sealed aborted_infra, never counted, uses no attempt,
// and its slot waits for a requeue. 202 with the controls.
func (h *ExperimentsHandler) cancelRun(ctx *fasthttp.RequestCtx) {
	op, ok := h.operator(ctx)
	if !ok {
		return
	}
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	rv, ok := h.runTarget(ctx, m)
	if !ok {
		return
	}
	var req RunControlRequest
	if !decodeOptional(ctx, h.mode, &req, `{"reason"?}`) {
		return
	}
	if rv.Sealed {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "run "+rv.Run.RunID+" already finished ("+string(rv.Run.Outcome)+"); requeue it instead when it is aborted_infra or infra_error")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if _, err := op.CancelRun(withPrincipal(work, ctx), m.ExpID, rv.Slot.SlotID, rv.Run.RunID, req.Reason); err != nil {
		sendControlError(ctx, h.mode, "cancel run "+rv.Run.RunID, err)
		return
	}
	out, err := h.controlsView(work, op, m, runs)
	if err != nil {
		sendControlError(ctx, h.mode, "read controls of "+m.ExpID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, out)
}

// requeueRun handles POST /experiments/{id}/runs/{run}/requeue: run the
// run's slot again. Only the slot's newest run, and only when it is
// aborted_infra or infra_error (or was cancelled): the old run is kept,
// marked superseded, and uses no attempt. 202 with the controls.
func (h *ExperimentsHandler) requeueRun(ctx *fasthttp.RequestCtx) {
	op, ok := h.operator(ctx)
	if !ok {
		return
	}
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	rv, ok := h.runTarget(ctx, m)
	if !ok {
		return
	}
	var req RunControlRequest
	if !decodeOptional(ctx, h.mode, &req, `{"reason"?}`) {
		return
	}
	if rv.Superseded {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "run "+rv.Run.RunID+" was already requeued")
		return
	}
	for _, other := range runs {
		if other.Slot.SlotID == rv.Slot.SlotID && !other.Superseded && other.Run.Attempt > rv.Run.Attempt {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "run "+rv.Run.RunID+" is not the newest run of its slot ("+other.Run.RunID+" is)")
			return
		}
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if _, err := op.RequeueRun(withPrincipal(work, ctx), m.ExpID, rv.Slot.SlotID, req.Reason); err != nil {
		sendControlError(ctx, h.mode, "requeue run "+rv.Run.RunID, err)
		return
	}
	h.recorder.follow(m.ExpID)
	out, err := h.controlsView(work, op, m, h.index.Runs(m.ExpID))
	if err != nil {
		sendControlError(ctx, h.mode, "read controls of "+m.ExpID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, out)
}

// failedTasks maps each condition to the tasks whose counted run's outcome
// is selected (default: every outcome but pass). Compatibility slots never
// count.
func failedTasks(runs []RunView, outcomes []schemas.Outcome) (map[string][]string, int) {
	pick := func(o schemas.Outcome) bool {
		if len(outcomes) == 0 {
			return o != schemas.OutcomePass
		}
		return slices.Contains(outcomes, o)
	}
	byCond := map[string][]string{}
	n := 0
	for _, rv := range countedRuns(runs) {
		if !rv.Slot.Counted || !rv.Sealed || !pick(rv.Run.Outcome) {
			continue
		}
		if !slices.Contains(byCond[rv.Slot.Condition], rv.Slot.Task) {
			byCond[rv.Slot.Condition] = append(byCond[rv.Slot.Condition], rv.Slot.Task)
			n++
		}
	}
	for c := range byCond {
		slices.Sort(byCond[c])
	}
	return byCond, n
}

// RerunExperiment builds the exploratory child of m that runs again the
// tasks of its failed counted runs: the same arms, conditions, trials and
// budget, tasks narrowed to the failures, rerun_of set, no follow-up
// binding, no adaptive trials. ErrConflict when nothing failed.
func RerunExperiment(m *schemas.Manifest, runs []RunView, outcomes []schemas.Outcome, now time.Time) (*schemas.Experiment, int, error) {
	if m.Experiment.Matrix != nil {
		return nil, 0, fmt.Errorf("%w: experiment %s is a model matrix; rerun its failures from a new experiment", runctl.ErrInvalid, m.ExpID)
	}
	for _, o := range outcomes {
		if o == schemas.OutcomePass {
			return nil, 0, fmt.Errorf("%w: pass is not a failure", runctl.ErrInvalid)
		}
	}
	byCond, n := failedTasks(runs, outcomes)
	if n == 0 {
		return nil, 0, fmt.Errorf("%w: experiment %s has no finished run with a selected failure to rerun", runctl.ErrConflict, m.ExpID)
	}
	e := m.Experiment // a copy; slices below are rebuilt, never shared
	e.ID = ""
	base := strings.TrimSuffix(e.Name, "-failures")
	e.Name = fmt.Sprintf("%s-failures-%s", base, now.UTC().Format("20060102-150405"))
	e.RerunOf = m.ExpID
	e.Extends, e.Trials = nil, nil
	e.EvaluationID = ""
	var conds []schemas.ConditionSpec
	for _, c := range m.Experiment.Conditions {
		tasks := byCond[c.ID]
		if len(tasks) == 0 {
			continue
		}
		c.Tasks = schemas.TaskSelector{IDs: slices.Clone(tasks)}
		conds = append(conds, c)
	}
	if len(conds) == 0 {
		return nil, 0, fmt.Errorf("%w: the failed runs are of no planned condition", runctl.ErrConflict)
	}
	// The primary condition must stay planned for the verdict to read; keep
	// it with the same failing tasks of any condition when it had none.
	hasPrimary := slices.ContainsFunc(conds, func(c schemas.ConditionSpec) bool { return c.ID == e.Primary.Condition })
	if !hasPrimary {
		var all []string
		for _, ts := range byCond {
			for _, t := range ts {
				if !slices.Contains(all, t) {
					all = append(all, t)
				}
			}
		}
		slices.Sort(all)
		for _, c := range m.Experiment.Conditions {
			if c.ID == e.Primary.Condition {
				c.Tasks = schemas.TaskSelector{IDs: all}
				conds = append([]schemas.ConditionSpec{c}, conds...)
			}
		}
	}
	e.Conditions = conds
	return &e, n, nil
}

// launchRerun plans, creates and starts an exploratory rerun.
func launchRerun(work context.Context, ctx *fasthttp.RequestCtx, l Launcher, parent *schemas.Manifest, exp *schemas.Experiment, tasks int) (*RerunFailuresResult, error) {
	child, err := l.Plan(work, exp)
	if err == nil && child == nil {
		err = errors.New("the planner returned no manifest")
	}
	if err != nil {
		return nil, fmt.Errorf("plan the rerun: %w", err)
	}
	if plan := BuildPlan(child); len(plan.Errors) > 0 {
		return nil, fmt.Errorf("plan the rerun: %s", strings.Join(plan.Errors, "; "))
	}
	if err := l.Create(work, child); err != nil {
		return nil, fmt.Errorf("create the rerun: %w", err)
	}
	tenancy.SetCreated(ctx, child.ExpID)
	res := &RerunFailuresResult{ParentID: parent.ExpID, ExperimentID: child.ExpID, Tasks: tasks, Slots: len(child.Slots), Exploratory: true}
	for id, a := range child.Arms {
		if p, ok := parent.Arms[id]; ok && p.ConfigHash != a.ConfigHash {
			res.ConfigChanged = append(res.ConfigChanged, id)
		}
	}
	slices.Sort(res.ConfigChanged)
	if err := l.Start(withPrincipal(work, ctx), child.ExpID); err != nil {
		res.StartError = err.Error()
	} else {
		res.Started = true
	}
	return res, nil
}

// rerunFailures handles POST /experiments/{id}/rerun-failures: 202 with the
// new exploratory experiment. Its runs never join this experiment's verdict.
func (h *ExperimentsHandler) rerunFailures(ctx *fasthttp.RequestCtx) {
	if h.launcher == nil {
		sendUnavailable(ctx, h.mode, "experiment launcher")
		return
	}
	m, runs, ok := h.lookup(ctx)
	if !ok {
		return
	}
	var req RerunFailuresRequest
	if !decodeOptional(ctx, h.mode, &req, `{"outcomes"?, "reason"?}`) {
		return
	}
	if st := h.status(m, runs, false); st.State == schemas.ExperimentRunning {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "experiment "+m.ExpID+" is still running: rerun its failures once it has finished")
		return
	}
	exp, n, err := RerunExperiment(m, runs, req.Outcomes, time.Now())
	if err != nil {
		sendControlError(ctx, h.mode, "rerun failures of "+m.ExpID, err)
		return
	}
	work, cancel := workContext(ctx, evaluationLaunchTimeout)
	defer cancel()
	res, err := launchRerun(work, ctx, h.launcher, m, exp, n)
	if err != nil {
		lib.SendError(ctx, h.mode, statusOf(err), lib.ErrTypeBadRequest, err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, res)
}

// resumePaused clears a pause before POST /resume requeues anything: a
// paused experiment whose run is still active only needs the pause lifted.
// It reports true when the request is answered.
func (h *ExperimentsHandler) resumePaused(ctx *fasthttp.RequestCtx, m *schemas.Manifest, runs []RunView) bool {
	op, ok := h.launcher.(RunOperator)
	if !ok {
		return false
	}
	var req RunControlRequest
	if !decodeOptional(ctx, h.mode, &req, `{"reason"?}`) {
		return true
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	_, cleared, err := op.Unpause(withPrincipal(work, ctx), m.ExpID, req.Reason)
	if err != nil {
		sendControlError(ctx, h.mode, "resume experiment "+m.ExpID, err)
		return true
	}
	ar, isAR := h.launcher.(ActivityReporter)
	if cleared && isAR && ar.Active(m.ExpID) {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, h.status(m, runs, true))
		return true
	}
	return false
}
