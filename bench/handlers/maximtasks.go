package handlers

// Maxim production failures to regression tests (lane Z06, bench/tracebulk):
// an org connected to Maxim (handlers/maxim.go) pulls the failed, errored or
// low-scored traces of its log repository in bulk, sees them grouped into
// failure clusters, drafts one task per cluster into a benchmark, says what
// should have happened for each draft, and runs its readiness check.
//
//	GET  /integrations/maxim/batches                                   the org's imports, newest first
//	POST /integrations/maxim/batches                                   pull and cluster failures (member)
//	GET  /integrations/maxim/batches/{id}                              one import with its clusters and drafts
//	POST /integrations/maxim/batches/{id}/draft                        clusters to pending tasks (member)
//	POST /integrations/maxim/batches/{id}/tasks/{suite}/{task}/accept  what should have happened; queues readiness (member)
//	POST /integrations/maxim/batches/{id}/tasks/{suite}/{task}/reject  drop a draft (member)
//	POST /integrations/maxim/batches/{id}/tasks/{suite}/{task}/readiness  run the readiness check again (member)

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/maxim"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/tracebulk"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"github.com/valyala/fasthttp"
)

// MaximPullTimeout bounds one bulk pull.
const MaximPullTimeout = 5 * time.Minute

// readinessJobKind is the job that runs a task's readiness check.
const readinessJobKind = "suite.admit"

// MaximBatchCreate is POST /integrations/maxim/batches.
type MaximBatchCreate struct {
	Filter tracebulk.Filter `json:"filter"`
}

// MaximBatchList is GET /integrations/maxim/batches.
type MaximBatchList struct {
	Connected bool                `json:"connected"`
	Batches   []tracebulk.Summary `json:"batches"`
}

// MaximDraftRequest is POST /integrations/maxim/batches/{id}/draft.
type MaximDraftRequest struct {
	Suite      string   `json:"suite"`
	Clusters   []string `json:"clusters,omitempty"`
	PerCluster int      `json:"per_cluster,omitempty"`
	Kind       string   `json:"kind,omitempty"`
}

// MaximDraftResult answers a draft.
type MaximDraftResult struct {
	Batch    *tracebulk.Batch      `json:"batch"`
	Tasks    []tracebulk.TaskDraft `json:"tasks"`
	Warnings []string              `json:"warnings"`
}

// MaximReviewRequest is POST .../accept.
type MaximReviewRequest struct {
	// Answer is the right answer (question and answer tasks: required).
	Answer string `json:"answer,omitempty"`
	// Rubric replaces the generated rubric; empty keeps it.
	Rubric string `json:"rubric,omitempty"`
	// Instruction replaces the drafted request; empty keeps the recorded one.
	Instruction string `json:"instruction,omitempty"`
	// SkipReadiness accepts without queueing the readiness check.
	SkipReadiness bool `json:"skip_readiness,omitempty"`
}

// MaximRejectRequest is POST .../reject.
type MaximRejectRequest struct {
	Reason string `json:"reason"`
}

// MaximTaskResult answers accept, reject and readiness.
type MaximTaskResult struct {
	Task tracebulk.TaskDraft `json:"task"`
	// Message says what happened to the readiness check, in words.
	Message string `json:"message,omitempty"`
}

type maximTasksHandler struct{ env OrgEnv }

func init() {
	RegisterOrgArea("maximtasks", func(env OrgEnv, r *router.Router) {
		h := &maximTasksHandler{env: env}
		base := APIPrefix + "/integrations/maxim/batches"
		r.GET(base, env.R(h.list))
		r.POST(base, env.W(h.create))
		r.GET(base+"/{id}", env.R(h.get))
		r.POST(base+"/{id}/draft", env.W(h.draft))
		r.POST(base+"/{id}/tasks/{suite}/{task}/accept", env.W(h.accept))
		r.POST(base+"/{id}/tasks/{suite}/{task}/reject", env.W(h.reject))
		r.POST(base+"/{id}/tasks/{suite}/{task}/readiness", env.W(h.readiness))
	})
}

func (h *maximTasksHandler) ok(ctx *fasthttp.RequestCtx) bool {
	if h.env.Cfg == nil || h.env.Cfg.Root == "" {
		sendUnavailable(ctx, h.env.Mode, "maxim failures (no project folder)")
		return false
	}
	return true
}

func (h *maximTasksHandler) store() *tracebulk.Store {
	return tracebulk.Open(h.env.Cfg.Path(traceinbox.Dir))
}

func (h *maximTasksHandler) benchRoot() string {
	return h.env.Cfg.Path(firstNonEmptyStr(h.env.Cfg.BenchmarksRoot, "benchmarks"))
}

func (h *maximTasksHandler) inbox() *traceinbox.Inbox {
	ib := traceinbox.New(h.env.Cfg.Path(traceinbox.Dir), &traceinbox.Config{}, h.benchRoot())
	ib.ProjectRoot = h.env.Cfg.Root
	return ib
}

