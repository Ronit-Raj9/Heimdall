package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// Round 7 lane T3: the website side of the trace builder. A failure in the
// inbox becomes a task through the job kind tracetask.build (lane T1); these
// routes start it, read its state and result, and record a person's approval
// or rejection of the draft. Admission decides whether a draft is good; the
// approval route is the only code that sets provenance.instruction_reviewed
// for a built task, and only for a person with the member role.

// TraceTaskKind is the job kind lane T1 registers.
const TraceTaskKind = "tracetask.build"

// Routes.
const (
	TraceTaskBuildPath   = APIPrefix + "/inbox/{id}/build-task"
	TraceTaskApprovePath = APIPrefix + "/inbox/{id}/build-task/approve"
	TraceTaskRejectPath  = APIPrefix + "/inbox/{id}/build-task/reject"
)

// TraceTaskDefaultCapUSD is the cap the form starts with.
const TraceTaskDefaultCapUSD = 0.50

// TraceTaskMaxCapUSD refuses a cap above this per failure.
const TraceTaskMaxCapUSD = 10.0

// traceTaskGuardReviewed is the guard that waits for the person's approval.
const traceTaskGuardReviewed = "guard_instruction_clear"

// TraceTaskBuildRequest is the body of POST /inbox/{id}/build-task. Retry
// asks the builder once more with the failing controls of the latest job as
// feedback; it spends what is left of the latest job's cap.
type TraceTaskBuildRequest struct {
	Suite     string  `json:"suite"`
	Model     string  `json:"model"`
	BudgetUSD float64 `json:"budget_usd"`
	Retry     bool    `json:"retry,omitempty"`
}

// TraceTaskParams are the job's params (the contract's four fields, plus the
// retry feedback this lane adds).
type TraceTaskParams struct {
	InboxID   string  `json:"inbox_id"`
	Suite     string  `json:"suite"`
	Model     string  `json:"model"`
	BudgetUSD float64 `json:"budget_usd"`
	RetryOf   string  `json:"retry_of,omitempty"`
	Feedback  string  `json:"feedback,omitempty"`
}

// TraceTaskStarted answers POST /inbox/{id}/build-task.
type TraceTaskStarted struct {
	JobID string `json:"job_id"`
}

// TraceTaskQuote is one quote from the session (the tracedraft Quote shape).
type TraceTaskQuote struct {
	Text string `json:"text"`
	Turn int    `json:"turn,omitempty"`
	Role string `json:"role,omitempty"`
}

// TraceTaskFailure is what went wrong in the session.
type TraceTaskFailure struct {
	Kind          string           `json:"kind"`
	Summary       string           `json:"summary"`
	Quotes        []TraceTaskQuote `json:"quotes"`
	DroppedQuotes int              `json:"dropped_quotes,omitempty"`
}

// TraceTaskFile is one file of the task folder.
type TraceTaskFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// TraceTaskMutant is one wrong-agent variant.
type TraceTaskMutant struct {
	Name            string `json:"name"`
	WhatItDoesWrong string `json:"what_it_does_wrong"`
}

// TraceTaskStandin is the stand-in service, when replay was not enough.
type TraceTaskStandin struct {
	Why   string          `json:"why"`
	Files []TraceTaskFile `json:"files"`
}

// TraceTaskCheck is one control or guard result.
type TraceTaskCheck struct {
	Name   string `json:"name"`
	Want   string `json:"want,omitempty"`
	Got    string `json:"got"`
	Detail string `json:"detail,omitempty"`
}

// TraceTaskAdmission is admission's verdict on the draft.
type TraceTaskAdmission struct {
	Admitted bool             `json:"admitted"`
	Controls []TraceTaskCheck `json:"controls"`
	Guards   []TraceTaskCheck `json:"guards"`
}

// TraceTaskRef is where the draft was written (assumed field, see notes T3).
type TraceTaskRef struct {
	Suite string `json:"suite"`
	ID    string `json:"id"`
	Path  string `json:"path"` // under the benchmarks area, e.g. my-suite/tasks/refund-1
}

