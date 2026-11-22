package handlers

// Annotation queues (lane Y15): org queues of evaluation runs or judge
// decisions that signed-in people label correct or incorrect, K people
// per item, optionally blind (agent and version hidden). Nobody sees a
// peer's label or the judge's verdict before giving their own. Agreement
// between people (Cohen's and Fleiss' kappa) and with the judge is shown
// per queue; disagreements wait for the queue owner or an admin to decide.
// A resolved item of a queue tied to a benchmark is appended to that
// benchmark's judge calibration labels (calibration/labels.jsonl, source
// human_review, ref queue/<queue>/<item>), the same store the Calibration
// tab and `judge calibrate` read.

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/annotate"
	"github.com/maximhq/bifrost/bench/calibration"
	"github.com/maximhq/bifrost/bench/datapolicy"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
)

// AnnotationQueueRequest is the body of POST and PUT
// /annotation/queues[/{id}]. On PUT a missing field keeps its value.
type AnnotationQueueRequest struct {
	Name             *string   `json:"name,omitempty"`
	Description      *string   `json:"description,omitempty"`
	Instructions     *string   `json:"instructions,omitempty"`
	Benchmark        *string   `json:"benchmark,omitempty"`
	Kind             *string   `json:"kind,omitempty"`
	ReviewersPerItem *int      `json:"reviewers_per_item,omitempty"`
	Blind            *bool     `json:"blind,omitempty"`
	Assignment       *string   `json:"assignment,omitempty"`
	Reviewers        *[]string `json:"reviewers,omitempty"`
	Archived         *bool     `json:"archived,omitempty"`
}

// AnnotationQueueView is one queue with its counts.
type AnnotationQueueView struct {
	annotate.Queue
	Counts annotate.Counts `json:"counts"`
	// Mine counts items the caller can label now.
	Mine int `json:"mine"`
	// CanManage is true for the queue's creator and org admins.
	CanManage bool `json:"can_manage"`
	CanLabel  bool `json:"can_label"`
}

// AnnotationQueueList answers GET /annotation/queues.
type AnnotationQueueList struct {
	Queues []AnnotationQueueView `json:"queues"`
}

// AnnotationItemsRequest is the body of POST /annotation/queues/{id}/items:
// runs of an evaluation's experiment, or items given directly.
type AnnotationItemsRequest struct {
	// Experiment adds the sealed runs of this experiment; Sample keeps at
	// most that many (0: every run), and Stratify takes them evenly from
	// the runs the judge passed and failed.
	Experiment string `json:"experiment,omitempty"`
	Sample     int    `json:"sample,omitempty"`
	Stratify   bool   `json:"stratify,omitempty"`
	// Items are added as given (answers produced elsewhere).
	Items []annotate.Item `json:"items,omitempty"`
}

// AnnotationItemsResult answers POST /annotation/queues/{id}/items.
type AnnotationItemsResult struct {
	Added     int      `json:"added"`
	Duplicate int      `json:"duplicate"`
	Skipped   []string `json:"skipped"`
}

// AnnotationItemView is an item as the caller may see it: in a blind
// queue the agent, version, run and experiment are hidden from labellers,
// and peer labels and the judge's verdict stay hidden until the caller
// has labelled (or the item is done).
type AnnotationItemView struct {
	ID           string               `json:"id"`
	State        string               `json:"state"`
	Task         string               `json:"task"`
	Question     string               `json:"question,omitempty"`
	Answer       string               `json:"answer"`
	AnswerFrom   string               `json:"answer_from,omitempty"` // answer, files or final_message (annotate.AnswerFrom*)
	RunID        string               `json:"run_id,omitempty"`
	Experiment   string               `json:"experiment,omitempty"`
	Agent        string               `json:"agent,omitempty"`
	AgentVersion string               `json:"agent_version,omitempty"`
	Hidden       []string             `json:"hidden,omitempty"` // what blind mode or label order hides
	JudgeLabel   string               `json:"judge_label,omitempty"`
	JudgeVotes   []string             `json:"judge_votes,omitempty"`
	JudgeReason  string               `json:"judge_reason,omitempty"`
	Labels       []annotate.Label     `json:"labels"`
	LabelCount   int                  `json:"label_count"`
	MyLabel      string               `json:"my_label,omitempty"`
	Resolution   *annotate.Resolution `json:"resolution,omitempty"`
	Calibration  string               `json:"calibration,omitempty"`
	CalibError   string               `json:"calibration_error,omitempty"`
	Assigned     []string             `json:"assigned,omitempty"`
	AddedAt      time.Time            `json:"added_at"`
}