func (h *maximTasksHandler) list(ctx *fasthttp.RequestCtx) {
	if !h.ok(ctx) {
		return
	}
	s, err := maxim.Open(h.env.Cfg.Root).Load()
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim connection", err)
		return
	}
	list, err := h.store().List()
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim imports", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, MaximBatchList{Connected: s != nil, Batches: list})
}

func (h *maximTasksHandler) create(ctx *fasthttp.RequestCtx) {
	if !h.ok(ctx) {
		return
	}
	var in MaximBatchCreate
	if !decodeStrict(ctx, h.env.Mode, &in, "body must be {\"filter\": {...}}") {
		return
	}
	if err := in.Filter.Normalize(); err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
		return
	}
	s, err := maxim.Open(h.env.Cfg.Root).Load()
	if err != nil {
		sendDepError(ctx, h.env.Mode, "maxim connection", err)
		return
	}
	if s == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "Maxim is not connected; connect it in Settings > Integrations first")
		return
	}
	if h.env.Services.Keys == nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"key store is not configured: the server needs a master key (HEIMDALL_MASTER_KEY) to read the Maxim API key; ask your server owner")
		return
	}
	work, cancel := workContext(ctx, MaximPullTimeout)
	defer cancel()
	key, err := h.env.Services.Keys.Reveal(work, maximOrg(h.env.Services), s.KeyID)
	if err != nil {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "the stored Maxim API key cannot be read; save it again in Settings > Integrations")
		return
	}
	client := MaximClient
	if h.env.Services.Hosted && client.HTTP == nil {
		client.HTTP = HostedPullClient()
	}
	b, err := tracebulk.PullMaxim(work, h.inbox(), tracebulk.Maxim{Client: client, APIKey: key, Settings: *s},
		tracebulk.PullOptions{Filter: in.Filter, CreatedBy: secretActor(ctx)})
	if err != nil {
		msg := strings.ReplaceAll(err.Error(), key, "[key]")
		if errors.Is(err, maxim.ErrUnauthorized) {
			msg = "Maxim refused the API key; save a new one in Settings > Integrations"
		}
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadGateway, lib.ErrTypeUnavailable, msg)
		return
	}
	if err := h.store().Save(b); err != nil {
		sendDepError(ctx, h.env.Mode, "maxim imports", err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, b)
}

func (h *maximTasksHandler) get(ctx *fasthttp.RequestCtx) {
	if !h.ok(ctx) {
		return
	}
	id := pathParam(ctx, "id")
	b, err := h.store().Get(id)
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	before := readinessOf(b)
	h.refresh(ctx, b)
	if readinessOf(b) != before {
		// Save only what changed, so a page that polls does not rewrite the file.
		if b, err = h.store().Update(id, func(b *tracebulk.Batch) error { h.refresh(ctx, b); return nil }); err != nil {
			h.sendErr(ctx, err)
			return
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, b)
}

func readinessOf(b *tracebulk.Batch) string {
	var sb strings.Builder
	for _, t := range b.Tasks {
		sb.WriteString(t.Readiness.Status + ",")
	}
	return sb.String()
}

// refresh reads each accepted task's readiness from its suite lock and
// readiness job.
func (h *maximTasksHandler) refresh(ctx context.Context, b *tracebulk.Batch) {
	org := maximOrg(h.env.Services)
	tracebulk.RefreshReadiness(h.benchRoot(), b, func(jobID string) bool {
		if h.env.Services.Jobs == nil {
			return false
		}
		j, err := h.env.Services.Jobs.Get(ctx, org, jobID)
		return err == nil && j != nil && (j.Status == jobs.StatusSucceeded || j.Status == jobs.StatusFailed || j.Status == jobs.StatusCancelled)
	})
}

func (h *maximTasksHandler) draft(ctx *fasthttp.RequestCtx) {
	if !h.ok(ctx) {
		return
	}
	var in MaximDraftRequest
	if !decodeStrict(ctx, h.env.Mode, &in, "body must be {\"suite\": ..., \"clusters\": [...]}") {
		return
	}
	work, cancel := workContext(ctx, MaximPullTimeout)
	defer cancel()
	var res *tracebulk.DraftResult
	b, err := h.store().Update(pathParam(ctx, "id"), func(b *tracebulk.Batch) error {
		var err error
		res, err = tracebulk.Draft(work, h.inbox(), b, tracebulk.DraftOptions{Suite: strings.TrimSpace(in.Suite), Clusters: in.Clusters,
			PerCluster: in.PerCluster, Kind: in.Kind}, time.Now().UTC())
		return err
	})
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, MaximDraftResult{Batch: b, Tasks: res.Tasks, Warnings: res.Warnings})
}

