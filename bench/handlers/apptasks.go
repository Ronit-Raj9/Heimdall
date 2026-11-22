package handlers

// Tasks from your API (lane Z18): upload an OpenAPI file and a policy
// document; Heimdall builds a copy of the app (a stateful mock), a pinned
// model drafts the policy rules (a person approves them once), the seed
// data and the scenarios (the apptasks.generate job), each scenario is
// proved against the app copy's rules, and the survivors are written into
// a benchmark as pending review. The readiness checks are the quality
// filter. Drafts live in the project's uploads/app-tasks/ folder.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/apptasks"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/valyala/fasthttp"
)

// AppTasksCreateRequest is the body of POST /app-tasks.
type AppTasksCreateRequest struct {
	// Suite is the benchmark the tasks go into: a new one, or one this
	// generator made for the same app.
	Suite string `json:"suite"`
	// App is the app copy's service id (services/<app>/), also the tool
	// server's name the agent sees.
	App   string `json:"app"`
	Title string `json:"title,omitempty"`
	// Spec is the OpenAPI 3 file (YAML or JSON) as text.
	Spec     string `json:"spec"`
	SpecName string `json:"spec_name,omitempty"`
	// Policy is the policy document as text or Markdown.
	Policy     string `json:"policy"`
	PolicyName string `json:"policy_name,omitempty"`
}

// AppTasksDraftView is a draft as the API shows it.
type AppTasksDraftView struct {
	*apptasks.Draft
	Operations  []scaffold.MockOperation `json:"operations"`
	Collections []string                 `json:"collections"`
	Next        string                   `json:"next"`
}

// AppTasksDraftSummary is one row of GET /app-tasks.
type AppTasksDraftSummary struct {
	ID        string  `json:"id"`
	Suite     string  `json:"suite"`
	App       string  `json:"app"`
	Title     string  `json:"title"`
	Status    string  `json:"status"`
	Rules     int     `json:"rules"`
	Tasks     int     `json:"tasks"`
	Written   int     `json:"written"`
	Pending   int     `json:"pending"`
	SpentUSD  float64 `json:"spent_usd"`
	CreatedAt string  `json:"created_at"`
}

// AppTasksList answers GET /app-tasks.
type AppTasksList struct {
	Drafts []AppTasksDraftSummary `json:"drafts"`
}

// AppTasksRulesRequest is the body of PUT /app-tasks/{id}/rules: the rule
// list a person checked, approved as a whole.
type AppTasksRulesRequest struct {
	Rules   []apptasks.Rule `json:"rules"`
	Approve bool            `json:"approve"`
}

// AppTasksWriteRequest is the body of POST /app-tasks/{id}/write.
type AppTasksWriteRequest struct {
	// Judge pins the reply judge of a new benchmark when the tasks were
	// drafted with reply rubrics (judged).
	Judge   *scaffold.JudgeConfig `json:"judge,omitempty"`
	HeldOut bool                  `json:"held_out,omitempty"`
}

// AppTasksWriteResult answers POST /app-tasks/{id}/write.
type AppTasksWriteResult struct {
	Suite   string             `json:"suite"`
	Created bool               `json:"created"`
	Tasks   []apptasks.Written `json:"tasks"`
	Next    string             `json:"next"`
}

// AppTasksReviewRequest is the body of POST /app-tasks/{id}/review.
type AppTasksReviewRequest struct {
	Task     string `json:"task"`
	Decision string `json:"decision"` // accepted | rejected
}

// AppTasksReviewResult answers POST /app-tasks/{id}/review.
type AppTasksReviewResult struct {
	Task     apptasks.Written `json:"task"`
	Pending  int              `json:"pending"`
	Accepted int              `json:"accepted"`
	Rejected int              `json:"rejected"`
}

type appTasksArea struct{ *suiteAuthoring }

func init() {
	RegisterOrgArea("apptasks", func(env OrgEnv, r *router.Router) {
		h := &appTasksArea{&suiteAuthoring{mode: env.Mode, projects: env.Services.Projects, suites: env.Services.Suites}}
		r.POST(APIPrefix+"/app-tasks", env.U(h.create))
		r.GET(APIPrefix+"/app-tasks", env.R(h.list))
		r.GET(APIPrefix+"/app-tasks/{id}", env.R(h.get))
		r.PUT(APIPrefix+"/app-tasks/{id}/rules", env.W(h.rules))
		r.POST(APIPrefix+"/app-tasks/{id}/write", env.W(h.write))
		r.POST(APIPrefix+"/app-tasks/{id}/review", env.W(h.review))
	})
}