// AnnotationItemList answers GET /annotation/queues/{id}/items.
type AnnotationItemList struct {
	Queue AnnotationQueueView  `json:"queue"`
	Items []AnnotationItemView `json:"items"`
	Total int                  `json:"total"`
}

// AnnotationNext answers POST /annotation/queues/{id}/next: the item to
// label, or none when the caller is done.
type AnnotationNext struct {
	Queue AnnotationQueueView `json:"queue"`
	Item  *AnnotationItemView `json:"item"`
}

// AnnotationLabelRequest is the body of POST .../items/{item}/labels and
// .../items/{item}/resolve.
type AnnotationLabelRequest struct {
	Label string `json:"label"`
	Note  string `json:"note,omitempty"`
}

// AnnotationLabelResult answers a label or a decision.
type AnnotationLabelResult struct {
	Item     AnnotationItemView `json:"item"`
	Resolved bool               `json:"resolved"`
	// Calibration says where the resolved label went: the calibration
	// item id, or why it could not be written.
	Calibration      string `json:"calibration,omitempty"`
	CalibrationError string `json:"calibration_error,omitempty"`
}

// AnnotationAssignResult answers POST /annotation/queues/{id}/assign.
type AnnotationAssignResult struct {
	Assigned int `json:"assigned"`
}

// AnnotationAgreement answers GET /annotation/queues/{id}/agreement.
type AnnotationAgreement struct {
	Queue     string             `json:"queue"`
	Agreement annotate.Agreement `json:"agreement"`
	// Labellers names the people in the pairs (user id to email).
	Labellers map[string]string `json:"labellers"`
}

// AnnotationPerson is one member who can be named as a labeller.
type AnnotationPerson struct {
	UserID string `json:"user_id"`
	Name   string `json:"name"`
}

// AnnotationPeople answers GET /annotation/people: the organization's
// members by user id and display name (displayActor's rule: never an
// email), sorted by name. Local is true on a server with no members list
// (local mode), where labellers are named by their user id.
type AnnotationPeople struct {
	People []AnnotationPerson `json:"people"`
	Local  bool               `json:"local,omitempty"`
}

type annotationHandler struct {
	env  OrgEnv
	root string
	now  func() time.Time
}

func init() {
	RegisterOrgArea("annotation", func(env OrgEnv, r *router.Router) {
		h := &annotationHandler{env: env, now: time.Now}
		if env.Services.Projects != nil {
			h.root = env.Services.Projects.Root()
		} else if env.Cfg != nil {
			h.root = env.Cfg.Root
		}
		p := APIPrefix + "/annotation/queues"
		r.GET(p, env.R(h.list))
		r.POST(p, env.W(h.create))
		r.GET(p+"/{id}", env.R(h.get))
		r.PUT(p+"/{id}", env.W(h.update))
		r.DELETE(p+"/{id}", env.W(h.remove))
		r.POST(p+"/{id}/items", env.U(h.addItems))
		r.GET(p+"/{id}/items", env.R(h.items))
		r.POST(p+"/{id}/assign", env.W(h.assign))
		r.POST(p+"/{id}/next", env.W(h.next))
		r.POST(p+"/{id}/items/{item}/labels", env.W(h.label))
		r.POST(p+"/{id}/items/{item}/resolve", env.W(h.resolve))
		r.GET(p+"/{id}/agreement", env.R(h.agreement))
		r.GET(APIPrefix+"/annotation/people", env.R(h.people))
	})
}

func (h *annotationHandler) store(ctx *fasthttp.RequestCtx) *annotate.Store {
	if h.root == "" {
		sendUnavailable(ctx, h.env.Mode, "annotation queues")
		return nil
	}
	return annotate.Open(h.root).WithClock(func() time.Time { return h.now().UTC() })
}

func (h *annotationHandler) bad(ctx *fasthttp.RequestCtx, msg string) {
	lib.SendError(ctx, h.env.Mode, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, msg)
}

