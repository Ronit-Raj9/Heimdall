package handlers

// Policy document to compliance benchmark (lane Z23). A policy or SOP
// (PDF, DOCX, Markdown or text) uploaded to a support benchmark is split
// into numbered clauses; the policy.generate job drafts three scenarios per
// clause into the document's staging folder; these routes list, edit and
// review the drafts (accepted ones become tasks, rejected ones are
// dropped) and build the per-clause compliance report from the
// evaluations of the accepted tasks, as JSON, Markdown or PDF.

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/keystore"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/policybench"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// PolicyDocUpload is the body of POST /suites/{id}/policy-docs.
type PolicyDocUpload struct {
	Name string `json:"name"` // the file name; its extension picks the format
	// ContentBase64 is the file (PDF, DOCX, Markdown or text); Text is
	// pasted text instead.
	ContentBase64 string `json:"content_base64,omitempty"`
	Text          string `json:"text,omitempty"`
	Format        string `json:"format,omitempty"` // markdown, text, pdf, docx; empty: from the name
	DryRun        bool   `json:"dry_run,omitempty"`
}

// PolicyDocSummary is one document in the list. A viewer gets only the
// id, title and upload time (Limited on the list): enough to open the
// compliance report, nothing of the clauses or drafts.
type PolicyDocSummary struct {
	ID         string         `json:"id"`
	Name       string         `json:"name,omitempty"`
	Title      string         `json:"title"`
	Format     string         `json:"format,omitempty"`
	UploadedAt string         `json:"uploaded_at"`
	Clauses    int            `json:"clauses,omitempty"`
	Drafts     map[string]int `json:"drafts,omitempty"`
}

// PolicyDocList answers GET /suites/{id}/policy-docs.
type PolicyDocList struct {
	Suite string             `json:"suite"`
	Docs  []PolicyDocSummary `json:"docs"`
	// Limited is set for a viewer: each document lists its id, title and
	// upload time only.
	Limited bool `json:"limited,omitempty"`
	// Shape is the support task policy drafts copy; empty when the
	// benchmark has none, and Next then says why policy tasks cannot be
	// drafted here.
	Shape string `json:"shape,omitempty"`
	Next  string `json:"next,omitempty"`
}

// PolicyDocView answers GET /suites/{id}/policy-docs/{doc} and the upload.
type PolicyDocView struct {
	Suite  string             `json:"suite"`
	DryRun bool               `json:"dry_run,omitempty"`
	Doc    *policybench.Doc   `json:"doc"`
	Counts map[string]int     `json:"counts"`
	Shape  string             `json:"shape,omitempty"`
	Tools  []policybench.Tool `json:"tools,omitempty"`
	Grader string             `json:"grader"`
	Judged bool               `json:"judged"`
	Next   string             `json:"next"`
	// UploadedByName is the display name of the person who uploaded the
	// document; the raw email in doc.uploaded_by is left out for a caller
	// who is not an org admin (fix wave 3, N33).
	UploadedByName string `json:"uploaded_by_name,omitempty"`
}

// PolicyClausesRequest is the body of PUT /suites/{id}/policy-docs/{doc}/clauses.
type PolicyClausesRequest struct {
	Clauses []policybench.Clause `json:"clauses"`
}

// PolicyReviewRequest is the body of POST /suites/{id}/policy-docs/{doc}/review.
type PolicyReviewRequest struct {
	Accept []string `json:"accept,omitempty"`
	Reject []string `json:"reject,omitempty"`
	// SwitchGrader moves the benchmark to grader checks with the pinned
	// reply judge when it uses another grader (policy tasks are graded by
	// their checks.yaml, whose clause rubric needs the judge).
	SwitchGrader bool                  `json:"switch_grader,omitempty"`
	Judge        *scaffold.JudgeConfig `json:"judge,omitempty"`
}

// PolicyReviewResult answers the review.
type PolicyReviewResult struct {
	Suite         string         `json:"suite"`
	Doc           string         `json:"doc"`
	Accepted      []string       `json:"accepted"`
	Rejected      []string       `json:"rejected"`
	GraderChanged bool           `json:"grader_changed"`
	Counts        map[string]int `json:"counts"`
	Next          string         `json:"next"`
}

// PolicyDraftFiles answers GET /suites/{id}/policy-docs/{doc}/drafts/{task}.
type PolicyDraftFiles struct {
	Task  string             `json:"task"`
	Draft *policybench.Draft `json:"draft"`
	Files []PolicyDraftFile  `json:"files"`
}

