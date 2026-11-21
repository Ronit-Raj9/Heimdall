package handlers

// Releases from evaluations (lane X12): one release covers a whole
// evaluation and names the agent version it puts live and the version it
// replaces ("codex v4 replaces v3"), with before and after numbers.
//
//	GET  /evaluations/{id}/release-preview  what a release would record, and what blocks it
//	POST /evaluations/{id}/release          record it (approver, note, override)
//	POST /evaluations/{id}/schedule         repeat the evaluation nightly or weekly
//	GET  /agents/{id}/live-version          the version the newest release put live

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tenancy"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// EvaluationReleaseChild is one benchmark of a release preview.
type EvaluationReleaseChild struct {
	Benchmark    string `json:"benchmark"`
	ExperimentID string `json:"experiment_id"`
	Status       string `json:"status"`
	Included     bool   `json:"included"` // finished, so its runs are evidence of the release
}

// EvaluationReleasePreview is GET /evaluations/{id}/release-preview.
type EvaluationReleasePreview struct {
	Evaluation      string                        `json:"evaluation"`
	Status          string                        `json:"status"`
	Mode            string                        `json:"mode"`
	Agent           string                        `json:"agent"`
	Version         int                           `json:"version"`
	ReplacesVersion int                           `json:"replaces_version"`
	Live            LiveVersion                   `json:"live"`
	Candidate       string                        `json:"candidate"`
	Baseline        *schemas.GateBaseline         `json:"baseline,omitempty"`
	Summary         *schemas.ReleaseSummary       `json:"summary,omitempty"`
	Gate            *schemas.EvaluationGateResult `json:"gate,omitempty"`
	Decision        schemas.Decision              `json:"decision"`
	Children        []EvaluationReleaseChild      `json:"children"`
	Blocked         []string                      `json:"blocked"`
	Ready           bool                          `json:"ready"`             // can be released without an override
	Problem         string                        `json:"problem,omitempty"` // why it cannot be released at all
}

// EvaluationReleaseRequest is POST /evaluations/{id}/release.
type EvaluationReleaseRequest struct {
	Approver string `json:"approver,omitempty"` // default: the signed-in user
	Note     string `json:"note,omitempty"`
	Override string `json:"override,omitempty"` // required to release against a block
}

// EvaluationScheduleRequest is POST /evaluations/{id}/schedule.
type EvaluationScheduleRequest struct {
	Cadence      string  `json:"cadence"`                   // nightly | weekly
	MaxUSDPerDay float64 `json:"max_usd_per_day,omitempty"` // default: the evaluation's budget, else the default budget per benchmark
	Timezone     string  `json:"timezone,omitempty"`
}

// EvaluationScheduleResponse is the schedule written.
type EvaluationScheduleResponse struct {
	Schedule schedule.Schedule `json:"schedule"`
	Next     string            `json:"next,omitempty"`
}

// Schedule cadences and their cron (02:00 every night, 02:00 on Mondays).
var evaluationCadences = map[string]string{"nightly": "0 2 * * *", "weekly": "0 2 * * 1"}

func init() {
	RegisterOrgArea("evalrelease", func(env OrgEnv, r *router.Router) {
		h := &evalReleaseHandler{evaluationsHandler{env: env, now: time.Now}}
		p := APIPrefix + "/evaluations/{id}"
		r.GET(p+"/release-preview", env.R(h.preview))
		r.POST(p+"/release", env.W(h.release))
		r.POST(p+"/schedule", env.W(h.schedule))
		r.GET(APIPrefix+"/agents/{id}/live-version", env.R(h.live))
	})
}

type evalReleaseHandler struct{ evaluationsHandler }

func (h *evalReleaseHandler) gates() gateEnv {
	return gateEnv{env: h.env, root: h.root(), now: h.now}
}