func (h *maximTasksHandler) accept(ctx *fasthttp.RequestCtx) {
	if !h.ok(ctx) {
		return
	}
	var in MaximReviewRequest
	if !decodeStrict(ctx, h.env.Mode, &in, "body must be {\"answer\": ..., \"rubric\": ..., \"instruction\": ...}") {
		return
	}
	who := secretActor(ctx)
	h.onTask(ctx, func(b *tracebulk.Batch, t *tracebulk.TaskDraft) (string, error) {
		if err := tracebulk.Accept(h.benchRoot(), h.inbox(), b, t, tracebulk.Review{Answer: in.Answer, Rubric: in.Rubric, Instruction: in.Instruction}, who, time.Now().UTC()); err != nil {
			return "", err
		}
		switch {
		case t.State == tracebulk.StateNeedFiles:
			return "Accepted. Finish these files, then run the readiness check: " + strings.Join(t.EditMe, ", "), nil
		case in.SkipReadiness:
			return "Accepted. Run the readiness check when you are ready.", nil
		}
		return h.queue(ctx, t), nil
	})
}

func (h *maximTasksHandler) reject(ctx *fasthttp.RequestCtx) {
	if !h.ok(ctx) {
		return
	}
	var in MaximRejectRequest
	if !decodeStrict(ctx, h.env.Mode, &in, "body must be {\"reason\": ...}") {
		return
	}
	if strings.TrimSpace(in.Reason) == "" {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "a reject needs a reason")
		return
	}
	who := secretActor(ctx)
	h.onTask(ctx, func(b *tracebulk.Batch, t *tracebulk.TaskDraft) (string, error) {
		return "Rejected; the draft was removed from the benchmark.", tracebulk.Reject(h.benchRoot(), b, t, in.Reason, who, time.Now().UTC())
	})
}

func (h *maximTasksHandler) readiness(ctx *fasthttp.RequestCtx) {
	if !h.ok(ctx) {
		return
	}
	h.onTask(ctx, func(b *tracebulk.Batch, t *tracebulk.TaskDraft) (string, error) {
		if t.State == tracebulk.StateNeedFiles {
			if t.EditMe, _ = tracebulk.EditMeFiles(tracebulk.TaskDir(h.benchRoot(), t)); len(t.EditMe) == 0 {
				t.State = tracebulk.StateAccepted
			}
		}
		if t.State != tracebulk.StateAccepted {
			return "", errors.Join(tracebulk.ErrConflict, errors.New("only an accepted task with no EDIT ME line left runs its readiness check; this one is "+t.State))
		}
		return h.queue(ctx, t), nil
	})
}

// queue starts the task's readiness job and records it; a busy or missing
// job runner is reported in words, never as a failed accept.
func (h *maximTasksHandler) queue(ctx *fasthttp.RequestCtx, t *tracebulk.TaskDraft) string {
	if h.env.Services.Jobs == nil {
		return "Accepted. This server runs no jobs; run the readiness check with `heimdall task admit`."
	}
	params, _ := json.Marshal(map[string]string{"suite": t.Suite, "task": t.ID})
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := h.env.Services.Jobs.Create(work, jobs.NewJob{OrgID: maximOrg(h.env.Services), Kind: readinessJobKind, Params: params,
		CreatedBy: createdBy(auth.PrincipalFrom(ctx))})
	switch {
	case errors.Is(err, jobs.ErrConflict):
		return "Accepted. Another readiness check is running on this benchmark; run this one again when it ends."
	case err != nil:
		return "Accepted, but the readiness check could not start: " + err.Error()
	}
	tracebulk.SetReadiness(t, j.ID, time.Now().UTC())
	return "Accepted. The readiness check is queued as job " + j.ID + "."
}

// onTask runs fn on one task of a batch under the batch lock and answers
// the task.
func (h *maximTasksHandler) onTask(ctx *fasthttp.RequestCtx, fn func(*tracebulk.Batch, *tracebulk.TaskDraft) (string, error)) {
	key := pathParam(ctx, "suite") + "/" + pathParam(ctx, "task")
	var out MaximTaskResult
	_, err := h.store().Update(pathParam(ctx, "id"), func(b *tracebulk.Batch) error {
		t, ok := b.Task(key)
		if !ok {
			return errors.Join(tracebulk.ErrNotFound, errors.New("task "+key+" is not in this import"))
		}
		msg, err := fn(b, t)
		if err != nil {
			return err
		}
		out = MaximTaskResult{Task: *t, Message: msg}
		return nil
	})
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *maximTasksHandler) sendErr(ctx *fasthttp.RequestCtx, err error) {
	switch {
	case errors.Is(err, tracebulk.ErrNotFound) || errors.Is(err, schemas.ErrNotFound) || errors.Is(err, traceinbox.ErrNotFound):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
	case errors.Is(err, tracebulk.ErrConflict) || errors.Is(err, traceinbox.ErrConflict):
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
	default:
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, err.Error())
	}
}