func (h *appTasksArea) store() apptasks.Store {
	return apptasks.Store{Dir: filepath.Join(h.projects.Root(), filepath.FromSlash(apptasks.Folder))}
}

func (h *appTasksArea) sendErr(ctx *fasthttp.RequestCtx, err error) {
	switch {
	case errors.Is(err, apptasks.ErrNotFound):
		lib.SendError(ctx, h.mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
	case errors.Is(err, apptasks.ErrBusy):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
	default:
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
	}
}

func (h *appTasksArea) view(st apptasks.Store, d *apptasks.Draft) *AppTasksDraftView {
	v := &AppTasksDraftView{Draft: d, Operations: []scaffold.MockOperation{}, Collections: []string{}}
	if m, _, err := st.Mock(d); err == nil {
		v.Operations, v.Collections = m.Operations, m.Collections()
	}
	switch d.Status {
	case apptasks.StatusNew:
		v.Next = "Draft the policy rules (the apptasks.generate job, stage rules), with the model to pin."
	case apptasks.StatusRulesDrafted:
		v.Next = "Read the rules, fix any that are wrong, and approve the list."
	case apptasks.StatusRulesApproved:
		v.Next = "Draft the tasks (the apptasks.generate job, stage tasks)."
	case apptasks.StatusGenerated:
		v.Next = "Check the drafted tasks and why others were dropped, then write them into benchmark " + d.Suite + "."
	default:
		v.Next = "Run the readiness checks on " + d.Suite + ", then accept or reject each pending task."
	}
	return v
}

// create handles POST /app-tasks.
func (h *appTasksArea) create(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) || h.refuseViewer(ctx) {
		return
	}
	var req AppTasksCreateRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"suite","app","title"?,"spec","spec_name"?,"policy","policy_name"?}: `+err.Error())
		return
	}
	req.Suite, req.App = strings.TrimSpace(req.Suite), strings.TrimSpace(req.App)
	if isBuiltinSuite(h.suites, req.Suite) {
		sendBuiltinReadOnly(ctx, h.mode, req.Suite)
		return
	}
	if len(req.Spec) > scaffold.MaxMockSpecBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeBadRequest, "the OpenAPI file is over 4 MiB")
		return
	}
	if len(req.Policy) > apptasks.MaxPolicyBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeBadRequest, "the policy is over 256 KiB")
		return
	}
	if strings.TrimSpace(req.Spec) == "" {
		h.bad(ctx, "spec: paste or upload an OpenAPI 3 file")
		return
	}
	if dir, err := h.projects.Resolve(projectfs.AreaBenchmarks, req.Suite); err == nil && req.Suite != "" {
		if _, err := os.Stat(filepath.Join(dir, "suite.yaml")); err == nil {
			if _, err := scaffold.ReadMock(filepath.Join(dir, "services", req.App)); err != nil {
				lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
					"benchmark "+req.Suite+" exists and has no app copy services/"+req.App+" from this generator; pick a new benchmark id")
				return
			}
		}
	}
	st := h.store()
	d, _, err := st.Create(apptasks.Upload{Suite: req.Suite, App: req.App, Title: req.Title, Spec: []byte(req.Spec), SpecName: req.SpecName,
		Policy: []byte(req.Policy), PolicyName: req.PolicyName, By: actorName(ctx)})
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, h.view(st, d))
}

// list handles GET /app-tasks.
func (h *appTasksArea) list(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	drafts, err := h.store().List()
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list drafts: "+err.Error())
		return
	}
	out := AppTasksList{Drafts: []AppTasksDraftSummary{}}
	for _, d := range drafts {
		s := AppTasksDraftSummary{ID: d.ID, Suite: d.Suite, App: d.App, Title: d.Title, Status: d.Status, Rules: len(d.Rules),
			Tasks: len(d.Plans), Written: len(d.Written), SpentUSD: d.SpentUSD, CreatedAt: d.CreatedAt.Format("2006-01-02T15:04:05Z07:00")}
		for _, w := range d.Written {
			if w.Review == apptasks.ReviewPending {
				s.Pending++
			}
		}
		out.Drafts = append(out.Drafts, s)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

func (h *appTasksArea) draftParam(ctx *fasthttp.RequestCtx) (string, bool) {
	id := pathParam(ctx, "id")
	if !apptasks.ValidDraftID(id) {
		sendNotFound(ctx, h.mode, "draft", id)
		return "", false
	}
	return id, true
}

// get handles GET /app-tasks/{id}. Drafts hold the tasks' hidden checks,
// so viewers are refused.
func (h *appTasksArea) get(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) || h.refuseViewer(ctx) {
		return
	}
	id, ok := h.draftParam(ctx)
	if !ok {
		return
	}
	st := h.store()
	d, err := st.Load(id)
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, h.view(st, d))
}

// edit loads a draft under its lock, runs fn and saves it.
func (h *appTasksArea) edit(ctx *fasthttp.RequestCtx, fn func(st apptasks.Store, d *apptasks.Draft) (any, int, error)) {
	if !h.needProjects(ctx) || h.refuseViewer(ctx) {
		return
	}
	id, ok := h.draftParam(ctx)
	if !ok {
		return
	}
	st := h.store()
	unlock, err := st.TryLock(id)
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	defer unlock()
	d, err := st.Load(id)
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	out, status, err := fn(st, d)
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	if err := st.Save(d); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "save the draft: "+err.Error())
		return
	}
	if out == nil {
		out = h.view(st, d)
	}
	lib.SendJSON(ctx, h.mode, status, out)
}

// rules handles PUT /app-tasks/{id}/rules.
func (h *appTasksArea) rules(ctx *fasthttp.RequestCtx) {
	var req AppTasksRulesRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"rules":[{"id","text","kind","operations"}],"approve":true}: `+err.Error())
		return
	}
	if !req.Approve {
		h.bad(ctx, "approve must be true: the list is approved as a whole, once")
		return
	}
	h.edit(ctx, func(st apptasks.Store, d *apptasks.Draft) (any, int, error) {
		m, _, err := st.Mock(d)
		if err != nil {
			return nil, 0, err
		}
		return nil, fasthttp.StatusOK, apptasks.ApproveRules(d, m, req.Rules, actorName(ctx))
	})
}