// buildPreview resolves what a release of ev would record.
func (h *evalReleaseHandler) buildPreview(ctx context.Context, ev *schemas.Evaluation) (*EvaluationReleasePreview, *evalResolution) {
	p := &EvaluationReleasePreview{Evaluation: ev.ID, Status: ev.Status, Mode: ev.Spec.Mode, Children: []EvaluationReleaseChild{}, Blocked: []string{}}
	for _, c := range ev.Children {
		if c.ExperimentID == "" {
			continue
		}
		p.Children = append(p.Children, EvaluationReleaseChild{Benchmark: c.Benchmark, ExperimentID: c.ExperimentID, Status: c.Status,
			Included: c.Status == string(schemas.ExperimentDone)})
	}
	if ev.Status != schemas.EvaluationDone {
		p.Problem = "the evaluation has not finished; a release needs its results"
		return p, nil
	}
	if !slices.ContainsFunc(p.Children, func(c EvaluationReleaseChild) bool { return c.Included }) {
		p.Problem = "no benchmark of this evaluation finished"
		return p, nil
	}
	evals, _ := h.store().List()
	r, err := h.gates().resolve(ctx, ev, evals)
	if err != nil {
		p.Problem = "read the evaluation's results: " + err.Error()
		return p, nil
	}
	p.Agent, p.Version, p.Candidate, p.Live = r.Agent, r.CandidateVersion, r.Candidate, r.Live
	p.ReplacesVersion = r.Live.Version
	p.Baseline, p.Summary, p.Decision = r.Baseline, r.summary(), r.decision()
	p.Gate = ev.GateResult
	if p.Gate == nil {
		p.Gate = h.gates().judge(ev, r)
	}
	if p.Version > 0 && p.Version == r.Live.Version {
		p.Problem = fmt.Sprintf("%s is already the live version", versionLabel(p.Agent, p.Version))
		return p, r
	}
	p.Blocked = append(p.Blocked, r.blocked(p.Gate)...)
	p.Ready = len(p.Blocked) == 0
	return p, r
}

func (h *evalReleaseHandler) preview(ctx *fasthttp.RequestCtx) {
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	h.refresh(ev)
	work, cancel := workContext(ctx, analyzeTimeout)
	defer cancel()
	p, _ := h.buildPreview(work, ev)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, p)
}

// release records a release of the evaluation: every finished child is
// analyzed (an identical saved analysis is reused), then the release log
// checks each child's seals and verdict and records one release for all.
func (h *evalReleaseHandler) release(ctx *fasthttp.RequestCtx) {
	s := h.env.Services
	if s.Releases == nil {
		sendUnavailable(ctx, h.env.Mode, "release log")
		return
	}
	var req EvaluationReleaseRequest
	dec := json.NewDecoder(strings.NewReader(string(ctx.PostBody())))
	dec.DisallowUnknownFields()
	if len(ctx.PostBody()) > 0 {
		if err := dec.Decode(&req); err != nil {
			h.fail(ctx, fasthttp.StatusBadRequest, "parse release request: "+err.Error())
			return
		}
	}
	approver := strings.TrimSpace(req.Approver)
	if p := auth.PrincipalFrom(ctx); p != nil && approver == "" {
		approver = p.Email
		if approver == "" {
			approver = p.UserID
		}
	}
	if approver == "" {
		h.fail(ctx, fasthttp.StatusBadRequest, "approver is required")
		return
	}
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	h.refresh(ev)
	work, cancel := workContext(ctx, evaluationLaunchTimeout)
	defer cancel()
	p, r := h.buildPreview(work, ev)
	if p.Problem != "" || r == nil {
		h.fail(ctx, fasthttp.StatusConflict, "release refused: "+p.Problem)
		return
	}
	var refs []schemas.ReleaseChildRef
	for _, c := range p.Children {
		if !c.Included {
			continue
		}
		if s.Analyzer != nil {
			if _, err := s.Analyzer.Analyze(work, c.ExperimentID, ""); err != nil {
				sendDepError(ctx, h.env.Mode, "analyze "+c.Benchmark, err)
				return
			}
		}
		refs = append(refs, schemas.ReleaseChildRef{Benchmark: c.Benchmark, ExpID: c.ExperimentID})
	}
	gate := ""
	if p.Gate != nil {
		gate = p.Gate.State
	}
	rr := &schemas.ReleaseRequest{
		EvaluationID: ev.ID, Approver: approver, Note: req.Note, Override: req.Override,
		Evaluation: &schemas.ReleaseEvaluation{
			EvaluationID: ev.ID, AgentID: p.Agent, Version: p.Version, ReplacesVersion: p.ReplacesVersion,
			CandidateArm: p.Candidate, Children: refs, Decision: p.Decision, Blocked: p.Blocked, Gate: gate, Summary: p.Summary,
		},
	}
	rec, err := s.Releases.Create(work, rr)
	if err != nil {
		var refused releaseRefusal
		if errors.As(err, &refused) && refused.ReleaseRefused() {
			h.fail(ctx, fasthttp.StatusConflict, err.Error())
			return
		}
		sendDepError(ctx, h.env.Mode, "release "+ev.ID, err)
		return
	}
	tenancy.SetCreated(ctx, rec.ReleaseID)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, rec)
}

