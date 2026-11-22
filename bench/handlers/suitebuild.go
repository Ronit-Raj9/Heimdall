package handlers

// Own benchmarks from the website (lane X13): add tasks to a
// question-and-answer suite from a spreadsheet (CSV), JSON Lines or support
// tickets, choose the suite's grader and pin its judge, and mark a task held
// out or public. Each writes through the org's confined project store, as
// the suite authoring area does; admission (readiness checks) stays the
// suite.admit job.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// maxRowImportBytes caps the data of one rows import.
const maxRowImportBytes = 8 << 20

// RowImportRequest is the body of POST /suites/{id}/rows.
type RowImportRequest struct {
	// Format is csv, jsonl or tickets (a JSON array or object, JSON Lines, or
	// plain text tickets split by --- lines).
	Format string `json:"format"`
	Data   string `json:"data"`
	// Match is the answer_match mode of rows that name none: exact (default),
	// contains_all, regex, numeric or choice.
	Match string `json:"match,omitempty"`
	// HeldOut marks every imported task held out (private split).
	HeldOut bool `json:"held_out,omitempty"`
	// DryRun parses and checks the rows and writes nothing.
	DryRun bool `json:"dry_run,omitempty"`
	// Source names where the rows came from, for provenance (a file name).
	Source string `json:"source,omitempty"`

	// Lane Y13 row mapper. Mapping picks the column of each field (dotted
	// paths into nested records); empty fields are detected. With no
	// mapping the one saved by an earlier import (import-map.yaml) is used.
	Mapping *scaffold.RowMapping `json:"mapping,omitempty"`
	// SaveMapping keeps Mapping in benchmarks/<id>/import-map.yaml so the
	// next import of the same export reuses it.
	SaveMapping bool `json:"save_mapping,omitempty"`
	// GradeOn is how conversation rows are graded when a row names none:
	// final (the agent's last reply, default) or transcript (the whole
	// conversation).
	GradeOn string `json:"grade_on,omitempty"`
	// Attachments are files uploaded with the rows, by file name; a row
	// names them in its attachments column. Each at most 5 MiB.
	Attachments map[string]RowAttachmentUpload `json:"attachments,omitempty"`
	// Async imports in the background and answers 202 with the import's
	// progress (GET /suites/{id}/rows/imports/{import}). Imports of more
	// than 500 rows are always async.
	Async bool `json:"async,omitempty"`
}

// RowAttachmentUpload is one uploaded file.
type RowAttachmentUpload struct {
	ContentBase64 string `json:"content_base64"`
	MediaType     string `json:"media_type,omitempty"`
}

// RowImportResult answers POST /suites/{id}/rows.
type RowImportResult struct {
	Suite    string                `json:"suite"`
	Grader   string                `json:"grader"`
	DryRun   bool                  `json:"dry_run"`
	Tasks    []scaffold.RowTask    `json:"tasks"`
	Problems []scaffold.RowProblem `json:"problems"`
	Next     string                `json:"next"`

	// Rows is how many records the data holds; TasksTotal how many can
	// become tasks (Tasks lists at most the first 500 of a preview).
	Rows       int `json:"rows"`
	TasksTotal int `json:"tasks_total"`
	// Columns are the column paths the data holds, for the mapping step;
	// Mapping is the mapping the import used (detected fields filled in).
	Columns      []scaffold.RowColumn `json:"columns"`
	Mapping      scaffold.RowMapping  `json:"mapping"`
	MappingSaved bool                 `json:"mapping_saved,omitempty"`
	// Import is the background import, for an async import.
	Import *RowImportStatus `json:"import,omitempty"`
}

// SuiteGraderRequest is the body of PUT /suites/{id}/grader.
type SuiteGraderRequest struct {
	// Grader is answer_match (exact answer, no model) or judge (rubric judge).
	Grader string                `json:"grader"`
	Judge  *scaffold.JudgeConfig `json:"judge,omitempty"`
}

// SuiteGraderResult answers PUT /suites/{id}/grader.
type SuiteGraderResult struct {
	Suite   string                `json:"suite"`
	Grader  string                `json:"grader"`
	Grading string                `json:"grading"`
	Judge   *scaffold.JudgeConfig `json:"judge,omitempty"`
	Next    string                `json:"next"`
}

// TaskSplitRequest is the body of PUT /suites/{id}/tasks/{task}/split.
type TaskSplitRequest struct {
	HeldOut bool `json:"held_out"`
}

// TaskSplitResult answers PUT /suites/{id}/tasks/{task}/split.
type TaskSplitResult struct {
	Suite string `json:"suite"`
	Task  string `json:"task"`
	Split string `json:"split"`
}