// TraceTaskResult is the job's result (contract of round 7).
type TraceTaskResult struct {
	Failure     TraceTaskFailure    `json:"failure"`
	Shape       string              `json:"shape"`
	ShapeReason string              `json:"shape_reason"`
	Files       []TraceTaskFile     `json:"files"`
	Mutants     []TraceTaskMutant   `json:"mutants"`
	Standin     *TraceTaskStandin   `json:"standin"`
	SpentUSD    float64             `json:"spent_usd"`
	ModelCalls  int                 `json:"model_calls"`
	Notes       []string            `json:"notes,omitempty"`
	Unsure      bool                `json:"unsure,omitempty"`
	Task        *TraceTaskRef       `json:"task,omitempty"`
	Admission   *TraceTaskAdmission `json:"admission,omitempty"`
}

// TraceTaskDecision is a person's approval or rejection of a draft.
type TraceTaskDecision struct {
	JobID    string    `json:"job_id"`
	Decision string    `json:"decision"` // approved or rejected
	By       string    `json:"by"`
	At       time.Time `json:"at"`
	Task     string    `json:"task,omitempty"`
}

// TraceTaskState answers GET /inbox/{id}/build-task.
type TraceTaskState struct {
	JobID      string             `json:"job_id,omitempty"`
	Status     string             `json:"status,omitempty"` // job status; "" when no job ever ran
	Step       string             `json:"step,omitempty"`   // what the running job is doing
	Error      string             `json:"error,omitempty"`
	Result     *TraceTaskResult   `json:"result,omitempty"`
	Decision   *TraceTaskDecision `json:"decision,omitempty"`
	CanApprove bool               `json:"can_approve"`
	Blocked    string             `json:"blocked,omitempty"` // why Approve is not offered
	CanRetry   bool               `json:"can_retry"`
	Failing    []string           `json:"failing,omitempty"` // controls and guards that did not hold
	Budget     float64            `json:"budget_usd,omitempty"`
}

// TraceTaskApproveRequest is the body of approve and reject.
type TraceTaskApproveRequest struct {
	JobID string `json:"job_id"`
}

// TraceTaskApproved answers approve.
type TraceTaskApproved struct {
	Decision TraceTaskDecision `json:"decision"`
	Guards   []TraceTaskCheck  `json:"guards"`
	Ready    bool              `json:"ready"`
}

var traceTaskIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type traceTaskHandler struct {
	env OrgEnv
	now func() time.Time
}

func init() {
	RegisterOrgArea("tracetask", func(env OrgEnv, r *router.Router) {
		h := &traceTaskHandler{env: env, now: time.Now}
		r.GET(TraceTaskBuildPath, env.R(h.get))
		r.POST(TraceTaskBuildPath, env.W(h.start))
		r.POST(TraceTaskApprovePath, env.W(h.approve))
		r.POST(TraceTaskRejectPath, env.W(h.reject))
	})
}

// caller is the org and principal; write routes need the member role (the
// role that may edit a benchmark).
func (h *traceTaskHandler) caller(ctx *fasthttp.RequestCtx, write bool) (string, *auth.Principal, bool) {
	if h.env.Services.Jobs == nil {
		sendUnavailable(ctx, h.env.Mode, "job runner")
		return "", nil, false
	}
	p, ok := auth.Lookup(ctx)
	if !ok {
		org := h.env.Services.OrgID
		if org == "" {
			org = auth.LocalOrgID
		}
		return org, auth.Local(), true
	}
	if !p.IsAuthenticated() || p.OrgID == "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "Sign in to build tasks.")
		return "", nil, false
	}
	if write && !p.Can(auth.RoleMember) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "Building or approving a task needs the member role. Ask an org admin.")
		return "", nil, false
	}
	return p.OrgID, p, true
}

func (h *traceTaskHandler) itemID(ctx *fasthttp.RequestCtx) (string, bool) {
	id := pathParam(ctx, "id")
	if !traceTaskIDRe.MatchString(id) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "That is not a failure id.")
		return "", false
	}
	return id, true
}

func (h *traceTaskHandler) decode(ctx *fasthttp.RequestCtx, v any) bool {
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "bad body: "+err.Error())
		return false
	}
	return true
}