// PolicyDraftFile is one text file of a draft.
type PolicyDraftFile struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

const maxPolicyReportRuns = 20000

func init() {
	RegisterOrgArea("policydocs", func(env OrgEnv, r *router.Router) {
		h := &policyDocs{suiteBuild: &suiteBuild{&suiteAuthoring{mode: env.Mode, projects: env.Services.Projects, suites: env.Services.Suites}}, index: env.Services.Index}
		h.keys, h.orgID, h.hosted = env.Services.Keys, env.Services.OrgID, env.Services.Hosted
		base := APIPrefix + "/suites/{id}/policy-docs"
		r.GET(base, env.R(h.list))
		r.POST(base, env.U(refuseBuiltin(env.Mode, h.suites, h.upload)))
		r.GET(base+"/{doc}", env.R(h.get))
		r.DELETE(base+"/{doc}", env.W(refuseBuiltin(env.Mode, h.suites, h.remove)))
		r.PUT(base+"/{doc}/clauses", env.W(refuseBuiltin(env.Mode, h.suites, h.setClauses)))
		r.GET(base+"/{doc}/drafts/{task}", env.R(h.draft))
		r.POST(base+"/{doc}/review", env.W(refuseBuiltin(env.Mode, h.suites, h.review)))
		r.GET(base+"/{doc}/report", env.R(h.report))
	})
}

type policyDocs struct {
	*suiteBuild
	index ReadIndex
	// keys, orgID and hosted let review check the reply judge's model key
	// before it switches the benchmark to judged checks (fix wave 3, N276).
	keys   keystore.Store
	orgID  string
	hosted bool
}

func (h *policyDocs) docParam(ctx *fasthttp.RequestCtx) (string, string, *policybench.Doc, bool) {
	if !h.needProjects(ctx) {
		return "", "", nil, false
	}
	id, dir, ok := h.suiteDirParam(ctx)
	if !ok {
		return "", "", nil, false
	}
	docID := pathParam(ctx, "doc")
	if !policybench.ValidDocID(docID) {
		sendNotFound(ctx, h.mode, "policy document", docID)
		return "", "", nil, false
	}
	d, err := policybench.Load(dir, docID)
	if errors.Is(err, os.ErrNotExist) {
		sendNotFound(ctx, h.mode, "policy document", docID)
		return "", "", nil, false
	}
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusInternalServerError, lib.ErrTypeInternal, err.Error())
		return "", "", nil, false
	}
	return id, dir, d, true
}

// namedPolicyDoc is the document as a caller sees it (fix wave 3, N33): a
// copy whose uploader, deciders and generation actors are left out when
// they are emails the caller may not see (a decider then reads as the
// name). nil names (local mode) returns d itself.
func namedPolicyDoc(names *actorNames, d *policybench.Doc) *policybench.Doc {
	if names == nil || d == nil {
		return d
	}
	c := *d
	c.UploadedBy, _ = names.show(d.UploadedBy)
	if len(d.Drafts) > 0 {
		c.Drafts = make([]policybench.Draft, len(d.Drafts))
		for i, dr := range d.Drafts {
			if keep, name := names.show(dr.DecidedBy); keep == "" && dr.DecidedBy != "" {
				dr.DecidedBy = name
			}
			c.Drafts[i] = dr
		}
	}
	if len(d.Generations) > 0 {
		c.Generations = make([]policybench.Generation, len(d.Generations))
		for i, g := range d.Generations {
			if keep, name := names.show(g.By); keep == "" && g.By != "" {
				g.By = name
			}
			c.Generations[i] = g
		}
	}
	return &c
}

// gradingOf reads the benchmark's grader and whether its reply judge is pinned.
func gradingOf(dir string) (string, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, suite.SuiteFile))
	if err != nil {
		return "", false
	}
	var s scenarioSuite
	if yaml.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s.Grader, s.Grading == "judged" && s.Judge != nil && s.Judge.Provider != "" && s.Judge.Model != ""
}