func (h *annotationHandler) sendErr(ctx *fasthttp.RequestCtx, err error) {
	m := h.env.Mode
	switch {
	case errors.Is(err, annotate.ErrNotFound):
		lib.SendError(ctx, m, fasthttp.StatusNotFound, lib.ErrTypeNotFound, err.Error())
	case errors.Is(err, annotate.ErrInvalid):
		lib.SendError(ctx, m, fasthttp.StatusBadRequest, lib.ErrTypeBadRequest, strings.TrimPrefix(err.Error(), "invalid request: "))
	case errors.Is(err, annotate.ErrConflict):
		lib.SendError(ctx, m, fasthttp.StatusConflict, lib.ErrTypeConflict, strings.TrimPrefix(err.Error(), "conflict: "))
	case errors.Is(err, annotate.ErrForbidden):
		lib.SendError(ctx, m, fasthttp.StatusForbidden, lib.ErrTypeForbidden, strings.TrimPrefix(err.Error(), "forbidden: "))
	default:
		lib.SendError(ctx, m, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
	}
}

// requireSignedIn answers 403 for a caller that is not a signed-in person:
// labels carry the labeller's identity, so API tokens cannot label.
func (h *annotationHandler) requireSignedIn(ctx *fasthttp.RequestCtx, c caller) bool {
	if !c.SignedIn {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden,
			"labels are given by signed-in people in the browser; API tokens can create queues and add items, not label")
		return false
	}
	return true
}

func canManage(c caller, q *annotate.Queue) bool {
	return c.Manager || (c.UserID != "" && q.CreatedBy == c.UserID)
}

// allowedBenchmark reports whether the caller may see the queue's task
// content under the benchmark's access list.
func (h *annotationHandler) allowedBenchmark(c caller, q *annotate.Queue) bool {
	if q.Benchmark == "" || h.root == "" {
		return true
	}
	p, err := datapolicy.Open(h.root).Get(q.Benchmark)
	return err == nil && p.Allows(c.viewer())
}

func (h *annotationHandler) queueView(c caller, f *annotate.File) AnnotationQueueView {
	q := f.Queue
	v := AnnotationQueueView{Queue: q, Counts: annotate.CountsOf(f.Items, q.ReviewersPerItem), CanManage: canManage(c, &q),
		CanLabel: c.SignedIn && q.MayLabel(c.UserID) && !q.Archived}
	now := h.now()
	for i := range f.Items {
		it := &f.Items[i]
		if it.Resolution != nil || slices.ContainsFunc(it.Labels, func(l annotate.Label) bool { return l.UserID == c.UserID }) {
			continue
		}
		mine := slices.ContainsFunc(it.Assigned, func(a annotate.Assignment) bool {
			return a.UserID == c.UserID && (a.Fixed || now.Sub(a.At) < annotate.ClaimTTL)
		})
		if mine || (q.Assignment == annotate.AssignOpen && len(it.Labels) < q.ReviewersPerItem) {
			v.Mine++
		}
	}
	if !v.CanLabel {
		v.Mine = 0
	}
	if !v.CanManage {
		v.Reviewers = nil
	}
	return v
}

// itemView applies blind mode and label order to one item. manage shows
// the queue owner everything but, in a blind queue, the agent.
func itemView(c caller, q *annotate.Queue, it *annotate.Item, manage bool) AnnotationItemView {
	v := AnnotationItemView{ID: it.ID, State: it.State(q.ReviewersPerItem), Task: it.Task, Question: it.Question, Answer: it.Answer, AnswerFrom: it.AnswerFrom,
		RunID: it.RunID, Experiment: it.Experiment, Agent: it.Agent, AgentVersion: it.AgentVersion,
		JudgeLabel: it.JudgeLabel, JudgeVotes: it.JudgeVotes, JudgeReason: it.JudgeReason,
		Labels: []annotate.Label{}, LabelCount: len(it.Labels), Resolution: it.Resolution, Calibration: it.Calibration,
		CalibError: it.CalibrationError, AddedAt: it.AddedAt}
	mine := ""
	for _, l := range it.Labels {
		if l.UserID == c.UserID {
			mine = l.Label
		}
	}
	v.MyLabel = mine
	done := it.Resolution != nil
	if q.Blind && !(manage && done) {
		v.RunID, v.Experiment, v.Agent, v.AgentVersion = "", "", "", ""
		v.Hidden = append(v.Hidden, "agent")
	}
	if manage || mine != "" || done {
		if manage || done {
			v.Labels = append(v.Labels, it.Labels...)
		} else {
			for _, l := range it.Labels {
				if l.UserID == c.UserID {
					v.Labels = append(v.Labels, l)
				}
			}
			v.Hidden = append(v.Hidden, "peer_labels")
		}
	} else {
		v.JudgeLabel, v.JudgeVotes, v.JudgeReason = "", nil, ""
		if it.JudgeLabel != "" {
			v.Hidden = append(v.Hidden, "judge")
		}
		if len(it.Labels) > 0 {
			v.Hidden = append(v.Hidden, "peer_labels")
		}
		v.Resolution = nil
	}
	if manage {
		for _, a := range it.Assigned {
			v.Assigned = append(v.Assigned, a.UserID)
		}
	}
	return v
}

