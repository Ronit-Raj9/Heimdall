package handlers

// Authoring helper (lane Z19, INFRA_I3 items 6-8): the review queue of
// generated tasks (a person accepts or rejects each; until then the task
// counts in no evaluation, gate, release or report card), the cheap quality
// checks (duplicate tasks, overlap with the built-in library) and the
// automatic held-out split with its canary. Drafting itself is the
// authoring.assist job (POST /jobs), because it calls a model.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/taskgates"
	"github.com/valyala/fasthttp"
)

// GeneratedTask is one generated task in the review queue.
type GeneratedTask struct {
	Task      string                      `json:"task"`
	Title     string                      `json:"title,omitempty"`
	Split     string                      `json:"split"`
	Review    string                      `json:"review"` // pending | accepted | rejected
	Generated []schemas.GeneratedField    `json:"generated"`
	CostUSD   float64                     `json:"cost_usd"`
	Decision  *schemas.TaskReviewDecision `json:"decision,omitempty"` // the person's decision, when it still holds
	// Stale is set when a decision exists but fields were drafted again
	// since, so the task is pending once more.
	Stale bool `json:"stale,omitempty"`
}

// GeneratedTaskList answers GET /suites/{id}/generated.
type GeneratedTaskList struct {
	Suite    string          `json:"suite"`
	Tasks    []GeneratedTask `json:"tasks"`
	Pending  int             `json:"pending"`
	Accepted int             `json:"accepted"`
	Rejected int             `json:"rejected"`
	Note     string          `json:"note"`
}

// TaskReviewRequest is the body of PUT /suites/{id}/tasks/{task}/review-state.
type TaskReviewRequest struct {
	Decision string `json:"decision"` // accepted | rejected
	Note     string `json:"note,omitempty"`
}

// TaskReviewResult answers PUT /suites/{id}/tasks/{task}/review-state.
type TaskReviewResult struct {
	Suite    string                     `json:"suite"`
	Task     string                     `json:"task"`
	Review   string                     `json:"review"`
	Decision schemas.TaskReviewDecision `json:"decision"`
}

// HeldOutSplitRequest is the body of POST /suites/{id}/held-out.
type HeldOutSplitRequest struct {
	taskgates.SplitPlan
	DryRun bool `json:"dry_run,omitempty"`
}

// HeldOutSplitResult answers POST /suites/{id}/held-out.
type HeldOutSplitResult struct {
	taskgates.SplitResult
	DryRun  bool     `json:"dry_run"`
	Written []string `json:"written,omitempty"` // files rewritten, relative to the suite
}

type authoringGates struct {
	mode     schemas.Mode
	projects projectfs.Store
	suites   SuiteSource
	// library loads the library index; a field so tests swap it.
	library func(ctx context.Context) *taskgates.Index
}

const generatedNote = "A generated task counts in no evaluation, gate, release or report card until a person accepts it. Run the readiness check first: it shows whether each drafted wrong answer fails."

func init() {
	RegisterOrgArea("authoringgates", func(env OrgEnv, r *router.Router) {
		h := &authoringGates{mode: env.Mode, projects: env.Services.Projects, suites: env.Services.Suites}
		h.library = h.libraryIndex
		r.GET(APIPrefix+"/suites/{id}/generated", env.R(h.listGenerated))
		r.PUT(APIPrefix+"/suites/{id}/tasks/{task}/review-state", env.W(refuseBuiltin(env.Mode, h.suites, h.setReview)))
		r.GET(APIPrefix+"/suites/{id}/quality", env.R(h.quality))
		r.POST(APIPrefix+"/suites/{id}/held-out", env.W(refuseBuiltin(env.Mode, h.suites, h.heldOut)))
	})
}

func (h *authoringGates) load(ctx *fasthttp.RequestCtx) (*schemas.Suite, bool) {
	id, ok := idParam(ctx, h.mode, "id", "suite")
	if !ok {
		return nil, false
	}
	if h.suites == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable, "benchmarks are not available on this server")
		return nil, false
	}
	s, err := h.suites.LoadSuite(ctx, id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "no such file") {
			sendNotFound(ctx, h.mode, "suite", id)
			return nil, false
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "load "+id+": "+err.Error())
		return nil, false
	}
	return s, true
}