func (h *policyDocs) view(ctx *fasthttp.RequestCtx, id, dir string, d *policybench.Doc) *PolicyDocView {
	names := callerNames(ctx)
	v := &PolicyDocView{Suite: id, Doc: namedPolicyDoc(names, d), Counts: d.Counts()}
	_, v.UploadedByName = names.show(d.UploadedBy)
	v.Grader, v.Judged = gradingOf(dir)
	if sh, _, err := policybench.SuiteShape(dir, ""); err == nil {
		v.Shape = sh.From
		if tools, err := policybench.Tools(dir, sh.Desk); err == nil {
			v.Tools = tools
		}
	}
	switch {
	case v.Shape == "":
		v.Next = policybench.ErrNoShape.Error()
	case v.Counts[policybench.StatusPending] > 0:
		v.Next = "Review the drafted tasks: accept the good ones (they join the benchmark) and reject the rest."
	case v.Counts[policybench.StatusAccepted] > 0:
		v.Next = "Run the readiness check on the accepted tasks, then an evaluation of your agent; the report fills in from its runs."
	default:
		v.Next = "Check the clauses (fix the split, untick the ones with nothing to test), then draft the tasks with a pinned model and a budget."
	}
	return v
}

func (h *policyDocs) list(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	p := auth.PrincipalFrom(ctx)
	limited := p != nil && p.Role != "" && !p.Role.AtLeast(auth.RoleMember)
	id, dir, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	docs, err := policybench.List(dir)
	if err != nil {
		h.sendStoreError(ctx, "list policy documents", err)
		return
	}
	out := PolicyDocList{Suite: id, Docs: []PolicyDocSummary{}, Limited: limited}
	if sh, _, err := policybench.SuiteShape(dir, ""); err == nil {
		out.Shape = sh.From
	} else if errors.Is(err, policybench.ErrNoShape) {
		out.Next = err.Error()
	}
	for _, d := range docs {
		if limited {
			out.Docs = append(out.Docs, PolicyDocSummary{ID: d.ID, Title: d.Title, UploadedAt: d.UploadedAt})
			continue
		}
		out.Docs = append(out.Docs, PolicyDocSummary{ID: d.ID, Name: d.Name, Title: d.Title, Format: d.Format, UploadedAt: d.UploadedAt,
			Clauses: len(d.Clauses), Drafts: d.Counts()})
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

func (h *policyDocs) upload(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	id, dir, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	var req PolicyDocUpload
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"name","content_base64"|"text","format"?,"dry_run"?}: `+err.Error())
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 200 || strings.ContainsAny(name, "/\\\n\r") {
		h.bad(ctx, "name: the file name, one line of at most 200 characters")
		return
	}
	var data []byte
	switch {
	case req.ContentBase64 != "" && req.Text != "":
		h.bad(ctx, "send content_base64 or text, not both")
		return
	case req.ContentBase64 != "":
		b, err := base64.StdEncoding.DecodeString(req.ContentBase64)
		if err != nil {
			h.bad(ctx, "content_base64 is not base64: "+err.Error())
			return
		}
		data = b
	case req.Text != "":
		data = []byte(req.Text)
	default:
		h.bad(ctx, "the document is empty: send content_base64 (the file) or text")
		return
	}
	if len(data) > policybench.MaxSourceBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeTooLarge, fmt.Sprintf("the document is over %d MiB", policybench.MaxSourceBytes>>20))
		return
	}
	format := req.Format
	switch format {
	case "":
		format = policybench.FormatOf(name, data)
	case policybench.FormatMarkdown, policybench.FormatText, policybench.FormatPDF, policybench.FormatDOCX:
	default:
		h.bad(ctx, "format: markdown, text, pdf or docx")
		return
	}
	text, err := policybench.Extract(ctx, format, data)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	d, err := policybench.NewDoc(name, format, data, text, actorName(ctx), time.Now())
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	if req.DryRun {
		v := h.view(ctx, id, dir, d)
		v.DryRun = true
		v.Next = "Check the clauses, then upload."
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, v)
		return
	}
	if old, err := policybench.Load(dir, d.ID); err == nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
			fmt.Sprintf("this document is already uploaded as %s (%s); open it, or delete it first", old.ID, old.Name))
		return
	}
	if err := policybench.Save(dir, d, text); err != nil {
		h.sendStoreError(ctx, "save the policy document", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, h.view(ctx, id, dir, d))
}

func (h *policyDocs) get(ctx *fasthttp.RequestCtx) {
	if h.refuseViewer(ctx) {
		return
	}
	id, dir, d, ok := h.docParam(ctx)
	if !ok {
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, h.view(ctx, id, dir, d))
}

func (h *policyDocs) remove(ctx *fasthttp.RequestCtx) {
	_, dir, d, ok := h.docParam(ctx)
	if !ok {
		return
	}
	if err := os.RemoveAll(policybench.Dir(dir, d.ID)); err != nil {
		h.sendStoreError(ctx, "delete the policy document", err)
		return
	}
	ctx.SetStatusCode(fasthttp.StatusNoContent)
}

func (h *policyDocs) setClauses(ctx *fasthttp.RequestCtx) {
	id, dir, d, ok := h.docParam(ctx)
	if !ok {
		return
	}
	var req PolicyClausesRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"clauses":[{"number","title"?,"section"?,"text","skip"?}]}: `+err.Error())
		return
	}
	if err := d.SetClauses(req.Clauses); err != nil {
		h.bad(ctx, err.Error())
		return
	}
	if err := policybench.Save(dir, d, ""); err != nil {
		h.sendStoreError(ctx, "save the clauses", err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, h.view(ctx, id, dir, d))
}