type suiteBuild struct{ *suiteAuthoring }

func init() {
	RegisterOrgArea("suitebuild", func(env OrgEnv, r *router.Router) {
		h := &suiteBuild{&suiteAuthoring{mode: env.Mode, projects: env.Services.Projects, suites: env.Services.Suites}}
		r.POST(APIPrefix+"/suites/{id}/rows", env.U(refuseBuiltin(env.Mode, h.suites, h.importRows)))
		r.GET(APIPrefix+"/suites/{id}/rows/imports/{import}", env.R(refuseBuiltin(env.Mode, h.suites, h.importStatus)))
		r.PUT(APIPrefix+"/suites/{id}/grader", env.W(refuseBuiltin(env.Mode, h.suites, h.setGrader)))
		r.PUT(APIPrefix+"/suites/{id}/tasks/{task}/split", env.W(refuseBuiltin(env.Mode, h.suites, h.setSplit)))
	})
}

// answerSuite is what the area reads from suite.yaml.
type answerSuite struct {
	Grader  string   `yaml:"grader"`
	Grading string   `yaml:"grading"`
	Tasks   []string `yaml:"tasks"`
	Output  struct {
		Kind string `yaml:"kind"`
	} `yaml:"output"`
	Judge *scaffold.JudgeConfig `yaml:"judge"`
}

// readAnswerSuite reads suite.yaml and refuses a suite that is not graded on
// an answer (a code, state or artifact suite keeps its tests).
func (h *suiteBuild) readAnswerSuite(ctx *fasthttp.RequestCtx, id, dir string) ([]byte, *answerSuite, bool) {
	raw, err := os.ReadFile(filepath.Join(dir, suite.SuiteFile))
	if err != nil {
		h.sendStoreError(ctx, "read suite.yaml", err)
		return nil, nil, false
	}
	var s answerSuite
	if err := yaml.Unmarshal(raw, &s); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "suite.yaml: "+err.Error()+"; fix it on the Edit tab")
		return nil, nil, false
	}
	if s.Output.Kind != "answer" {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			"benchmark "+id+" grades code or workspace state with its own tests ("+s.Grader+"); questions, spreadsheets and tickets go in a question-and-answer benchmark: create one from Benchmarks > New")
		return nil, nil, false
	}
	return raw, &s, true
}