// listGenerated handles GET /suites/{id}/generated.
func (h *authoringGates) listGenerated(ctx *fasthttp.RequestCtx) {
	s, ok := h.load(ctx)
	if !ok {
		return
	}
	f, err := suite.ReadReviewFile(s.Root)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	out := GeneratedTaskList{Suite: s.ID, Tasks: []GeneratedTask{}, Note: generatedNote}
	names := newActorNames(ctx)
	for _, t := range s.Tasks {
		if !t.IsGenerated() {
			continue
		}
		g := GeneratedTask{Task: t.ID, Title: t.Title, Split: taskSplitOf(t), Review: schemas.TaskReviewState(t, f), Generated: t.GeneratedFields()}
		if g.Generated == nil {
			g.Generated = []schemas.GeneratedField{}
		}
		g.CostUSD = generatedCost(g.Generated)
		if d, ok := f.Decisions[t.ID]; ok {
			if d.Generated == schemas.GeneratedFingerprint(t.GeneratedFields()) {
				g.Decision = namedDecision(names, d)
			} else {
				g.Stale = true
			}
		}
		switch g.Review {
		case schemas.ReviewPending:
			out.Pending++
		case schemas.ReviewAccepted:
			out.Accepted++
		case schemas.ReviewRejected:
			out.Rejected++
		}
		out.Tasks = append(out.Tasks, g)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// suiteRel is the suite's folder inside the project store's benchmarks
// area, or false when the store does not hold it.
func (h *authoringGates) suiteRel(s *schemas.Suite) (string, bool) {
	if h.projects == nil {
		return "", false
	}
	base, err := h.projects.Resolve(projectfs.AreaBenchmarks, ".")
	if err != nil {
		return "", false
	}
	a, err1 := filepath.EvalSymlinks(base)
	b, err2 := filepath.EvalSymlinks(s.Root)
	if err1 != nil || err2 != nil {
		a, b = filepath.Clean(base), filepath.Clean(s.Root)
	}
	rel, err := filepath.Rel(a, b)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func (h *authoringGates) needStore(ctx *fasthttp.RequestCtx, s *schemas.Suite) (string, bool) {
	rel, ok := h.suiteRel(s)
	if !ok {
		lib.SendError(ctx, h.mode, fasthttp.StatusServiceUnavailable, lib.ErrTypeUnavailable,
			"benchmark "+s.ID+" is not in this project's file store, so it can not be changed from here")
		return "", false
	}
	return rel, true
}

// setReview handles PUT /suites/{id}/tasks/{task}/review-state.
func (h *authoringGates) setReview(ctx *fasthttp.RequestCtx) {
	s, ok := h.load(ctx)
	if !ok {
		return
	}
	taskID, ok := idParam(ctx, h.mode, "task", "task")
	if !ok {
		return
	}
	t := s.TaskByID(taskID)
	if t == nil {
		sendNotFound(ctx, h.mode, "task", s.ID+"/"+taskID)
		return
	}
	var req TaskReviewRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, `body must be {"decision": "accepted"|"rejected", "note"?}: `+err.Error())
		return
	}
	by := actorName(ctx)
	d, err := suite.ReviewDecision(t, req.Decision, by, req.Note, time.Time{})
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	rel, ok := h.needStore(ctx, s)
	if !ok {
		return
	}
	path := rel + "/" + schemas.TaskReviewFileName
	old, _, err := h.projects.Read(ctx, projectfs.AreaBenchmarks, path)
	if err != nil && !errors.Is(err, projectfs.ErrNotFound) {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "read "+schemas.TaskReviewFileName+": "+err.Error())
		return
	}
	f, err := suite.ReadReviewFile(s.Root)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	f.Suite = s.ID
	f.Decisions[taskID] = d
	data, err := suite.EncodeReviewFile(f)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	ifMatch := ""
	if old != nil {
		ifMatch = shaOf(old)
	}
	if _, err := h.projects.Write(ctx, projectfs.AreaBenchmarks, path, data,
		projectfs.WriteOpts{Actor: by, Note: "task " + taskID + " " + d.Decision, IfMatch: ifMatch}); err != nil {
		if errors.Is(err, projectfs.ErrConflict) {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "another review was saved meanwhile; reload and decide again")
			return
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, "write "+schemas.TaskReviewFileName+": "+err.Error())
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &TaskReviewResult{Suite: s.ID, Task: taskID, Review: d.Decision, Decision: d})
}

