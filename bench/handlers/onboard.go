package handlers

// Add benchmark (lane Z20, audit/INFRA_I3 items 1-3): one front door for any
// input. Detect and inspect write nothing; a draft is staged under drafts/
// (outside benchmarks/, so evaluations never see it); readiness and publish
// are the onboard.ready and onboard.publish jobs.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/benchcatalog"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/onboard"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/valyala/fasthttp"
)

// Job kinds of the Add benchmark flow (registered in cmd/bifrost-bench).
const (
	OnboardReadyKind   = "onboard.ready"
	OnboardPublishKind = "onboard.publish"
)

// OnboardInputRequest is the body of POST /onboard/detect and /onboard/inspect.
type OnboardInputRequest struct {
	Input  onboard.Input `json:"input"`
	Source string        `json:"source,omitempty"` // inspect with this source (a detect match); empty: the best match
}

// OnboardDetectResult answers POST /onboard/detect.
type OnboardDetectResult struct {
	Matches []onboard.Match `json:"matches"`
	Name    string          `json:"name,omitempty"`
	Size    int64           `json:"size,omitempty"`
	Next    string          `json:"next"`
}

// OnboardDraftRequest is the body of POST /onboard/drafts.
type OnboardDraftRequest struct {
	Input onboard.Input `json:"input"`
	Plan  onboard.Plan  `json:"plan"`
}

// OnboardDraftList answers GET /onboard/drafts.
type OnboardDraftList struct {
	Drafts []onboard.SetSummary `json:"drafts"`
}

// OnboardUploadResult answers POST /onboard/upload.
type OnboardUploadResult struct {
	Upload string `json:"upload"` // pass as input.upload
	Size   int    `json:"size"`
	Zip    bool   `json:"zip"`
}

// OnboardReadyRequest is the optional body of POST /onboard/drafts/{set}/ready.
type OnboardReadyRequest struct {
	Task string `json:"task,omitempty"` // one task; empty: every task of the draft
	// JudgeConsent agrees to the judge calls of a judge-graded draft's readiness checks.
	JudgeConsent bool `json:"judge_consent,omitempty"`
}

// OnboardJobResult answers the ready and publish routes.
type OnboardJobResult struct {
	Set string    `json:"set"`
	Job *jobs.Job `json:"job"`
}

// OnboardReadyParams are the params of the onboard.ready job.
type OnboardReadyParams struct {
	Set          string `json:"set"`
	Task         string `json:"task,omitempty"`
	JudgeConsent bool   `json:"judge_consent,omitempty"`
}

// OnboardPublishParams are the params of the onboard.publish job.
type OnboardPublishParams struct {
	Set            string   `json:"set"`
	Tasks          []string `json:"tasks,omitempty"`
	IncludeUnready bool     `json:"include_unready,omitempty"`
}

type onboardHandler struct{ env OrgEnv }

// onboardDraftTimeout bounds one draft (a Hugging Face paging or an import).
const onboardDraftTimeout = 4 * time.Minute

func init() {
	RegisterOrgArea("onboard", func(env OrgEnv, r *router.Router) {
		h := &onboardHandler{env: env}
		r.POST(APIPrefix+"/onboard/upload", env.U(h.upload))
		r.POST(APIPrefix+"/onboard/detect", env.W(h.detect))
		r.POST(APIPrefix+"/onboard/inspect", env.W(h.inspect))
		r.GET(APIPrefix+"/onboard/drafts", env.R(h.list))
		r.POST(APIPrefix+"/onboard/drafts", env.W(h.draft))
		r.GET(APIPrefix+"/onboard/drafts/{set}", env.R(h.get))
		r.DELETE(APIPrefix+"/onboard/drafts/{set}", env.W(h.remove))
		r.PATCH(APIPrefix+"/onboard/drafts/{set}/tasks/{task}", env.W(h.editTask))
		r.POST(APIPrefix+"/onboard/drafts/{set}/ready", env.W(h.ready))
		r.POST(APIPrefix+"/onboard/drafts/{set}/publish", env.W(h.publish))
	})
}

