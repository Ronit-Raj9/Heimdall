package handlers

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/taskcat"
	"github.com/valyala/fasthttp"
)

// Task categories (W4-44): the kind of work of each task of a benchmark,
// kept in the .suite.categories.json sidecar so a label never changes a
// task's fingerprint. Rules label what they can; a person's label wins.

// TaskCategoriesView answers the category routes: task id to its label.
type TaskCategoriesView struct {
	Suite    string                   `json:"suite"`
	Labels   map[string]taskcat.Label `json:"labels"`
	Labelled int                      `json:"labelled,omitempty"` // POST: labels the rules added
}

// TaskCategoryRequest is the body of PUT /suites/{id}/tasks/{task}/category.
type TaskCategoryRequest struct {
	Category string `json:"category"`
}

func init() {
	RegisterOrgArea("categories_w444", func(env OrgEnv, r *router.Router) {
		h := &taskCategoriesHandler{mode: env.Mode, suites: env.Services.Suites}
		r.GET(APIPrefix+"/suites/{id}/categories", env.R(h.get))
		r.POST(APIPrefix+"/suites/{id}/categories", env.W(refuseBuiltin(env.Mode, h.suites, h.backfill)))
		r.PUT(APIPrefix+"/suites/{id}/tasks/{task}/category", env.W(refuseBuiltin(env.Mode, h.suites, h.set)))
	})
}

type taskCategoriesHandler struct {
	mode   schemas.Mode
	suites SuiteSource
}

// load resolves the suite of the path, or answers and returns nil.
func (h *taskCategoriesHandler) load(ctx *fasthttp.RequestCtx) *schemas.Suite {
	if h.suites == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "benchmarks are not available on this server")
		return nil
	}
	id, ok := idParam(ctx, h.mode, "id", "suite")
	if !ok {
		return nil
	}
	work, cancel := workContext(ctx, readTimeout)
	defer cancel()
	ids, err := h.suites.SuiteIDs(work)
	if err != nil {
		sendDepError(ctx, h.mode, "list suites", err)
		return nil
	}
	if !slices.Contains(ids, id) {
		sendNotFound(ctx, h.mode, "suite", id)
		return nil
	}
	s, err := h.suites.LoadSuite(work, id)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "load benchmark "+id+": "+err.Error())
		return nil
	}
	return s
}

func (h *taskCategoriesHandler) send(ctx *fasthttp.RequestCtx, s *schemas.Suite, labelled int) {
	labels, err := taskcat.Read(s.Root)
	if err != nil {
		sendDepError(ctx, h.mode, "read the task categories of "+s.ID, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, TaskCategoriesView{Suite: s.ID, Labels: labels, Labelled: labelled})
}

// get handles GET /suites/{id}/categories.
func (h *taskCategoriesHandler) get(ctx *fasthttp.RequestCtx) {
	if s := h.load(ctx); s != nil {
		h.send(ctx, s, 0)
	}
}

// backfill handles POST /suites/{id}/categories: every task without a label
// gets one from the rules; a task the rules cannot label stays without one.
func (h *taskCategoriesHandler) backfill(ctx *fasthttp.RequestCtx) {
	s := h.load(ctx)
	if s == nil {
		return
	}
	have, err := taskcat.Read(s.Root)
	if err != nil {
		sendDepError(ctx, h.mode, "read the task categories of "+s.ID, err)
		return
	}
	n := 0
	for _, t := range s.Tasks {
		if _, ok := have[t.ID]; ok {
			continue
		}
		l, ok := taskcat.Rules(categoryInputs(t))
		if !ok {
			continue
		}
		if err := taskcat.Set(s.Root, t.ID, l); err != nil {
			sendDepError(ctx, h.mode, "label "+s.ID+"/"+t.ID, err)
			return
		}
		n++
	}
	h.send(ctx, s, n)
}

// set handles PUT /suites/{id}/tasks/{task}/category: a person's label.
func (h *taskCategoriesHandler) set(ctx *fasthttp.RequestCtx) {
	var req TaskCategoryRequest
	dec := json.NewDecoder(bytes.NewReader(ctx.PostBody()))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil || !taskcat.Valid(req.Category) {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			`body must be {"category"} with one of: `+strings.Join(taskcat.All(), ", "))
		return
	}
	s := h.load(ctx)
	if s == nil {
		return
	}
	taskID, ok := idParam(ctx, h.mode, "task", "task")
	if !ok {
		return
	}
	if s.TaskByID(taskID) == nil {
		sendNotFound(ctx, h.mode, "task", s.ID+"/"+taskID)
		return
	}
	if err := taskcat.Set(s.Root, taskID, taskcat.Label{Category: req.Category, Source: taskcat.SourcePerson, Sure: true, Why: actorName(ctx)}); err != nil {
		sendDepError(ctx, h.mode, "label "+s.ID+"/"+taskID, err)
		return
	}
	h.send(ctx, s, 0)
}

// categoryInputs are the facts the rules read from a task's own files: the
// paths its reference diff changes, its title, and for a trace task the
// tool names of the recorded session (tools.observed.json).
func categoryInputs(t *schemas.Task) taskcat.Inputs {
	in := taskcat.Inputs{Task: t, CommitSubject: t.Title}
	if strings.HasSuffix(t.Controls.Reference, ".diff") {
		if data, err := os.ReadFile(filepath.Join(t.Dir, t.Controls.Reference)); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if p, ok := strings.CutPrefix(line, "+++ b/"); ok {
					in.ChangedFiles = append(in.ChangedFiles, strings.TrimSpace(p))
				}
			}
		}
	}
	if data, err := os.ReadFile(filepath.Join(t.Dir, "tools.observed.json")); err == nil {
		var doc struct {
			Calls []struct {
				Name string `json:"name"`
			} `json:"calls"`
		}
		if json.Unmarshal(data, &doc) == nil {
			for _, c := range doc.Calls {
				in.SessionTools = append(in.SessionTools, c.Name)
			}
		}
	}
	return in
}