// latest is the newest tracetask.build job of the item, nil when none.
func (h *traceTaskHandler) latest(work context.Context, org, item string) (*jobs.Job, error) {
	page, err := h.env.Services.Jobs.List(work, org, jobs.Filter{Kind: TraceTaskKind, Limit: 500})
	if err != nil {
		return nil, err
	}
	var best *jobs.Job
	for i := range page.Jobs {
		j := &page.Jobs[i]
		var p TraceTaskParams
		if json.Unmarshal(j.Params, &p) != nil || p.InboxID != item {
			continue
		}
		if best == nil || j.CreatedAt.After(best.CreatedAt) {
			best = j
		}
	}
	return best, nil
}

func (h *traceTaskHandler) start(ctx *fasthttp.RequestCtx) {
	mode := h.env.Mode
	org, p, ok := h.caller(ctx, true)
	if !ok {
		return
	}
	item, ok := h.itemID(ctx)
	if !ok {
		return
	}
	var req TraceTaskBuildRequest
	if !h.decode(ctx, &req) {
		return
	}
	req.Suite, req.Model = strings.TrimSpace(req.Suite), strings.TrimSpace(req.Model)
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	params := TraceTaskParams{InboxID: item, Suite: req.Suite, Model: req.Model, BudgetUSD: req.BudgetUSD}
	if req.Retry {
		prev, err := h.latest(work, org, item)
		if err != nil {
			sendDepError(ctx, mode, "jobs", err)
			return
		}
		st := h.state(prev, nil, true)
		if prev == nil || !st.CanRetry {
			lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "Try again is offered once, after admission refused a draft.")
			return
		}
		var pp TraceTaskParams
		_ = json.Unmarshal(prev.Params, &pp)
		left := pp.BudgetUSD - st.Result.SpentUSD
		if left < 0.01 {
			lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "The cap is spent. Start a new build with a new cap.")
			return
		}
		params = TraceTaskParams{InboxID: item, Suite: pp.Suite, Model: pp.Model, BudgetUSD: left, RetryOf: prev.ID,
			Feedback: "Admission refused the last draft: " + strings.Join(st.Failing, "; ")}
	}
	switch {
	case params.Suite == "":
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "Pick a benchmark for the task.")
		return
	case params.Model == "":
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "Pick a builder model.")
		return
	case params.BudgetUSD <= 0:
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "Set a cap above $0.")
		return
	case params.BudgetUSD > TraceTaskMaxCapUSD:
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, fmt.Sprintf("The cap is at most $%.2f per failure.", TraceTaskMaxCapUSD))
		return
	}
	if isBuiltinSuite(h.env.Services.Suites, params.Suite) {
		sendBuiltinReadOnly(ctx, mode, params.Suite)
		return
	}
	raw, err := json.Marshal(params)
	if err != nil {
		lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	j, err := h.env.Services.Jobs.Create(work, jobs.NewJob{OrgID: org, Kind: TraceTaskKind, Params: raw, CreatedBy: createdBy(p)})
	if err != nil {
		status, typ := fasthttp.StatusInternalServerError, lib.ErrTypeInternal
		if errors.Is(err, jobs.ErrInvalidParams) || errors.Is(err, jobs.ErrUnknownKind) {
			status, typ = fasthttp.StatusBadRequest, lib.ErrTypeBadRequest
		}
		lib.SendError(ctx, mode, status, typ, "Could not start the build: "+err.Error())
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusAccepted, TraceTaskStarted{JobID: j.ID})
}

func (h *traceTaskHandler) get(ctx *fasthttp.RequestCtx) {
	org, p, ok := h.caller(ctx, false)
	if !ok {
		return
	}
	item, ok := h.itemID(ctx)
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := h.latest(work, org, item)
	if err != nil {
		sendDepError(ctx, h.env.Mode, "jobs", err)
		return
	}
	dec := h.readDecision(work, item)
	if dec != nil && j != nil && dec.JobID != j.ID {
		dec = nil
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.state(j, dec, p.Can(auth.RoleMember)))
}