func (h *onboardHandler) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

// caller is the org and principal of a request; local mode is the local org.
func (h *onboardHandler) caller(ctx *fasthttp.RequestCtx, write bool) (string, *auth.Principal, bool) {
	p, ok := auth.Lookup(ctx)
	if !ok {
		org := h.env.Services.OrgID
		if org == "" {
			org = auth.LocalOrgID
		}
		return org, auth.Local(), true
	}
	if !p.IsAuthenticated() || p.OrgID == "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in to add benchmarks")
		return "", nil, false
	}
	if write && !p.Can(auth.RoleMember) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "adding a benchmark needs the member role; ask an org admin")
		return "", nil, false
	}
	return p.OrgID, p, true
}

// onboardEnv is the pipeline's view of the org.
func (h *onboardHandler) onboardEnv(org string, p *auth.Principal) (*onboard.Env, bool) {
	if h.env.Cfg == nil || h.env.Cfg.Root == "" {
		return nil, false
	}
	e := &onboard.Env{Root: h.env.Cfg.Root, BenchmarksDir: h.env.Cfg.Path(h.env.Cfg.BenchmarksRoot), Hosted: h.env.Services.Hosted,
		Actor: createdBy(p), Store: h.env.Services.Projects}
	if keys := h.env.Services.Keys; keys != nil {
		e.Token = func(ctx context.Context, id string) (string, error) {
			if _, err := keys.Get(ctx, org, id); err != nil {
				return "", fmt.Errorf("connection %s: no such saved secret in this organization", id)
			}
			return keys.Reveal(ctx, org, id)
		}
	}
	return e, true
}

func (h *onboardHandler) setup(ctx *fasthttp.RequestCtx, write bool) (string, *auth.Principal, *onboard.Env, bool) {
	org, p, ok := h.caller(ctx, write)
	if !ok {
		return "", nil, nil, false
	}
	e, ok := h.onboardEnv(org, p)
	if !ok {
		sendUnavailable(ctx, h.env.Mode, "project folder")
		return "", nil, nil, false
	}
	return org, p, e, true
}