// importRows handles POST /suites/{id}/rows.
func (h *suiteBuild) importRows(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	id, dir, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	var req RowImportRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"format","data","match"?,"held_out"?,"dry_run"?,"source"?,"mapping"?,"save_mapping"?,"grade_on"?,"attachments"?,"async"?}: `+err.Error())
		return
	}
	limit := maxRowImportBytes
	if req.Async || len(req.Data) > maxRowImportBytes {
		limit = maxAsyncRowImportBytes
	}
	if len(req.Data) > limit {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeBadRequest,
			fmt.Sprintf("the data is over %d MiB; split it into smaller files", limit>>20))
		return
	}
	if req.Match != "" && !slices.Contains([]string{"exact", "contains_all", "regex", "numeric", "choice"}, req.Match) {
		h.bad(ctx, "match: want exact, contains_all, regex, numeric or choice")
		return
	}
	if req.GradeOn != "" && req.GradeOn != schemas.ChatGradeFinal && req.GradeOn != schemas.ChatGradeTranscript {
		h.bad(ctx, "grade_on: want final or transcript")
		return
	}
	if len(req.Source) > 200 || strings.ContainsAny(req.Source, "\n\r") {
		h.bad(ctx, "source: one line of at most 200 characters")
		return
	}
	if err := req.Mapping.Validate(); err != nil {
		h.bad(ctx, err.Error())
		return
	}
	uploads, msg := decodeUploads(req.Attachments)
	if msg != "" {
		h.bad(ctx, msg)
		return
	}
	raw, s, ok := h.readAnswerSuite(ctx, id, dir)
	if !ok {
		return
	}
	grader := scaffold.RowGraderExact
	if s.Grader == scaffold.RowGraderJudge {
		grader = scaffold.RowGraderJudge
	} else if s.Grader != scaffold.RowGraderExact {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest,
			"benchmark "+id+" uses grader "+s.Grader+"; pick the exact answer or the rubric judge grader first")
		return
	}
	recs, lines, err := scaffold.ReadRecords(req.Format, []byte(req.Data))
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
	if len(recs) > maxAsyncImportRows {
		h.bad(ctx, fmt.Sprintf("%d rows; import at most %d at once (split the file)", len(recs), maxAsyncImportRows))
		return
	}
	mapping := req.Mapping
	if mapping.Empty() {
		mapping = h.savedMapping(dir)
	}
	effective := scaffold.DetectMapping(recs)
	if mapping != nil {
		effective = mergeMapping(effective, *mapping)
	}
	rows, problems := scaffold.MapRows(recs, lines, req.Format == scaffold.RowFormatTickets, mapping)
	existing := map[string]bool{}
	for _, t := range s.Tasks {
		existing[t] = true
	}
	if ents, err := os.ReadDir(filepath.Join(dir, "tasks")); err == nil {
		for _, e := range ents {
			existing[e.Name()] = true
		}
	}
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = req.Format + " import"
	}
	tasks, more := scaffold.ImportRows(rows, scaffold.RowTaskOptions{Grader: grader, Match: req.Match, HeldOut: req.HeldOut,
		Source: source, Taken: func(id string) bool { return existing[id] }, Uploads: uploads, GradeOn: req.GradeOn})
	problems = append(problems, more...)
	slices.SortStableFunc(problems, func(a, b scaffold.RowProblem) int { return a.Line - b.Line })
	out := RowImportResult{Suite: id, Grader: grader, DryRun: req.DryRun, Tasks: tasks, Problems: problems,
		Rows: len(recs), TasksTotal: len(tasks), Columns: scaffold.Columns(recs), Mapping: effective}
	if out.Tasks == nil {
		out.Tasks = []scaffold.RowTask{}
	}
	if out.Problems == nil {
		out.Problems = []scaffold.RowProblem{}
	}
	if len(out.Tasks) > scaffold.MaxImportRows {
		out.Tasks = out.Tasks[:scaffold.MaxImportRows]
	}
	if len(out.Problems) > maxListedProblems {
		out.Problems = out.Problems[:maxListedProblems]
	}
	if req.DryRun || len(tasks) == 0 {
		out.Next = "Check the preview, then import."
		if len(tasks) > scaffold.MaxImportRows {
			out.Next = fmt.Sprintf("Check the preview (the first %d of %d tasks), then import: more than %d rows import in the background.", scaffold.MaxImportRows, len(tasks), scaffold.MaxImportRows)
		}
		if !req.DryRun {
			out.Next = "No row could become a task; fix the rows listed and import again."
		}
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
		return
	}
	if req.SaveMapping && req.Mapping != nil && !req.Mapping.Empty() {
		if err := h.saveMapping(ctx, id, *req.Mapping, actorName(ctx)); err != nil {
			h.sendStoreError(ctx, "save the column mapping", err)
			return
		}
		out.MappingSaved = true
	}
	if req.Async || len(tasks) > scaffold.MaxImportRows {
		st, err := h.startImport(id, dir, source, actorName(ctx), tasks, problems, len(recs))
		if err != nil {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, err.Error())
			return
		}
		out.Import = st
		out.Next = fmt.Sprintf("Importing %d tasks in the background; this page shows the progress. Run the readiness checks when it is done.", len(tasks))
		lib.SendJSON(ctx, h.mode, fasthttp.StatusAccepted, &out)
		return
	}
	if busy := rowImports.running(h.projects.Root(), id); busy != "" {
		lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeConflict, "an import into "+id+" is still running ("+busy+"); wait for it to finish")
		return
	}
	if err := h.writeRowTasks(ctx, id, dir, raw, tasks, source, actorName(ctx)); err != nil {
		var se storeErr
		if errors.As(err, &se) {
			h.sendStoreError(ctx, se.what, se.err)
		} else {
			lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		}
		return
	}
	out.Next = "Run the readiness checks: each new task must prove its check passes the right answer and fails wrong ones before evaluations use it."
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, &out)
}

// storeErr is a project store failure of writeRowTasks.
type storeErr struct {
	what string
	err  error
}

func (e storeErr) Error() string { return e.what + ": " + e.err.Error() }

func (e storeErr) Unwrap() error { return e.err }

// writeRowTasks writes tasks into the suite folder and adds them to
// suite.yaml (raw, the file as read) in one versioned write. On failure the
// task folders written are removed.
func (h *suiteBuild) writeRowTasks(ctx context.Context, id, dir string, raw []byte, tasks []scaffold.RowTask, source, actor string) error {
	var written []string
	undo := func() {
		for _, d := range written {
			os.RemoveAll(d)
		}
	}
	edited := raw
	for _, t := range tasks {
		taskDir := filepath.Join(dir, "tasks", t.ID)
		if err := writeTreeAtomic(taskDir, t.Files); err != nil {
			undo()
			return storeErr{"write task " + t.ID, err}
		}
		written = append(written, taskDir)
		next, _, err := scaffold.AddTaskID(edited, t.ID)
		if err != nil {
			undo()
			return errors.New("add the tasks to suite.yaml: " + err.Error())
		}
		edited = next
	}
	if _, err := h.projects.Write(ctx, projectfs.AreaBenchmarks, id+"/"+suite.SuiteFile, edited,
		projectfs.WriteOpts{Actor: actor, Note: "import " + plural(len(tasks), "task") + " from " + source, IfMatch: shaOf(raw)}); err != nil {
		undo()
		return storeErr{"add the tasks to suite.yaml", err}
	}
	_ = os.Chmod(filepath.Join(dir, suite.SuiteFile), 0o644)
	return nil
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

// setGrader handles PUT /suites/{id}/grader.
func (h *suiteBuild) setGrader(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	id, dir, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	var req SuiteGraderRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"grader":"answer_match"|"judge","judge"?:{"provider","model","prompt_version"?,"votes"?}}: `+err.Error())
		return
	}
	raw, _, ok := h.readAnswerSuite(ctx, id, dir)
	if !ok {
		return
	}
	if j := req.Judge; j != nil {
		for _, v := range []string{j.Provider, j.Model, j.PromptVersion} {
			if len(v) > 200 || strings.ContainsAny(v, "\n\r") {
				h.bad(ctx, "judge: provider, model and prompt version are one line each")
				return
			}
		}
		if j.PromptVersion != "" && !strings.HasPrefix(j.PromptVersion, "qa-v") && !strings.HasPrefix(j.PromptVersion, "longmemeval-v") {
			h.bad(ctx, "judge.prompt_version: leave it empty for qa-v1, the shipped question-and-answer prompt")
			return
		}
	}
	edited, err := scaffold.SetSuiteGrader(raw, req.Grader, req.Judge)
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
	note := "grader " + req.Grader
	if req.Judge != nil && req.Grader == scaffold.RowGraderJudge {
		note += " (" + req.Judge.Provider + "/" + req.Judge.Model + ")"
	}
	if _, err := h.projects.Write(ctx, projectfs.AreaBenchmarks, id+"/"+suite.SuiteFile, edited,
		projectfs.WriteOpts{Actor: actorName(ctx), Note: note, IfMatch: shaOf(raw)}); err != nil {
		h.sendStoreError(ctx, "write suite.yaml", err)
		return
	}
	_ = os.Chmod(filepath.Join(dir, suite.SuiteFile), 0o644)
	var after answerSuite
	_ = yaml.Unmarshal(edited, &after)
	out := SuiteGraderResult{Suite: id, Grader: after.Grader, Grading: after.Grading, Judge: after.Judge,
		Next: "The grader is part of every task's fingerprint: run the readiness checks again before evaluations use this benchmark."}
	if out.Grading == "" {
		out.Grading = "verified"
	}
	if out.Grader == scaffold.RowGraderJudge {
		out.Next = "Label a few answers on the Calibration tab and calibrate the judge, then run the readiness checks again."
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
}

