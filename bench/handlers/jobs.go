package handlers

// Jobs area (P4): the job API (docs/platform-design.md, Jobs). Jobs belong to
// the caller's organization: the principal's org, or the local org when no
// auth middleware ran (local mode, where the write token guards writes).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// JobsAPI is the job runner as the API uses it (*jobs.Runner).
type JobsAPI interface {
	Kinds() []string
	Create(ctx context.Context, n jobs.NewJob) (*jobs.Job, error)
	Get(ctx context.Context, orgID, id string) (*jobs.Job, error)
	List(ctx context.Context, orgID string, f jobs.Filter) (*jobs.Page, error)
	Logs(ctx context.Context, orgID, id string, since int64, limit int) (*jobs.LogPage, error)
	Cancel(ctx context.Context, orgID, id string) (*jobs.Job, error)
}

var _ JobsAPI = (*jobs.Runner)(nil)

// ErrNotRunning answers a cancel of an experiment with no active run (409).
var ErrNotRunning = errors.New("not running")

// JobList is GET /jobs: a page of jobs and the kinds POST /jobs accepts.
type JobList struct {
	Jobs  []jobs.Job `json:"jobs"`
	Next  string     `json:"next,omitempty"`
	Kinds []string   `json:"kinds"`
}

// JobCreate is the body of POST /jobs.
type JobCreate struct {
	Kind   string          `json:"kind"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Job log streaming.
const (
	jobLogPollEvery = 500 * time.Millisecond
	jobLogMaxStream = 6 * time.Hour
)

// JobsHandler serves /api/bench/jobs.
type JobsHandler struct {
	mode  schemas.Mode
	api   JobsAPI
	write lib.Middleware
	audit AuditFunc // hosted mode: one audit row per create and cancel; nil writes none

	stop     chan struct{}
	stopOnce sync.Once
}

// NewJobsHandler creates the jobs handler. write guards POST routes in local
// mode (the write token); in hosted mode the dispatcher's policy and the
// role checks here guard them. api may be nil (503).
func NewJobsHandler(mode schemas.Mode, api JobsAPI, write lib.Middleware) *JobsHandler {
	if r, ok := api.(*jobs.Runner); ok && r == nil {
		api = nil // a nil runner in the interface answers 503 too
	}
	if write == nil {
		write = func(next fasthttp.RequestHandler) fasthttp.RequestHandler { return next }
	}
	return &JobsHandler{mode: mode, api: api, write: write, stop: make(chan struct{})}
}

// WithAudit sets the audit writer of the write routes (hosted mode, where
// platform handlers write their own audit rows).
func (h *JobsHandler) WithAudit(a AuditFunc) *JobsHandler {
	h.audit = a
	return h
}

// RegisterRoutes registers the job routes.
func (h *JobsHandler) RegisterRoutes(r *router.Router, middlewares ...lib.Middleware) {
	writeMW := append(slices.Clone(middlewares), h.write)
	p := APIPrefix + "/jobs"
	r.GET(p, lib.ChainMiddlewares(h.list, middlewares...))
	r.POST(p, lib.ChainMiddlewares(h.create, writeMW...))
	r.GET(p+"/{id}", lib.ChainMiddlewares(h.get, middlewares...))
	r.GET(p+"/{id}/logs", lib.ChainMiddlewares(h.logs, middlewares...))
	r.POST(p+"/{id}/cancel", lib.ChainMiddlewares(h.cancel, writeMW...))
	r.POST(p+"/{id}/priority", lib.ChainMiddlewares(h.priority, writeMW...)) // Z10: jobpriority.go
}

// Close ends open log streams.
func (h *JobsHandler) Close() { h.stopOnce.Do(func() { close(h.stop) }) }

// org resolves the caller's organization and checks its role.
func (h *JobsHandler) org(ctx *fasthttp.RequestCtx, min auth.Role) (string, *auth.Principal, bool) {
	if h.api == nil {
		sendUnavailable(ctx, h.mode, "job runner")
		return "", nil, false
	}
	p, ok := auth.Lookup(ctx)
	if !ok {
		// No auth middleware: local mode's single organization.
		return auth.LocalOrgID, auth.Local(), true
	}
	if !p.IsAuthenticated() || p.OrgID == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnauthorized, lib.ErrTypeUnauthorized, "sign in to use jobs")
		return "", nil, false
	}
	if !p.Can(min) {
		lib.SendError(ctx, h.mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "jobs need the "+string(min)+" role")
		return "", nil, false
	}
	return p.OrgID, p, true
}

// list handles GET /jobs?kind=&status=&target=&limit=&before=.
func (h *JobsHandler) list(ctx *fasthttp.RequestCtx) {
	org, _, ok := h.org(ctx, auth.RoleViewer)
	if !ok {
		return
	}
	q := ctx.QueryArgs()
	f := jobs.Filter{Kind: string(q.Peek("kind")), Status: jobs.Status(q.Peek("status")), Target: string(q.Peek("target")),
		Before: string(q.Peek("before"))}
	if f.Status != "" && !f.Status.Valid() {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "status must be queued, running, succeeded, failed or cancelled")
		return
	}
	if raw := string(q.Peek("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 500 {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "limit must be 1..500")
			return
		}
		f.Limit = n
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	page, err := h.api.List(work, org, f)
	if err != nil {
		h.sendError(ctx, "list jobs", err)
		return
	}
	kinds := h.api.Kinds()
	if kinds == nil {
		kinds = []string{}
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, JobList{Jobs: plainJobs(page.Jobs), Next: page.Next, Kinds: kinds})
}

// create handles POST /jobs with {"kind": ..., "params": {...}}: member role,
// a kind the API may create, params the kind validates.
func (h *JobsHandler) create(ctx *fasthttp.RequestCtx) {
	org, p, ok := h.org(ctx, auth.RoleMember)
	if !ok {
		return
	}
	var body JobCreate
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "body must be {\"kind\": ..., \"params\": {...}}: "+err.Error())
		return
	}
	if strings.TrimSpace(body.Kind) == "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "kind is required; one of "+strings.Join(h.api.Kinds(), ", "))
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := h.api.Create(work, jobs.NewJob{OrgID: org, Kind: body.Kind, Params: body.Params, CreatedBy: createdBy(p)})
	if err != nil {
		h.sendError(ctx, "create job", err)
		return
	}
	if h.audit != nil {
		h.audit(ctx, org, "job.create", "job", j.ID, map[string]any{"kind": j.Kind, "target": j.Target})
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, j)
}

// get handles GET /jobs/{id}.
func (h *JobsHandler) get(ctx *fasthttp.RequestCtx) {
	org, _, ok := h.org(ctx, auth.RoleViewer)
	if !ok {
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "job")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := h.api.Get(work, org, id)
	if err != nil {
		h.sendError(ctx, "job "+id, err)
		return
	}
	if j != nil && j.CreatedBy != "" {
		v := *j
		v.CreatedBy, v.CreatedByName = newActorNames(ctx).show(j.CreatedBy)
		j = &v
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, plainJob(j))
}

// cancel handles POST /jobs/{id}/cancel: 202 with the job; a queued job is
// cancelled at once, a running one when its handler stops.
func (h *JobsHandler) cancel(ctx *fasthttp.RequestCtx) {
	org, _, ok := h.org(ctx, auth.RoleMember)
	if !ok {
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "job")
	if !ok {
		return
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	j, err := h.api.Cancel(work, org, id)
	if err != nil {
		h.sendError(ctx, "cancel job "+id, err)
		return
	}
	if h.audit != nil {
		h.audit(ctx, org, "job.cancel", "job", j.ID, map[string]any{"kind": j.Kind, "status": string(j.Status)})
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, j)
}

// logs handles GET /jobs/{id}/logs?since=<seq>&limit=: the lines after since
// as JSON, or server-sent events (one "log" event per line, an "end" event
// once the job ended and every line was sent) for a client that accepts
// text/event-stream.
func (h *JobsHandler) logs(ctx *fasthttp.RequestCtx) {
	org, _, ok := h.org(ctx, auth.RoleViewer)
	if !ok {
		return
	}
	id, ok := idParam(ctx, h.mode, "id", "job")
	if !ok {
		return
	}
	var since int64
	if raw := string(ctx.QueryArgs().Peek("since")); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "since must be a non-negative log sequence number")
			return
		}
		since = n
	}
	if raw := string(ctx.Request.Header.Peek("Last-Event-ID")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > since {
			since = n
		}
	}
	limit := 0
	if raw := string(ctx.QueryArgs().Peek("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 2000 {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "limit must be 1..2000")
			return
		}
		limit = n
	}
	work, cancel := workContext(ctx, readTimeout)
	page, err := h.api.Logs(work, org, id, since, limit)
	cancel()
	if err != nil {
		h.sendError(ctx, "logs of job "+id, err)
		return
	}
	if !bytes.Contains(ctx.Request.Header.Peek(fasthttp.HeaderAccept), []byte("text/event-stream")) {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, plainLogPage(page))
		return
	}
	ctx.SetStatusCode(fasthttp.StatusOK)
	ctx.SetContentType("text/event-stream; charset=utf-8")
	ctx.Response.Header.Set("Cache-Control", "no-store")
	ctx.Response.Header.Set("X-Accel-Buffering", "no")
	ctx.Response.Header.Set(lib.ModeHeader, string(h.mode))
	api, mode, stop, allowed := h.api, h.mode, h.stop, lib.StreamGuardOf(ctx)
	ctx.SetBodyStreamWriter(func(w *bufio.Writer) {
		streamJobLogs(w, mode, plainLogsAPI{api}, org, id, plainLogPage(page), stop, allowed)
	})
}

// streamJobLogs writes log lines as SSE until the job ended and its last
// line went out, the client left, the server stops, the stream is old, or
// allowed (the request's stream guard) no longer lets the caller read.
func streamJobLogs(w *bufio.Writer, mode schemas.Mode, api JobsAPI, org, id string, page *jobs.LogPage, stop <-chan struct{}, allowed func() bool) {
	if !writeSSE(w, "retry: "+strconv.Itoa(sseRetryMillis)+"\n\n") {
		return
	}
	poll := time.NewTicker(jobLogPollEvery)
	defer poll.Stop()
	keepAlive := time.NewTicker(sseKeepAlive)
	defer keepAlive.Stop()
	deadline := time.NewTimer(jobLogMaxStream)
	defer deadline.Stop()
	for {
		for _, l := range page.Lines {
			body, err := sonic.ConfigStd.Marshal(l)
			if err != nil {
				continue
			}
			if !writeSSE(w, "id: "+strconv.FormatInt(l.Seq, 10)+"\nevent: log\ndata: "+string(body)+"\n\n") {
				return
			}
		}
		if page.Status.Terminal() && len(page.Lines) == 0 {
			data, err := sonic.ConfigStd.Marshal(map[string]any{"status": page.Status, "dropped": page.Dropped, "mode": mode})
			if err == nil {
				writeSSE(w, "event: "+sseEndEvent+"\ndata: "+string(data)+"\n\n")
			}
			return
		}
		select {
		case <-stop:
			return
		case <-deadline.C:
			return
		case <-keepAlive.C:
			if !writeSSE(w, ": keep-alive\n\n") {
				return
			}
			continue
		case <-poll.C:
		}
		if !allowed() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
		next, err := api.Logs(ctx, org, id, page.Next, 0)
		cancel()
		if err != nil {
			return
		}
		page = next
	}
}

// sendError maps job errors to statuses.
func (h *JobsHandler) sendError(ctx *fasthttp.RequestCtx, what string, err error) {
	sendJobError(ctx, h.mode, what, err)
}

func sendJobError(ctx *fasthttp.RequestCtx, mode schemas.Mode, what string, err error) {
	switch {
	case errors.Is(err, jobs.ErrNotFound):
		lib.SendError(ctx, mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, what+": "+err.Error())
	case errors.Is(err, jobs.ErrUnknownKind), errors.Is(err, jobs.ErrNotCreatable), errors.Is(err, jobs.ErrInvalidParams):
		lib.SendError(ctx, mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, what+": "+err.Error())
	case errors.Is(err, jobs.ErrConflict), errors.Is(err, jobs.ErrFinished), errors.Is(err, ErrNotRunning):
		lib.SendError(ctx, mode, fasthttp.StatusConflict, lib.ErrTypeConflict, what+": "+err.Error())
	default:
		sendDepError(ctx, mode, what, err)
	}
}

// createdBy is the created_by of a job a principal starts.
func createdBy(p *auth.Principal) string {
	if p == nil {
		return "system"
	}
	if id := p.ActorID(); id != "" {
		return id
	}
	return "system"
}