// sendOnboardError maps pipeline errors to statuses.
func (h *onboardHandler) sendOnboardError(ctx *fasthttp.RequestCtx, err error) {
	mode := h.env.Mode
	switch {
	case errors.Is(err, onboard.ErrSetNotFound):
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "draft not found; it may have been deleted")
	case errors.Is(err, onboard.ErrReadOnly):
		lib.SendError(ctx, mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, err.Error())
	case errors.Is(err, onboard.ErrNoDraft):
		lib.SendError(ctx, mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
	case errors.Is(err, onboard.ErrNothingToPublish):
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
	case errors.Is(err, benchcatalog.ErrFetchRefused):
		h.bad(ctx, err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		lib.SendError(ctx, mode, fasthttp.StatusGatewayTimeout, lib.ErrTypeUnavailable, "this took too long; try a smaller limit")
	default:
		lib.SendError(ctx, mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
	}
}

// upload handles POST /onboard/upload: the multipart "file" part (or the raw
// body with ?filename=) is stored under uploads/onboard/<stamp>/; a zip is
// extracted there and the folder is the upload.
func (h *onboardHandler) upload(ctx *fasthttp.RequestCtx) {
	_, p, e, ok := h.setup(ctx, true)
	if !ok {
		return
	}
	store := h.env.Services.Projects
	if store == nil {
		sendUnavailable(ctx, h.env.Mode, "project files")
		return
	}
	name, data, err := datasetBody(ctx)
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
	if len(data) > lib.MaxUploadBytes {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge,
			fmt.Sprintf("the file is larger than %d MiB: upload a smaller split, or paste a link", lib.MaxUploadBytes>>20))
		return
	}
	name = benchcatalog.SafeFileName(name)
	dir := path.Join("onboard", time.Now().UTC().Format("20060102-150405.000000000"))
	if err := os.MkdirAll(filepath.Join(e.Root, string(projectfs.AreaUploads)), 0o755); err != nil {
		h.bad(ctx, err.Error())
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	opts := projectfs.WriteOpts{Actor: createdBy(p), Note: "Add benchmark upload " + name}
	out := OnboardUploadResult{Size: len(data), Zip: benchcatalog.IsZip(name, data)}
	if out.Zip {
		if _, err := store.ExtractZip(work, projectfs.AreaUploads, dir, bytes.NewReader(data), int64(len(data)), opts); err != nil {
			h.bad(ctx, "unpack the zip: "+err.Error())
			return
		}
		out.Upload = dir
	} else {
		rel := path.Join(dir, name)
		if _, err := store.Write(work, projectfs.AreaUploads, rel, data, opts); err != nil {
			h.bad(ctx, "save the file: "+err.Error())
			return
		}
		out.Upload = rel
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, &out)
}

func (h *onboardHandler) detect(ctx *fasthttp.RequestCtx) {
	_, _, e, ok := h.setup(ctx, true)
	if !ok {
		return
	}
	var req OnboardInputRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"input":{"url"|"upload"|"data","name"?,"connection"?,"hint"?}}: `+err.Error())
		return
	}
	work, cancel := workContext(ctx, time.Minute)
	defer cancel()
	ms, peek, err := onboard.Detect(work, e, req.Input)
	if err != nil {
		h.sendOnboardError(ctx, err)
		return
	}
	out := OnboardDetectResult{Matches: ms, Name: peek.Name, Size: peek.Size}
	if out.Matches == nil {
		out.Matches = []onboard.Match{}
	}
	switch {
	case len(ms) == 0 && peek.FetchErr != "":
		out.Next = "Could not read the link: " + peek.FetchErr
	case len(ms) == 0:
		out.Next = "Nothing here is a benchmark Heimdall can read: drop a CSV or JSONL file, a zip of a benchmark folder, or paste a Hugging Face, GitHub or Harbor link."
	case ms[0].CanDraft:
		out.Next = "Inspect it to see what is inside."
	default:
		out.Next = ms[0].Note
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, &out)
}

func (h *onboardHandler) inspect(ctx *fasthttp.RequestCtx) {
	_, _, e, ok := h.setup(ctx, true)
	if !ok {
		return
	}
	var req OnboardInputRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"input":{...},"source"?}: `+err.Error())
		return
	}
	work, cancel := workContext(ctx, 2*time.Minute)
	defer cancel()
	inv, err := onboard.Inspect(work, e, req.Input, req.Source)
	if err != nil {
		h.sendOnboardError(ctx, err)
		return
	}
	if inv.Suggest.Suite != "" && isReservedSuiteID(h.env.Services.Suites, inv.Suggest.Suite) {
		inv.Suggest.Suite += "-own"
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, inv)
}

func (h *onboardHandler) list(ctx *fasthttp.RequestCtx) {
	_, _, e, ok := h.setup(ctx, false)
	if !ok {
		return
	}
	sets, err := onboard.ListSets(e.Root)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "list drafts: "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, &OnboardDraftList{Drafts: sets})
}