// state turns a job into what the page shows. It never marks a task good:
// it only reads admission's verdict.
func (h *traceTaskHandler) state(j *jobs.Job, dec *TraceTaskDecision, member bool) TraceTaskState {
	st := TraceTaskState{}
	if j == nil {
		return st
	}
	st.JobID, st.Status, st.Error, st.Decision = j.ID, string(j.Status), j.Error, dec
	var pp TraceTaskParams
	if json.Unmarshal(j.Params, &pp) == nil {
		st.Budget = pp.BudgetUSD
	}
	if len(j.Progress) > 0 {
		var pr struct {
			Step    string `json:"step"`
			Message string `json:"message"`
		}
		if json.Unmarshal(j.Progress, &pr) == nil {
			st.Step = pr.Step
			if st.Step == "" {
				st.Step = pr.Message
			}
		}
	}
	if j.Status != jobs.StatusSucceeded || len(j.Result) == 0 {
		st.Blocked = "The build has not finished."
		return st
	}
	var res TraceTaskResult
	if err := json.Unmarshal(j.Result, &res); err != nil {
		st.Error = "The build's result cannot be read: " + err.Error()
		st.Blocked = st.Error
		return st
	}
	st.Result = &res
	st.Failing = traceTaskFailing(&res)
	retried := pp.RetryOf != ""
	switch {
	case dec != nil:
		st.Blocked = "A person already decided on this draft."
	case res.Admission == nil:
		st.Blocked = "Admission has not run on this draft."
	case len(st.Failing) > 0:
		st.Blocked = "Admission refused this draft."
		st.CanRetry = !retried && member && res.SpentUSD < pp.BudgetUSD
	case res.Unsure:
		st.Blocked = "The builder was not sure about this draft. Read its notes."
	case res.Task == nil:
		st.Blocked = "The build did not say where it wrote the task."
	case !member:
		st.Blocked = "Approving needs the member role."
	default:
		st.CanApprove = true
	}
	return st
}

// traceTaskFailing lists the controls and guards that did not hold, leaving
// out the guard that waits for the approval itself.
func traceTaskFailing(res *TraceTaskResult) []string {
	var out []string
	if res.Admission == nil {
		return nil
	}
	for _, c := range res.Admission.Controls {
		if c.Want != "" && c.Got != c.Want {
			out = append(out, fmt.Sprintf("%s want %s got %s", c.Name, c.Want, c.Got))
		}
	}
	for _, g := range res.Admission.Guards {
		if g.Name != traceTaskGuardReviewed && g.Got != "pass" {
			out = append(out, g.Name+" "+g.Got)
		}
	}
	return out
}

// decisionRel is where the person's decision is kept (uploads area).
func decisionRel(item string) string { return path.Join("tracetask", item, "decision.json") }

func (h *traceTaskHandler) readDecision(work context.Context, item string) *TraceTaskDecision {
	st := h.env.Services.Projects
	if st == nil {
		return nil
	}
	data, _, err := st.Read(work, projectfs.AreaUploads, decisionRel(item))
	if err != nil {
		return nil
	}
	var d TraceTaskDecision
	if json.Unmarshal(data, &d) != nil {
		return nil
	}
	return &d
}

// decide loads the job named in the body and checks it is the item's latest.
func (h *traceTaskHandler) decide(ctx *fasthttp.RequestCtx) (context.Context, context.CancelFunc, *auth.Principal, string, TraceTaskState, bool) {
	org, p, ok := h.caller(ctx, true)
	if !ok {
		return nil, nil, nil, "", TraceTaskState{}, false
	}
	item, ok := h.itemID(ctx)
	if !ok {
		return nil, nil, nil, "", TraceTaskState{}, false
	}
	var req TraceTaskApproveRequest
	if !h.decode(ctx, &req) {
		return nil, nil, nil, "", TraceTaskState{}, false
	}
	if h.env.Services.Projects == nil {
		sendUnavailable(ctx, h.env.Mode, "project files")
		return nil, nil, nil, "", TraceTaskState{}, false
	}
	work, cancel := workContext(ctx, readTimeout)
	j, err := h.latest(work, org, item)
	if err != nil {
		cancel()
		sendDepError(ctx, h.env.Mode, "jobs", err)
		return nil, nil, nil, "", TraceTaskState{}, false
	}
	if j == nil || j.ID != strings.TrimSpace(req.JobID) {
		cancel()
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "This is not the latest build of this failure. Reload the page.")
		return nil, nil, nil, "", TraceTaskState{}, false
	}
	dec := h.readDecision(work, item)
	if dec != nil && dec.JobID != j.ID {
		dec = nil
	}
	return work, cancel, p, item, h.state(j, dec, true), true
}