// setSplit handles PUT /suites/{id}/tasks/{task}/split.
func (h *suiteBuild) setSplit(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	id, _, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	taskID, ok := idParam(ctx, h.mode, "task", "task")
	if !ok {
		return
	}
	var req TaskSplitRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"held_out": true|false}: `+err.Error())
		return
	}
	rel := id + "/tasks/" + taskID + "/" + suite.TaskFile // the loader reads tasks/<id>
	raw, _, err := h.projects.Read(ctx, projectfs.AreaBenchmarks, rel)
	if err != nil {
		if errors.Is(err, projectfs.ErrNotFound) {
			sendNotFound(ctx, h.mode, "task", id+"/"+taskID)
			return
		}
		h.sendStoreError(ctx, "read "+rel, err)
		return
	}
	edited, err := scaffold.SetTaskSplit(raw, req.HeldOut)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "task.yaml: "+err.Error())
		return
	}
	split := "public"
	if req.HeldOut {
		split = "held_out"
	}
	if _, err := h.projects.Write(ctx, projectfs.AreaBenchmarks, rel, edited,
		projectfs.WriteOpts{Actor: actorName(ctx), Note: "split " + split, IfMatch: shaOf(raw)}); err != nil {
		h.sendStoreError(ctx, "write "+rel, err)
		return
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &TaskSplitResult{Suite: id, Task: taskID, Split: split})
}