func applyQueueRequest(q *annotate.Queue, r AnnotationQueueRequest) {
	if r.Name != nil {
		q.Name = *r.Name
	}
	if r.Description != nil {
		q.Description = *r.Description
	}
	if r.Instructions != nil {
		q.Instructions = *r.Instructions
	}
	if r.Benchmark != nil {
		q.Benchmark = *r.Benchmark
	}
	if r.Kind != nil {
		q.Kind = *r.Kind
	}
	if r.ReviewersPerItem != nil {
		q.ReviewersPerItem = *r.ReviewersPerItem
	}
	if r.Blind != nil {
		q.Blind = *r.Blind
	}
	if r.Assignment != nil {
		q.Assignment = *r.Assignment
	}
	if r.Reviewers != nil {
		q.Reviewers = *r.Reviewers
	}
	if r.Archived != nil {
		q.Archived = *r.Archived
	}
}

func (h *annotationHandler) list(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	fs, err := st.List()
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	c := callerOf(ctx)
	out := AnnotationQueueList{Queues: []AnnotationQueueView{}}
	for i := range fs {
		out.Queues = append(out.Queues, h.queueView(c, &fs[i]))
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *annotationHandler) checkBenchmark(ctx *fasthttp.RequestCtx, c caller, id string) bool {
	if id == "" {
		return true
	}
	if !validID(id) {
		h.bad(ctx, "benchmark: not a benchmark id")
		return false
	}
	if h.env.Services.Suites != nil {
		work, cancel := workContext(ctx, readTimeout)
		defer cancel()
		if _, err := h.env.Services.Suites.LoadSuite(work, id); err != nil {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusNotFound, lib.ErrTypeNotFound, "benchmark "+id+" not found")
			return false
		}
	}
	q := annotate.Queue{Benchmark: id}
	if !h.allowedBenchmark(c, &q) {
		sendRestricted(ctx, h.env.Mode, id)
		return false
	}
	return true
}