func (h *traceTaskHandler) writeDecision(work context.Context, item string, d TraceTaskDecision) error {
	data, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	_, err = h.env.Services.Projects.Write(work, projectfs.AreaUploads, decisionRel(item), data, projectfs.WriteOpts{Actor: d.By, Note: "trace task " + d.Decision})
	return err
}

func (h *traceTaskHandler) approve(ctx *fasthttp.RequestCtx) {
	mode := h.env.Mode
	work, cancel, p, item, st, ok := h.decide(ctx)
	if !ok {
		return
	}
	defer cancel()
	if !st.CanApprove {
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "Approve is not offered: "+st.Blocked)
		return
	}
	store := h.env.Services.Projects
	rel := path.Join(st.Result.Task.Path, "task.yaml")
	raw, _, err := store.Read(work, projectfs.AreaBenchmarks, rel)
	if err != nil {
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "The draft's task.yaml is gone: "+err.Error())
		return
	}
	out, changed, err := scaffold.SetInstructionReviewed(raw)
	if err != nil {
		lib.SendError(ctx, mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	by := createdBy(p)
	if p != nil && p.Email != "" {
		by = p.Email
	}
	if changed {
		if _, err := store.Write(work, projectfs.AreaBenchmarks, rel, out, projectfs.WriteOpts{Actor: by, Note: "instruction approved by " + by}); err != nil {
			lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "Could not record the approval: "+err.Error())
			return
		}
	}
	// Rerun the guard that depends on the approval: read the file back.
	guards := []TraceTaskCheck{}
	for _, g := range st.Result.Admission.Guards {
		if g.Name != traceTaskGuardReviewed {
			guards = append(guards, g)
		}
	}
	guards = append(guards, reviewedGuard(work, store, rel))
	ready := true
	for _, g := range guards {
		ready = ready && g.Got == "pass"
	}
	d := TraceTaskDecision{JobID: st.JobID, Decision: "approved", By: by, At: h.now().UTC(), Task: st.Result.Task.Path}
	if err := h.writeDecision(work, item, d); err != nil {
		lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "Could not record the approval: "+err.Error())
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusOK, TraceTaskApproved{Decision: d, Guards: guards, Ready: ready})
}

// reviewedGuard checks provenance.instruction_reviewed in the stored file.
func reviewedGuard(work context.Context, store projectfs.Store, rel string) TraceTaskCheck {
	g := TraceTaskCheck{Name: traceTaskGuardReviewed, Want: "pass", Got: "fail"}
	raw, _, err := store.Read(work, projectfs.AreaBenchmarks, rel)
	if err != nil {
		g.Detail = err.Error()
		return g
	}
	var t schemas.Task
	if err := yaml.Unmarshal(raw, &t); err != nil {
		g.Detail = err.Error()
		return g
	}
	if t.Provenance != nil && t.Provenance.InstructionReviewed != nil && *t.Provenance.InstructionReviewed {
		g.Got = "pass"
	}
	return g
}

func (h *traceTaskHandler) reject(ctx *fasthttp.RequestCtx) {
	mode := h.env.Mode
	work, cancel, p, item, st, ok := h.decide(ctx)
	if !ok {
		return
	}
	defer cancel()
	if st.Decision != nil {
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "A person already decided on this draft.")
		return
	}
	by := createdBy(p)
	if p != nil && p.Email != "" {
		by = p.Email
	}
	d := TraceTaskDecision{JobID: st.JobID, Decision: "rejected", By: by, At: h.now().UTC()}
	if st.Result != nil && st.Result.Task != nil {
		d.Task = st.Result.Task.Path
		store := h.env.Services.Projects
		for _, f := range st.Result.Files {
			rel := path.Join(st.Result.Task.Path, path.Clean("/" + f.Path)[1:])
			if err := store.Delete(work, projectfs.AreaBenchmarks, rel, projectfs.WriteOpts{Actor: by, Note: "trace task draft rejected"}); err != nil && !errors.Is(err, projectfs.ErrNotFound) {
				lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "Could not discard the draft: "+err.Error())
				return
			}
		}
	}
	if err := h.writeDecision(work, item, d); err != nil {
		lib.SendError(ctx, mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "Could not record the rejection: "+err.Error())
		return
	}
	lib.SendJSON(ctx, mode, fasthttp.StatusOK, d)
}