func (h *evalReleaseHandler) live(ctx *fasthttp.RequestCtx) {
	agent, ok := idParam(ctx, h.env.Mode, "id", "agent")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, liveVersion(work, h.env.Services.Releases, agent))
}

// scheduleName is the schedule name of an evaluation's repeat.
func scheduleName(ev *schemas.Evaluation, cadence string) string {
	id := strings.ReplaceAll(strings.TrimPrefix(ev.ID, "ev_"), "_", "-")
	n := "eval-" + id + "-" + cadence
	if len(n) > 64 {
		n = n[:64]
	}
	return n
}

// schedule adds a schedule that repeats the evaluation's spec nightly or
// weekly. Each run is a new evaluation; its gate is judged when it ends.
func (h *evalReleaseHandler) schedule(ctx *fasthttp.RequestCtx) {
	var req EvaluationScheduleRequest
	dec := json.NewDecoder(strings.NewReader(string(ctx.PostBody())))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		h.fail(ctx, fasthttp.StatusBadRequest, "parse schedule request: "+err.Error())
		return
	}
	cron, ok := evaluationCadences[req.Cadence]
	if !ok {
		h.fail(ctx, fasthttp.StatusBadRequest, "cadence must be nightly or weekly")
		return
	}
	ev, ok := h.load(ctx)
	if !ok {
		return
	}
	capUSD := req.MaxUSDPerDay
	if capUSD == 0 {
		capUSD = ev.Spec.BudgetUSD
	}
	if capUSD == 0 {
		capUSD = QuickRunDefaultBudget * float64(max(1, len(ev.Spec.Benchmarks)))
	}
	sch := schedule.Schedule{Name: scheduleName(ev, req.Cadence), Evaluation: ev.ID, Cron: cron, Timezone: strings.TrimSpace(req.Timezone), MaxUSDPerDay: capUSD}
	root := h.root()
	f, err := LoadWebSchedules(root)
	if err != nil {
		h.fail(ctx, fasthttp.StatusUnprocessableEntity, err.Error())
		return
	}
	if _, dup := f.Find(sch.Name); dup {
		h.fail(ctx, fasthttp.StatusConflict, "this evaluation already repeats "+req.Cadence+"; change or remove it on the Schedules page")
		return
	}
	if err := f.Add(sch); err != nil {
		h.fail(ctx, fasthttp.StatusConflict, err.Error())
		return
	}
	if err := checkScheduleShape(f); err != nil {
		h.fail(ctx, fasthttp.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := sch.Validate(root, ScheduleExperimentLoader); err != nil {
		h.fail(ctx, fasthttp.StatusUnprocessableEntity, err.Error())
		return
	}
	if h.env.Services.Hosted {
		full := &schedule.File{Alerts: f.Alerts, Schedules: slices.Clone(f.Schedules)}
		if ScheduleExtras != nil {
			ScheduleExtras(root, full)
		}
		if err := CheckHostedSchedules(full, time.Now()); err != nil {
			h.fail(ctx, fasthttp.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	data, err := yaml.Marshal(f)
	if err != nil {
		h.fail(ctx, fasthttp.StatusInternalServerError, err.Error())
		return
	}
	data = append([]byte("# Web schedules (Schedules page). Alerts name org secrets or environment\n# variables only; never put a webhook URL or a secret in this file.\n"), data...)
	if err := (&schedulesHandler{env: h.env}).write(ctx, data); err != nil {
		sendProjectErr(ctx, h.env.Mode, err)
		return
	}
	out := EvaluationScheduleResponse{Schedule: sch}
	if c, err := schedule.ParseCron(sch.Cron); err == nil {
		if loc, err := sch.Location(); err == nil {
			if n := c.Next(time.Now().In(loc)); !n.IsZero() {
				out.Next = n.Format(time.RFC3339)
			}
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, out)
}