func (h *annotationHandler) create(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	var req AnnotationQueueRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"name","benchmark"?,"kind"?,"reviewers_per_item"?,"blind"?,"assignment"?,"reviewers"?,"description"?,"instructions"?}: `+err.Error())
		return
	}
	c := callerOf(ctx)
	if !h.checkPeople(ctx, req) {
		return
	}
	var q annotate.Queue
	applyQueueRequest(&q, req)
	if !h.checkBenchmark(ctx, c, strings.TrimSpace(q.Benchmark)) {
		return
	}
	q.CreatedBy, q.CreatedByEmail = c.UserID, c.Email
	if q.CreatedBy == "" {
		q.CreatedBy = actorName(ctx)
	}
	made, err := st.Create(q)
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, h.queueView(c, &annotate.File{Queue: *made}))
}

// load reads the queue of {id} and checks benchmark access.
func (h *annotationHandler) load(ctx *fasthttp.RequestCtx, st *annotate.Store) (*annotate.File, caller, bool) {
	id := pathParam(ctx, "id")
	f, err := st.Get(id)
	if err != nil {
		h.sendErr(ctx, err)
		return nil, caller{}, false
	}
	c := callerOf(ctx)
	if !h.allowedBenchmark(c, &f.Queue) {
		sendRestricted(ctx, h.env.Mode, f.Queue.Benchmark)
		return nil, c, false
	}
	return f, c, true
}

func (h *annotationHandler) get(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.queueView(c, f))
}

func (h *annotationHandler) update(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok {
		return
	}
	if !canManage(c, &f.Queue) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only the queue's creator and org admins change its settings")
		return
	}
	var req AnnotationQueueRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, "body: "+err.Error())
		return
	}
	if !h.checkPeople(ctx, req) {
		return
	}
	if req.Benchmark != nil && strings.TrimSpace(*req.Benchmark) != f.Queue.Benchmark && !h.checkBenchmark(ctx, c, strings.TrimSpace(*req.Benchmark)) {
		return
	}
	nf, err := st.Update(f.Queue.ID, func(f *annotate.File, _ time.Time) error {
		q := f.Queue
		applyQueueRequest(&q, req)
		if err := q.Normalize(); err != nil {
			return err
		}
		f.Queue = q
		return nil
	})
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, h.queueView(c, nf))
}

// checkPeople refuses (422) a sent people per item outside 1 to 5 (never
// turned into 1) and, when the server has a members list, a labeller who
// is not a member of the organization by user id (N167). Without a members
// list (local mode, or the lookup failed) labellers stay free user ids.
func (h *annotationHandler) checkPeople(ctx *fasthttp.RequestCtx, req AnnotationQueueRequest) bool {
	if n := req.ReviewersPerItem; n != nil && (*n < 1 || *n > annotate.MaxReviewersPerItm) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "People per item is 1 to 5.")
		return false
	}
	if req.Reviewers == nil || len(*req.Reviewers) == 0 {
		return true
	}
	names := orgMemberNames(ctx)
	if names == nil {
		return true
	}
	for _, id := range *req.Reviewers {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		// The members list is keyed by user id and by email; labellers are
		// matched by user id, so an email is not a labeller.
		if _, ok := names[id]; !ok || strings.Contains(id, "@") {
			lib.SendError(ctx, h.env.Mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
				id+" is not a member of this organization. Pick labellers from the list.")
			return false
		}
	}
	return true
}

// people lists the members who can be named as labellers (N168).
func (h *annotationHandler) people(ctx *fasthttp.RequestCtx) {
	out := AnnotationPeople{People: []AnnotationPerson{}}
	names := orgMemberNames(ctx)
	if names == nil {
		out.Local = true
		lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
		return
	}
	for id := range names {
		if strings.Contains(id, "@") { // the email keys of the same members
			continue
		}
		name, _ := displayActor(ctx, id, names)
		if strings.Contains(name, "@") {
			name, _, _ = strings.Cut(name, "@")
		}
		out.People = append(out.People, AnnotationPerson{UserID: id, Name: name})
	}
	sort.Slice(out.People, func(i, j int) bool {
		a, b := strings.ToLower(out.People[i].Name), strings.ToLower(out.People[j].Name)
		if a != b {
			return a < b
		}
		return out.People[i].UserID < out.People[j].UserID
	})
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *annotationHandler) remove(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok {
		return
	}
	if !canManage(c, &f.Queue) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only the queue's creator and org admins delete it")
		return
	}
	if err := st.Delete(f.Queue.ID); err != nil {
		h.sendErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, map[string]any{"id": f.Queue.ID, "deleted": true})
}

func (h *annotationHandler) addItems(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok {
		return
	}
	if !canManage(c, &f.Queue) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only the queue's creator and org admins add items")
		return
	}
	var req AnnotationItemsRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"experiment"?,"sample"?,"stratify"?,"items"?:[{"task","answer","question"?,"judge_label"?,"run_id"?,"agent"?,"agent_version"?}]}: `+err.Error())
		return
	}
	if req.Experiment == "" && len(req.Items) == 0 {
		h.bad(ctx, "send an experiment or items")
		return
	}
	if req.Sample < 0 || req.Sample > annotate.MaxItems {
		h.bad(ctx, fmt.Sprintf("sample: 0 to %d", annotate.MaxItems))
		return
	}
	out := AnnotationItemsResult{Skipped: []string{}}
	items := append([]annotate.Item(nil), req.Items...)
	if req.Experiment != "" {
		if !validID(req.Experiment) {
			h.bad(ctx, "experiment: not an experiment id")
			return
		}
		more, skipped, err := h.runItems(ctx, &f.Queue, req)
		if err != nil {
			h.sendErr(ctx, err)
			return
		}
		items = append(items, more...)
		out.Skipped = append(out.Skipped, skipped...)
	}
	added, dup, err := st.AddItems(f.Queue.ID, items, c.Email)
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	out.Added, out.Duplicate = added, dup
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, out)
}