func (h *onboardHandler) draft(ctx *fasthttp.RequestCtx) {
	_, _, e, ok := h.setup(ctx, true)
	if !ok {
		return
	}
	var req OnboardDraftRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"input":{...},"plan":{"suite",...}}: `+err.Error())
		return
	}
	if isReservedSuiteID(h.env.Services.Suites, strings.TrimSpace(req.Plan.Suite)) {
		sendReservedSuiteID(ctx, h.env.Mode, strings.TrimSpace(req.Plan.Suite))
		return
	}
	work, cancel := workContext(ctx, onboardDraftTimeout)
	defer cancel()
	set, err := onboard.Draft(work, e, req.Input, req.Plan)
	if err != nil {
		h.sendOnboardError(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, set)
}

// loadSet reads {set}, answering on failure.
func (h *onboardHandler) loadSet(ctx *fasthttp.RequestCtx, e *onboard.Env) (*onboard.DraftSet, bool) {
	id := pathParam(ctx, "set")
	set, err := onboard.LoadSet(e.Root, id)
	if err != nil {
		h.sendOnboardError(ctx, err)
		return nil, false
	}
	return set, true
}

func (h *onboardHandler) get(ctx *fasthttp.RequestCtx) {
	org, _, e, ok := h.setup(ctx, false)
	if !ok {
		return
	}
	set, ok := h.loadSet(ctx, e)
	if !ok {
		return
	}
	h.syncJob(ctx, org, e.Root, set)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, set)
}

// syncJob marks a readiness run failed when its job ended without writing
// its result (a crash, a cancel): the set never shows "running" for ever.
func (h *onboardHandler) syncJob(ctx *fasthttp.RequestCtx, org, root string, set *onboard.DraftSet) {
	r := set.Readiness
	if r == nil || r.JobID == "" || (r.Status != onboard.StatusQueued && r.Status != onboard.StatusRunning) || h.env.Services.Jobs == nil {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := h.env.Services.Jobs.Get(work, org, r.JobID)
	if err != nil || j == nil {
		return
	}
	if j.Status == jobs.StatusRunning && r.Status == onboard.StatusQueued {
		r.Status = onboard.StatusRunning
	}
	if !j.Status.Terminal() {
		return
	}
	unlock := set.Lock()
	defer unlock()
	fresh, err := onboard.LoadSet(root, set.ID)
	if err != nil || fresh.Readiness == nil || fresh.Readiness.JobID != r.JobID ||
		(fresh.Readiness.Status != onboard.StatusQueued && fresh.Readiness.Status != onboard.StatusRunning) {
		if fresh != nil {
			*set = *fresh
		}
		return
	}
	fresh.Readiness.Status = onboard.StatusFailed
	fresh.Readiness.Error = j.Error
	if fresh.Readiness.Error == "" {
		fresh.Readiness.Error = "the readiness job ended " + string(j.Status) + " before it recorded results"
	}
	if fresh.Save() == nil {
		*set = *fresh
	}
}

func (h *onboardHandler) remove(ctx *fasthttp.RequestCtx) {
	_, _, e, ok := h.setup(ctx, true)
	if !ok {
		return
	}
	if err := onboard.DeleteSet(e.Root, pathParam(ctx, "set")); err != nil {
		h.sendOnboardError(ctx, err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h *onboardHandler) editTask(ctx *fasthttp.RequestCtx) {
	_, _, e, ok := h.setup(ctx, true)
	if !ok {
		return
	}
	set, ok := h.loadSet(ctx, e)
	if !ok {
		return
	}
	var req onboard.TaskEdit
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"review"?:"accepted"|"rejected"|"pending","held_out"?:bool,"drop"?:bool}: `+err.Error())
		return
	}
	unlock := set.Lock()
	defer unlock()
	if r := set.Readiness; r != nil && (r.Status == onboard.StatusQueued || r.Status == onboard.StatusRunning) && (req.Drop || req.HeldOut != nil) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "readiness is running on this draft; change its tasks when it ends")
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	if err := set.EditTask(work, e, pathParam(ctx, "task"), req); err != nil {
		h.sendOnboardError(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, set)
}

