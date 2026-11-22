package handlers

// Scenario rows from the website: a spreadsheet or JSON Lines of support
// conversations (the customer's opening message, who they are, the account
// they are about, the actions the agent must and must not take, a reply
// rubric) becomes support tasks with a simulated customer and a declarative
// checks.yaml. Each task copies the shape of a task already in the suite.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// ScenarioRowsRequest is the body of POST /suites/{id}/scenario-rows.
type ScenarioRowsRequest struct {
	// Format is csv (nested fields as JSON in a cell, lists split by |) or
	// jsonl (JSON Lines or a JSON array).
	Format string `json:"format"`
	Data   string `json:"data"`
	// ShapeTask names the task whose services, instruction and seed the new
	// tasks copy; empty takes the first task with a simulated customer.
	ShapeTask string `json:"shape_task,omitempty"`
	// SwitchGrader moves the suite to grader checks when it uses another
	// one; tasks without a checks.yaml keep their verifier command.
	SwitchGrader bool                  `json:"switch_grader,omitempty"`
	Judge        *scaffold.JudgeConfig `json:"judge,omitempty"` // pins the reply judge (grading: judged) with the switch
	HeldOut      bool                  `json:"held_out,omitempty"`
	DryRun       bool                  `json:"dry_run,omitempty"`
	Source       string                `json:"source,omitempty"`
}

// ScenarioRowsResult answers POST /suites/{id}/scenario-rows.
type ScenarioRowsResult struct {
	Suite         string                  `json:"suite"`
	Shape         string                  `json:"shape"`
	Grader        string                  `json:"grader"`
	GraderChanged bool                    `json:"grader_changed"`
	DryRun        bool                    `json:"dry_run"`
	Tasks         []scaffold.ScenarioTask `json:"tasks"`
	Problems      []scaffold.RowProblem   `json:"problems"`
	Next          string                  `json:"next"`
}

func init() {
	RegisterOrgArea("scenariorows", func(env OrgEnv, r *router.Router) {
		h := &suiteBuild{&suiteAuthoring{mode: env.Mode, projects: env.Services.Projects, suites: env.Services.Suites}}
		r.POST(APIPrefix+"/suites/{id}/scenario-rows", env.U(refuseBuiltin(env.Mode, h.suites, h.importScenarioRows)))
	})
}

// scenarioSuite is what the area reads from suite.yaml.
type scenarioSuite struct {
	Grader  string                `yaml:"grader"`
	Grading string                `yaml:"grading"`
	Tasks   []string              `yaml:"tasks"`
	Judge   *scaffold.JudgeConfig `yaml:"judge"`
}