// runItems turns the sealed runs of an experiment into items: the answer
// each run sealed, the task's instruction and the judge's verdict.
func (h *annotationHandler) runItems(ctx *fasthttp.RequestCtx, q *annotate.Queue, req AnnotationItemsRequest) ([]annotate.Item, []string, error) {
	ix := h.env.Services.Index
	if ix == nil {
		return nil, nil, fmt.Errorf("%w: the run index is not available on this server", annotate.ErrConflict)
	}
	m, ok := ix.Manifest(req.Experiment)
	if !ok {
		return nil, nil, fmt.Errorf("experiment %s: %w", req.Experiment, annotate.ErrNotFound)
	}
	if q.Benchmark != "" && m.SuiteID != q.Benchmark {
		return nil, nil, fmt.Errorf("%w: experiment %s ran benchmark %s, and this queue labels %s", annotate.ErrInvalid, req.Experiment, m.SuiteID, q.Benchmark)
	}
	if !h.allowedBenchmark(callerOf(ctx), &annotate.Queue{Benchmark: m.SuiteID}) {
		return nil, nil, fmt.Errorf("%w: benchmark %s is restricted", annotate.ErrForbidden, m.SuiteID)
	}
	var s *schemas.Suite
	if h.env.Services.Suites != nil {
		work, cancel := workContext(ctx, readTimeout)
		s, _ = h.env.Services.Suites.LoadSuite(work, m.SuiteID)
		cancel()
	}
	heldOutVisible := h.env.Cfg != nil && h.env.Cfg.HeldOut.Visible
	var items []annotate.Item
	var skipped []string
	instr := map[string]string{}
	for _, rv := range ix.Runs(req.Experiment) {
		run := rv.Run
		if !rv.Sealed || rv.Superseded || run.Practice || !rv.Slot.Counted {
			continue
		}
		task := rv.Slot.Task
		var t *schemas.Task
		if s != nil {
			t = s.TaskByID(task)
		}
		if t != nil && t.Split == schemas.SplitHeldOut && !heldOutVisible {
			skipped = append(skipped, "run "+run.RunID+": task "+task+" is held out")
			continue
		}
		ans, from := runAnswer(ix, &run)
		if from == "" {
			skipped = append(skipped, "run "+run.RunID+": the run saved no answer, changed no files and sent no final message")
			continue
		}
		if len(ans) > 64<<10 || !utf8.ValidString(ans) {
			skipped = append(skipped, "run "+run.RunID+": the answer is not text of at most 64 KiB")
			continue
		}
		it := annotate.Item{Task: task, Answer: ans, AnswerFrom: from, RunID: run.RunID, Experiment: req.Experiment,
			Agent: firstNonEmptyStr(run.Agent.ConfigID, rv.Slot.Arm), AgentVersion: run.Agent.AgentVersion, Ref: "run/" + run.RunID}
		if b, err := ix.RunFile(run.RunID, FileGrade); err == nil {
			var g schemas.Grade
			if json.Unmarshal(b, &g) == nil && g.Judged && !g.InvalidEvaluator {
				it.JudgeLabel = schemas.CalibrationIncorrect
				if g.Passed {
					it.JudgeLabel = schemas.CalibrationCorrect
				}
				if g.Judge != nil {
					it.JudgeVotes = g.Judge.Votes
				}
				if len(g.Reason) <= 2000 {
					it.JudgeReason = g.Reason
				}
			}
		}
		if q.Kind == annotate.KindJudge && it.JudgeLabel == "" {
			skipped = append(skipped, "run "+run.RunID+": no judge verdict")
			continue
		}
		if t != nil {
			if _, ok := instr[task]; !ok {
				instr[task] = readInstruction(t)
			}
			it.Question = instr[task]
		}
		items = append(items, it)
	}
	return sampleItems(items, req.Sample, req.Stratify, req.Experiment), skipped, nil
}

// runAnswer is what a person labels for a run, and where it came from
// (D5): the answer file the run exported, else the files the agent wrote
// (its workspace diff, annotate.AnswerFromDiff), else the agent's final
// message. from is "" when the run has none of the three. None of them
// carries the grade, so a blind queue stays blind.
func runAnswer(ix ReadIndex, run *schemas.Run) (text, from string) {
	if b, err := ix.RunFile(run.RunID, graders.AnswerFile); err == nil && strings.TrimSpace(string(b)) != "" {
		return string(b), annotate.AnswerFromFile
	}
	if b, err := ix.RunFile(run.RunID, FileDiff); err == nil {
		if s := annotate.AnswerFromDiff(b); strings.TrimSpace(s) != "" {
			return s, annotate.AnswerFromFiles
		}
	}
	if run.Claim != nil && strings.TrimSpace(run.Claim.Text) != "" {
		return run.Claim.Text, annotate.AnswerFromFinalMessage
	}
	return "", ""
}