// startJob queues kind with params for set.
func (h *onboardHandler) startJob(ctx *fasthttp.RequestCtx, org string, p *auth.Principal, kind string, params any) (*jobs.Job, bool) {
	if h.env.Services.Jobs == nil {
		sendUnavailable(ctx, h.env.Mode, "job runner")
		return nil, false
	}
	raw, _ := json.Marshal(params)
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := h.env.Services.Jobs.Create(work, jobs.NewJob{OrgID: org, Kind: kind, Params: raw, CreatedBy: createdBy(p)})
	if err != nil {
		status, typ := fasthttp.StatusInternalServerError, lib.ErrTypeInternal
		if errors.Is(err, jobs.ErrInvalidParams) {
			status, typ = fasthttp.StatusBadRequest, lib.ErrTypeBadRequest
		} else if strings.Contains(err.Error(), "unknown kind") || strings.Contains(err.Error(), "not registered") {
			status, typ = fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable
		}
		lib.SendError(ctx, h.env.Mode, status, typ, "start "+kind+": "+err.Error())
		return nil, false
	}
	return j, true
}

func (h *onboardHandler) ready(ctx *fasthttp.RequestCtx) {
	org, p, e, ok := h.setup(ctx, true)
	if !ok {
		return
	}
	set, ok := h.loadSet(ctx, e)
	if !ok {
		return
	}
	var req OnboardReadyRequest
	if len(strings.TrimSpace(string(ctx.PostBody()))) > 0 {
		if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
			h.bad(ctx, `body must be {"task"?,"judge_consent"?}: `+err.Error())
			return
		}
	}
	if req.Task != "" && set.Task(req.Task) == nil {
		sendNotFound(ctx, h.env.Mode, "draft task", req.Task)
		return
	}
	if len(set.Tasks) == 0 {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "this draft has no task to check")
		return
	}
	if set.Published != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "this draft was published; run readiness on the benchmark's page")
		return
	}
	if r := set.Readiness; r != nil && (r.Status == onboard.StatusQueued || r.Status == onboard.StatusRunning) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "readiness is already running on this draft")
		return
	}
	j, ok := h.startJob(ctx, org, p, OnboardReadyKind, OnboardReadyParams{Set: set.ID, Task: req.Task, JudgeConsent: req.JudgeConsent})
	if !ok {
		return
	}
	unlock := set.Lock()
	if fresh, err := onboard.LoadSet(e.Root, set.ID); err == nil {
		total := len(fresh.Tasks)
		if req.Task != "" {
			total = 1
		}
		if fresh.Readiness == nil || fresh.Readiness.JobID != j.ID {
			fresh.Readiness = &onboard.Readiness{JobID: j.ID, Status: onboard.StatusQueued, Total: total, At: time.Now().UTC()}
			_ = fresh.Save()
		}
	}
	unlock()
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, &OnboardJobResult{Set: set.ID, Job: j})
}

func (h *onboardHandler) publish(ctx *fasthttp.RequestCtx) {
	org, p, e, ok := h.setup(ctx, true)
	if !ok {
		return
	}
	set, ok := h.loadSet(ctx, e)
	if !ok {
		return
	}
	var req onboard.PublishOptions
	if len(strings.TrimSpace(string(ctx.PostBody()))) > 0 {
		if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
			h.bad(ctx, `body must be {"tasks"?:[...],"include_unready"?:bool}: `+err.Error())
			return
		}
	}
	if set.Published != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "this draft was published already")
		return
	}
	if isReservedSuiteID(h.env.Services.Suites, set.Suite) {
		sendReservedSuiteID(ctx, h.env.Mode, set.Suite)
		return
	}
	if tasks, _ := set.Publishable(req.Tasks, req.IncludeUnready); len(tasks) == 0 {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
			"no task is accepted and ready: run the readiness checks first, or publish not-ready tasks on purpose (include_unready)")
		return
	}
	j, ok := h.startJob(ctx, org, p, OnboardPublishKind, OnboardPublishParams{Set: set.ID, Tasks: req.Tasks, IncludeUnready: req.IncludeUnready})
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusAccepted, &OnboardJobResult{Set: set.ID, Job: j})
}