// importScenarioRows handles POST /suites/{id}/scenario-rows.
func (h *suiteBuild) importScenarioRows(ctx *fasthttp.RequestCtx) {
	if !h.needProjects(ctx) {
		return
	}
	id, dir, ok := h.suiteDirParam(ctx)
	if !ok {
		return
	}
	var req ScenarioRowsRequest
	if err := decodeAuthoring(ctx.PostBody(), &req); err != nil {
		h.bad(ctx, `body must be {"format","data","shape_task"?,"switch_grader"?,"judge"?,"held_out"?,"dry_run"?,"source"?}: `+err.Error())
		return
	}
	if len(req.Data) > maxRowImportBytes {
		lib.SendError(ctx, h.mode, fasthttp.StatusRequestEntityTooLarge, lib.ErrTypeBadRequest, "the data is over 8 MiB; split it into smaller files")
		return
	}
	if len(req.Source) > 200 || strings.ContainsAny(req.Source, "\n\r") {
		h.bad(ctx, "source: one line of at most 200 characters")
		return
	}
	if req.ShapeTask != "" && !scaffold.ValidID(req.ShapeTask) {
		h.bad(ctx, "shape_task: not a task id")
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, suite.SuiteFile))
	if err != nil {
		h.sendStoreError(ctx, "read suite.yaml", err)
		return
	}
	var s scenarioSuite
	if err := yaml.Unmarshal(raw, &s); err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "suite.yaml: "+err.Error()+"; fix it on the Edit tab")
		return
	}
	edited, changed := raw, false
	if s.Grader != scaffold.ChecksGrader || (req.Judge != nil && req.SwitchGrader) {
		if !req.SwitchGrader {
			lib.SendError(ctx, h.mode, fasthttp.StatusConflict, lib.ErrTypeBadRequest,
				"benchmark "+id+" uses grader "+s.Grader+"; scenario tasks are graded by their checks.yaml, so switch the benchmark to grader checks (switch_grader: true). Its other tasks keep their own verifier and need their readiness checks again")
			return
		}
		next, err := scaffold.SetChecksGrader(raw, req.Judge)
		if err != nil {
			h.bad(ctx, err.Error())
			return
		}
		edited, changed = next, s.Grader != scaffold.ChecksGrader || req.Judge != nil
		_ = yaml.Unmarshal(edited, &s)
	}
	shape, err := h.scenarioShape(dir, s.Tasks, req.ShapeTask)
	if err != nil {
		lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, err.Error())
		return
	}
	rows, problems, err := scaffold.ParseScenarioRows(req.Format, []byte(req.Data))
	if err != nil {
		h.bad(ctx, err.Error())
		return
	}
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
		source = req.Format + " scenario import"
	}
	judged := s.Grading == "judged" && s.Judge != nil && s.Judge.Provider != "" && s.Judge.Model != ""
	tasks, more := scaffold.ImportScenarioRows(rows, scaffold.ScenarioOptions{Shape: shape, HeldOut: req.HeldOut, Judged: judged,
		Source: source, Taken: func(id string) bool { return existing[id] }})
	problems = append(problems, more...)
	slices.SortStableFunc(problems, func(a, b scaffold.RowProblem) int { return a.Line - b.Line })
	out := ScenarioRowsResult{Suite: id, Shape: shape.From, Grader: scaffold.ChecksGrader, GraderChanged: changed, DryRun: req.DryRun, Tasks: tasks, Problems: problems}
	if out.Tasks == nil {
		out.Tasks = []scaffold.ScenarioTask{}
	}
	if out.Problems == nil {
		out.Problems = []scaffold.RowProblem{}
	}
	if req.DryRun || len(tasks) == 0 {
		out.Next = "Check the preview, then import."
		if !req.DryRun {
			out.Next = "No row could become a task; fix the rows listed and import again."
		}
		lib.SendJSON(ctx, h.mode, fasthttp.StatusOK, &out)
		return
	}
	var written []string
	undo := func() {
		for _, d := range written {
			os.RemoveAll(d)
		}
	}
	for _, t := range tasks {
		taskDir := filepath.Join(dir, "tasks", t.ID)
		if err := writeTreeAtomic(taskDir, t.Files); err != nil {
			undo()
			h.sendStoreError(ctx, "write task "+t.ID, err)
			return
		}
		written = append(written, taskDir)
		next, _, err := scaffold.AddTaskID(edited, t.ID)
		if err != nil {
			undo()
			lib.SendError(ctx, h.mode, fasthttp.StatusUnprocessableEntity, lib.ErrTypeBadRequest, "add the tasks to suite.yaml: "+err.Error())
			return
		}
		edited = next
	}
	note := "import " + plural(len(tasks), "scenario task") + " from " + source
	if changed {
		note += " (grader checks)"
	}
	if _, err := h.projects.Write(ctx, projectfs.AreaBenchmarks, id+"/"+suite.SuiteFile, edited,
		projectfs.WriteOpts{Actor: actorName(ctx), Note: note, IfMatch: shaOf(raw)}); err != nil {
		undo()
		h.sendStoreError(ctx, "add the tasks to suite.yaml", err)
		return
	}
	_ = os.Chmod(filepath.Join(dir, suite.SuiteFile), 0o644)
	out.Next = "Run the readiness checks: each new task must prove its reference passes every check and its mutants fail before evaluations use it."
	if changed {
		out.Next += " The benchmark now uses grader checks, so its earlier tasks need their readiness checks again too."
	}
	lib.SendJSON(ctx, h.mode, fasthttp.StatusCreated, &out)
}

// scenarioShape reads the task to copy: the named one, else the first task
// of the suite with a simulated customer.
func (h *suiteBuild) scenarioShape(dir string, tasks []string, want string) (*scaffold.ScenarioShape, error) {
	candidates := tasks
	if want != "" {
		candidates = []string{want}
	}
	var firstErr error
	for _, t := range candidates {
		files, err := scaffold.ReadTaskFolder(filepath.Join(dir, "tasks", t))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		sh, err := scaffold.ScenarioShapeFrom(t, files)
		if err == nil {
			return sh, nil
		}
		if firstErr == nil || want != "" {
			firstErr = err
		}
	}
	if want != "" && firstErr != nil {
		return nil, firstErr
	}
	return nil, errScenarioShape
}

var errScenarioShape = scenarioShapeError("no task of this benchmark has a simulated customer to copy; scenario rows need a support benchmark (Benchmarks > New, template support), whose sample task they copy")

type scenarioShapeError string

func (e scenarioShapeError) Error() string { return string(e) }