// readInstruction reads a task's instruction, at most 16 KiB.
func readInstruction(t *schemas.Task) string {
	if t.Instruction == "" || t.Dir == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(t.Dir, filepath.Clean("/" + t.Instruction)[1:]))
	if err != nil {
		return ""
	}
	if len(b) > 16<<10 {
		b = b[:16<<10]
	}
	if !utf8.Valid(b) {
		return ""
	}
	return string(b)
}

// sampleItems keeps n items in a fixed pseudo-random order (seeded by the
// experiment id, so the same request picks the same runs); stratify takes
// them in turn from the judge-passed and judge-failed groups.
func sampleItems(items []annotate.Item, n int, stratify bool, seed string) []annotate.Item {
	key := func(it annotate.Item) uint64 {
		h := sha256.Sum256([]byte(seed + "\x00" + it.RunID + "\x00" + it.Task))
		return binary.BigEndian.Uint64(h[:8])
	}
	sort.SliceStable(items, func(i, j int) bool { return key(items[i]) < key(items[j]) })
	if n <= 0 || n >= len(items) {
		return items
	}
	if !stratify {
		return items[:n]
	}
	groups := map[string][]annotate.Item{}
	order := []string{schemas.CalibrationCorrect, schemas.CalibrationIncorrect, ""}
	for _, it := range items {
		groups[it.JudgeLabel] = append(groups[it.JudgeLabel], it)
	}
	var out []annotate.Item
	for len(out) < n {
		took := false
		for _, g := range order {
			if len(groups[g]) > 0 && len(out) < n {
				out = append(out, groups[g][0])
				groups[g] = groups[g][1:]
				took = true
			}
		}
		if !took {
			break
		}
	}
	return out
}

func (h *annotationHandler) items(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok {
		return
	}
	state := string(ctx.QueryArgs().Peek("state"))
	offset, _ := strconv.Atoi(string(ctx.QueryArgs().Peek("offset")))
	limit, _ := strconv.Atoi(string(ctx.QueryArgs().Peek("limit")))
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	manage := canManage(c, &f.Queue)
	out := AnnotationItemList{Queue: h.queueView(c, f), Items: []AnnotationItemView{}}
	n := 0
	for i := range f.Items {
		it := &f.Items[i]
		if state != "" && it.State(f.Queue.ReviewersPerItem) != state {
			continue
		}
		if n >= offset && len(out.Items) < limit {
			out.Items = append(out.Items, itemView(c, &f.Queue, it, manage))
		}
		n++
	}
	out.Total = n
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *annotationHandler) assign(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok {
		return
	}
	if !canManage(c, &f.Queue) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only the queue's creator and org admins assign items")
		return
	}
	n, err := st.Assign(f.Queue.ID)
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, AnnotationAssignResult{Assigned: n})
}

func (h *annotationHandler) next(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok || !h.requireSignedIn(ctx, c) {
		return
	}
	it, q, err := st.Next(f.Queue.ID, c.UserID)
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	nf, _ := st.Get(f.Queue.ID)
	if nf == nil {
		nf = f
	}
	out := AnnotationNext{Queue: h.queueView(c, nf)}
	if it != nil {
		v := itemView(c, q, it, false)
		out.Item = &v
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

func (h *annotationHandler) label(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok || !h.requireSignedIn(ctx, c) {
		return
	}
	var req AnnotationLabelRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"label":"correct"|"incorrect","note"?}: `+err.Error())
		return
	}
	res, err := st.AddLabel(f.Queue.ID, pathParam(ctx, "item"), annotate.Label{UserID: c.UserID, Email: c.Email, Label: req.Label, Note: req.Note})
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	out := AnnotationLabelResult{Resolved: res.Resolved}
	if res.Resolved {
		out.Calibration, out.CalibrationError = h.calibrate(ctx, st, &res.Queue, &res.Item)
		res.Item.Calibration, res.Item.CalibrationError = out.Calibration, out.CalibrationError
	}
	out.Item = itemView(c, &res.Queue, &res.Item, false)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusCreated, out)
}

func (h *annotationHandler) resolve(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok || !h.requireSignedIn(ctx, c) {
		return
	}
	if !canManage(c, &f.Queue) {
		lib.SendError(ctx, h.env.Mode, fasthttp.StatusForbidden, lib.ErrTypeForbidden, "only the queue's creator and org admins decide disagreements")
		return
	}
	var req AnnotationLabelRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"label":"correct"|"incorrect","note"?}: `+err.Error())
		return
	}
	it, q, err := st.Resolve(f.Queue.ID, pathParam(ctx, "item"), annotate.Resolution{Label: req.Label, Note: req.Note, UserID: c.UserID, Email: c.Email})
	if err != nil {
		h.sendErr(ctx, err)
		return
	}
	out := AnnotationLabelResult{Resolved: true}
	out.Calibration, out.CalibrationError = h.calibrate(ctx, st, q, it)
	it.Calibration, it.CalibrationError = out.Calibration, out.CalibrationError
	out.Item = itemView(c, q, it, true)
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}