// write handles POST /app-tasks/{id}/write.
func (h *appTasksArea) write(ctx *fasthttp.RequestCtx) {
	var req AppTasksWriteRequest
	if len(strings.TrimSpace(string(ctx.PostBody()))) > 0 {
		if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
			h.bad(ctx, `body must be {"judge"?:{"provider","model"},"held_out"?}: `+err.Error())
			return
		}
	}
	h.edit(ctx, func(st apptasks.Store, d *apptasks.Draft) (any, int, error) {
		if isBuiltinSuite(h.suites, d.Suite) {
			return nil, 0, errors.New("benchmark " + d.Suite + " is a built-in benchmark and read-only")
		}
		if err := os.MkdirAll(filepath.Join(h.projects.Root(), string(projectfs.AreaBenchmarks)), 0o755); err != nil {
			return nil, 0, err
		}
		dir, err := h.projects.Resolve(projectfs.AreaBenchmarks, d.Suite)
		if err != nil {
			return nil, 0, err
		}
		_, statErr := os.Stat(filepath.Join(dir, "suite.yaml"))
		written, err := apptasks.WriteBenchmark(dir, st, d, req.Judge, req.HeldOut)
		if err != nil {
			return nil, 0, err
		}
		out := &AppTasksWriteResult{Suite: d.Suite, Created: statErr != nil, Tasks: written,
			Next: "Run the readiness checks on " + d.Suite + ": they build the app copy's image and prove each task's reference passes and its controls fail. Then accept or reject each pending task."}
		if !h.visible(dir) {
			out.Next += " Note: this server reads benchmarks from another folder, so the benchmark is not listed yet."
		}
		return out, fasthttp.StatusCreated, nil
	})
}

// review handles POST /app-tasks/{id}/review.
func (h *appTasksArea) review(ctx *fasthttp.RequestCtx) {
	var req AppTasksReviewRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"task","decision":"accepted"|"rejected"}: `+err.Error())
		return
	}
	if !validID(req.Task) {
		h.bad(ctx, "task: a task id")
		return
	}
	h.edit(ctx, func(st apptasks.Store, d *apptasks.Draft) (any, int, error) {
		dir, err := h.projects.Resolve(projectfs.AreaBenchmarks, d.Suite)
		if err != nil {
			return nil, 0, err
		}
		if err := apptasks.Review(dir, d, req.Task, req.Decision, actorName(ctx)); err != nil {
			return nil, 0, err
		}
		out := &AppTasksReviewResult{}
		for _, w := range d.Written {
			if w.ID == req.Task {
				out.Task = w
			}
			switch w.Review {
			case apptasks.ReviewPending:
				out.Pending++
			case apptasks.ReviewAccepted:
				out.Accepted++
			case apptasks.ReviewRejected:
				out.Rejected++
			}
		}
		return out, fasthttp.StatusOK, nil
	})
}