// libraryIndex indexes the built-in library (every other suite when the
// source marks none built in).
func (h *authoringGates) libraryIndex(ctx context.Context) *taskgates.Index {
	ids, err := h.suites.SuiteIDs(ctx)
	if err != nil {
		return taskgates.NewIndex(nil)
	}
	o, marks := h.suites.(suiteOrigin)
	var libIDs []string
	for _, id := range ids {
		if !marks || o.SuiteBuiltin(id) {
			libIDs = append(libIDs, id)
		}
	}
	return taskgates.LibraryIndex(ctx, libIDs, h.suites.LoadSuite)
}

// quality handles GET /suites/{id}/quality.
func (h *authoringGates) quality(ctx *fasthttp.RequestCtx) {
	s, ok := h.load(ctx)
	if !ok {
		return
	}
	threshold := 0.0
	if v := string(ctx.QueryArgs().Peek("threshold")); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err != nil || f < 0.5 || f > 1 {
			lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, "threshold must be a number from 0.5 to 1")
			return
		}
		threshold = f
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, taskgates.Check(s, h.library(ctx), threshold))
}

// storeWriter writes suite edits through the project store.
type storeWriter struct{ ps projectfs.Store }

func (w storeWriter) Write(ctx context.Context, rel string, data []byte, actor, note, ifMatch string) error {
	_, err := w.ps.Write(ctx, projectfs.AreaBenchmarks, rel, data, projectfs.WriteOpts{Actor: actor, Note: note, IfMatch: ifMatch})
	return err
}

// heldOut handles POST /suites/{id}/held-out.
func (h *authoringGates) heldOut(ctx *fasthttp.RequestCtx) {
	s, ok := h.load(ctx)
	if !ok {
		return
	}
	var req HeldOutSplitRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest,
			`body must be {"held_out_pct","seed"?,"stratify_by"?,"time_after"?,"confirm"?,"dry_run"?}: `+err.Error())
		return
	}
	res, edits, err := taskgates.PlanSplit(s, req.SplitPlan, h.library(ctx))
	switch {
	case errors.Is(err, taskgates.ErrPublicHeldOut):
		lib.SendJSON(ctx, h.mode, fasthttp.StatusConflict, &HeldOutSplitResult{SplitResult: *res, DryRun: true})
		return
	case err != nil:
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	out := HeldOutSplitResult{SplitResult: *res, DryRun: req.DryRun}
	if req.DryRun || len(edits) == 0 {
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
		return
	}
	rel, ok := h.needStore(ctx, s)
	if !ok {
		return
	}
	if err := taskgates.WriteEditsVia(ctx, storeWriter{h.projects}, rel, edits, actorName(ctx), "held-out split"); err != nil {
		if errors.Is(err, projectfs.ErrConflict) {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "a task file changed meanwhile; reload and split again: "+err.Error())
			return
		}
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return
	}
	for _, e := range edits {
		out.Written = append(out.Written, e.Rel)
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// namedDecision is a review decision as an answer shows it: the reviewer by
// display name (by_name), an email left out for a caller who is not an org
// admin.
func namedDecision(names *actorNames, d schemas.TaskReviewDecision) *schemas.TaskReviewDecision {
	d.By, d.ByName = names.show(d.By)
	return &d
}

// generatedCost is what drafting a task's generated fields cost: each assist
// call once (fix wave 3, N278). Fields written before the fix carry their
// call's whole cost each, so fields of one call (same op, time and prompt)
// are summed once; fields drafted since carry it on the first field only.
func generatedCost(fields []schemas.GeneratedField) float64 {
	seen := map[string]float64{}
	sum := 0.0
	for _, f := range fields {
		if f.At == "" {
			sum += f.CostUSD
			continue
		}
		k := f.Op + "|" + f.At + "|" + f.PromptSHA
		if f.CostUSD > seen[k] {
			sum += f.CostUSD - seen[k]
			seen[k] = f.CostUSD
		}
	}
	return sum
}