// calibrate appends a resolved item to the queue's benchmark calibration
// labels and records the outcome on the item.
func (h *annotationHandler) calibrate(ctx *fasthttp.RequestCtx, st *annotate.Store, q *annotate.Queue, it *annotate.Item) (string, string) {
	if q.Benchmark == "" {
		return "", ""
	}
	calID, msg := h.writeCalibration(ctx, q, it)
	_ = st.SetCalibration(q.ID, it.ID, calID, msg)
	return calID, msg
}

func (h *annotationHandler) writeCalibration(ctx *fasthttp.RequestCtx, q *annotate.Queue, it *annotate.Item) (string, string) {
	if isBuiltinSuite(h.env.Services.Suites, q.Benchmark) {
		return "", "benchmark " + q.Benchmark + " is a built-in benchmark; its calibration labels cannot change"
	}
	ps := h.env.Services.Projects
	if ps == nil {
		return "", "no project folder on this server"
	}
	dir, err := ps.Resolve(projectfs.AreaBenchmarks, q.Benchmark)
	if err != nil {
		return "", "benchmark " + q.Benchmark + ": " + err.Error()
	}
	if _, err := os.Stat(filepath.Join(dir, suite.SuiteFile)); err != nil {
		return "", "benchmark " + q.Benchmark + " no longer exists"
	}
	if h.env.Services.Suites != nil {
		work, cancel := workContext(ctx, readTimeout)
		s, err := h.env.Services.Suites.LoadSuite(work, q.Benchmark)
		cancel()
		if err == nil && s.TaskByID(it.Task) == nil {
			return "", "task " + it.Task + " is not in benchmark " + q.Benchmark
		}
	}
	r := it.Resolution
	labeller := r.Email
	note := r.Note
	if r.Method == "consensus" {
		var who []string
		for _, l := range it.Labels {
			who = append(who, firstNonEmptyStr(l.Email, l.UserID))
		}
		labeller = strings.Join(who, ", ")
		note = "agreed by " + strconv.Itoa(len(it.Labels)) + " in annotation queue " + q.Name
	} else {
		note = strings.TrimSpace("decided in annotation queue " + q.Name + ". " + note)
	}
	if utf8.RuneCountInString(labeller) > 200 {
		labeller = string([]rune(labeller)[:199]) + "…"
	}
	ci, err := calibration.NewItem(schemas.CalibrationLabelRequest{Task: it.Task, Answer: it.Answer, Label: r.Label,
		Labeller: firstNonEmptyStr(labeller, r.UserID, "queue"), Note: note, Ref: "queue/" + q.ID + "/" + it.ID},
		schemas.CalibrationSourceReview, r.At)
	if err != nil {
		return "", err.Error()
	}
	if _, err := calibration.Append(dir, []schemas.CalibrationItem{ci}); err != nil {
		return "", err.Error()
	}
	return ci.ID, ""
}

func (h *annotationHandler) agreement(ctx *fasthttp.RequestCtx) {
	st := h.store(ctx)
	if st == nil {
		return
	}
	f, c, ok := h.load(ctx, st)
	if !ok {
		return
	}
	out := AnnotationAgreement{Queue: f.Queue.ID, Labellers: map[string]string{}}
	manage := canManage(c, &f.Queue)
	items := f.Items
	if !manage {
		// Agreement over items still open would reveal how peers labelled.
		items = slices.DeleteFunc(slices.Clone(items), func(it annotate.Item) bool {
			return it.Resolution == nil && it.State(f.Queue.ReviewersPerItem) != annotate.StateDisputed
		})
	}
	out.Agreement = annotate.ComputeAgreement(items, f.Queue.ReviewersPerItem)
	for _, it := range items {
		for _, l := range it.Labels {
			out.Labellers[l.UserID] = firstNonEmptyStr(l.Email, l.UserID)
		}
	}
	lib.SendJSON(ctx, h.env.Mode, fasthttp.StatusOK, out)
}