func (h *policyDocs) draft(ctx *fasthttp.RequestCtx) {
	if h.refuseViewer(ctx) {
		return
	}
	_, dir, d, ok := h.docParam(ctx)
	if !ok {
		return
	}
	task := pathParam(ctx, "task")
	x := d.DraftByTask(task)
	if x == nil {
		sendNotFound(ctx, h.mode, "draft", task)
		return
	}
	root := policybench.DraftDir(dir, d.ID, task)
	if x.Status == policybench.StatusAccepted {
		root = filepath.Join(dir, "tasks", task)
	}
	files, err := scaffold.ReadTaskFolder(root)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusGone, lib.ErrTypeNotFound, "the draft's files are gone (rejected or deleted)")
		return
	}
	out := PolicyDraftFiles{Task: task, Draft: x, Files: []PolicyDraftFile{}}
	for _, f := range files {
		if len(f.Data) > 256<<10 || strings.HasPrefix(f.Path, "repo/") {
			continue
		}
		out.Files = append(out.Files, PolicyDraftFile{Path: f.Path, Text: string(f.Data)})
	}
	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

func (h *policyDocs) review(ctx *fasthttp.RequestCtx) {
	id, dir, d, ok := h.docParam(ctx)
	if !ok {
		return
	}
	var req PolicyReviewRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"accept"?:[task],"reject"?:[task],"switch_grader"?,"judge"?}: `+err.Error())
		return
	}
	if len(req.Accept)+len(req.Reject) == 0 {
		h.bad(ctx, "nothing to review: list tasks under accept or reject")
		return
	}
	for _, t := range append(append([]string(nil), req.Accept...), req.Reject...) {
		if !scaffold.ValidID(t) {
			h.bad(ctx, fmt.Sprintf("%q is not a task id", t))
			return
		}
	}
	out := PolicyReviewResult{Suite: id, Doc: d.ID, Accepted: []string{}, Rejected: []string{}}
	now := time.Now()
	if len(req.Reject) > 0 {
		done, err := policybench.Reject(dir, d, req.Reject, actorName(ctx), now)
		out.Rejected = append(out.Rejected, done...)
		if err != nil {
			h.bad(ctx, err.Error())
			return
		}
	}
	if len(req.Accept) > 0 {
		grader, judged := gradingOf(dir)
		var edit func([]byte) ([]byte, error)
		if grader != scaffold.ChecksGrader || !judged || (req.SwitchGrader && req.Judge != nil) {
			if !req.SwitchGrader || (!judged && req.Judge == nil) {
				lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict,
					"Pick a reply judge before accepting: policy tasks are graded by a judge model, so accepting switches this benchmark to judged checks.")
				return
			}
			if req.Judge != nil && !h.judgeKeyOK(ctx, req.Judge.Provider) {
				return
			}
			judge := req.Judge
			edit = func(raw []byte) ([]byte, error) { return scaffold.SetChecksGrader(raw, judge) }
			out.GraderChanged = true
		}
		var writeErr error
		done, err := policybench.Accept(dir, d, req.Accept, actorName(ctx), now, edit, func(old, next []byte, note string) error {
			_, err := h.projects.Write(ctx, projectfs.AreaBenchmarks, id+"/"+suite.SuiteFile, next,
				projectfs.WriteOpts{Actor: actorName(ctx), Note: note, IfMatch: shaOf(old)})
			if err == nil {
				_ = os.Chmod(filepath.Join(dir, suite.SuiteFile), 0o644)
			}
			writeErr = err
			return err
		})
		if err != nil {
			if writeErr != nil {
				h.sendStoreError(ctx, "add the tasks to suite.yaml", writeErr)
				return
			}
			h.bad(ctx, err.Error())
			return
		}
		out.Accepted = done
	}
	out.Counts = d.Counts()
	switch {
	case len(out.Accepted) > 0:
		out.Next = "Run the readiness check: each accepted task's compliant reference must pass every check and its violating control must fail before evaluations use it."
		if out.GraderChanged {
			out.Next += " The benchmark now uses grader checks, so its earlier tasks need their readiness checks again too."
		}
	default:
		out.Next = "Rejected drafts are dropped; draft their clauses again if you want new ones."
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// judgeKeyOK checks the organization has a working model key for the reply
// judge's provider before an accept pins it (fix wave 3, N276). A missing
// key answers 422 in plain words; an over-budget key or an unreadable key
// store answers as every other key check does. It reports false when it
// answered.
func (h *policyDocs) judgeKeyOK(ctx *fasthttp.RequestCtx, provider string) bool {
	var keys KeySource
	if h.keys != nil {
		keys = h.keys
	}
	err := ModelKeyRefusal(ctx, keys, quickOrgID(ctx, h.orgID), []string{provider}, nil, h.hosted)
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrModelKeyMissing):
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			"No working "+provider+" key in this organization. Add one under Models, then accept again.")
	case !SendModelKeyRefusal(ctx, h.mode, err):
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
	}
	return false
}

// policyRuns reads the counted, scored runs of the accepted tasks.
func (h *policyDocs) policyRuns(suiteID string, d *policybench.Doc, exp string) []policybench.RunRecord {
	if h.index == nil {
		return nil
	}
	tasks := d.TaskClause()
	if len(tasks) == 0 {
		return nil
	}
	exps := h.index.Experiments()
	if exp != "" {
		exps = []string{exp}
	}
	var out []policybench.RunRecord
	reasons := 0
	for _, e := range exps {
		for _, rv := range h.index.Runs(e) {
			if rv.Slot.Suite != suiteID || !rv.Slot.Counted || rv.Superseded || !rv.Sealed {
				continue
			}
			if _, ok := tasks[rv.Slot.Task]; !ok {
				continue
			}
			y, scored := rv.Run.Outcome.Score()
			if !scored {
				continue
			}
			rec := policybench.RunRecord{RunID: rv.Run.RunID, ExpID: rv.Run.ExpID, Task: rv.Slot.Task, Agent: agentLabel(rv), Passed: y == 1}
			if !rv.Run.Timing.Started.IsZero() {
				rec.At = rv.Run.Timing.Started.UTC().Format(time.RFC3339)
			}
			if !rec.Passed && reasons < 300 {
				reasons++
				if raw, err := h.index.RunFile(rv.Run.RunID, FileGrade); err == nil {
					var g schemas.Grade
					if json.Unmarshal(raw, &g) == nil {
						rec.Reason = g.Reason
					}
				}
				if rec.Reason == "" {
					rec.Reason = string(rv.Run.Outcome)
				}
			}
			out = append(out, rec)
			if len(out) >= maxPolicyReportRuns {
				return out
			}
		}
	}
	return out
}

func agentLabel(rv RunView) string {
	a := rv.Run.Agent.ConfigID
	if a == "" {
		a = rv.Slot.Arm
	}
	if v := rv.Run.Agent.AgentVersion; v != "" {
		a += " " + v
	}
	return a
}

func (h *policyDocs) report(ctx *fasthttp.RequestCtx) {
	id, _, d, ok := h.docParam(ctx)
	if !ok {
		return
	}
	exp := string(ctx.QueryArgs().Peek("exp"))
	if exp != "" && !validID(exp) {
		h.bad(ctx, "exp: not an evaluation id")
		return
	}
	rep := policybench.BuildReport(id, d, h.policyRuns(id, d, exp), time.Now())
	name := d.ID + "-compliance"
	switch format := string(ctx.QueryArgs().Peek("format")); format {
	case "", "json":
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, rep)
	case "markdown", "md":
		ctx.SetContentType("text/markdown; charset=utf-8")
		ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`.md"`)
		ctx.SetBodyString(rep.Markdown())
	case "pdf":
		ctx.SetContentType("application/pdf")
		ctx.Response.Header.Set("Content-Disposition", `attachment; filename="`+name+`.pdf"`)
		ctx.SetBody(rep.PDF())
	default:
		h.bad(ctx, "format: json, markdown or pdf")
	}
}
