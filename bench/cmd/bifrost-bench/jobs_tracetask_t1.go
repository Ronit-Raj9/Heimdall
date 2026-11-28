package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/tracebuild"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"gopkg.in/yaml.v3"
)

// Round 7 (T1): tracetask.build asks a builder model to turn one inbox
// failure into a finished task draft (tracebuild.Build). The draft is kept
// beside the inbox item (inbox/items/<item>/build/), never in a suite's
// admitted set; a person's approval moves it on. The model call goes
// through the gateway with one virtual key for the job and a dollar cap,
// the way trace.draft does (openCaller).

const jobTraceTaskBuild = "tracetask.build"

// traceTaskMaxUSD caps one build's spend.
const traceTaskMaxUSD = 5.0

// TraceTaskBuildDir is the folder under an inbox item holding the latest
// build: result.json and the task folder.
const TraceTaskBuildDir = "build"

func init() {
	registerJobKind(jobKindSpec{Kind: jobTraceTaskBuild, Local: true, Hosted: true, Opts: []jobs.KindOption{jobs.WithLimit(4, 1)},
		Validate: validateTraceTask, Run: runTraceTask})
}

// traceTaskParams are the params of tracetask.build. Provider, key_id and
// yes come with modelParams like trace.draft.
type traceTaskParams struct {
	InboxID  string `json:"inbox_id"`
	Suite    string `json:"suite"`
	Feedback string `json:"feedback,omitempty"` // the failing control of an earlier attempt ("Try again")
	RetryOf  string `json:"retry_of,omitempty"` // the earlier job id of a "Try again"
	// Mode "finalize" (with TaskID): no model call; admission again after a
	// person approved, and the lock entry when every check holds.
	Mode   string `json:"mode,omitempty"`
	TaskID string `json:"task_id,omitempty"`
	modelParams
}

// traceTaskAdmission is the admission part of the job's result. Status is
// "ran", or "not_run" with Reason when no admission could run here.
// Admitted is false until a person approves.
type traceTaskAdmission struct {
	Status   string           `json:"status"`
	Reason   string           `json:"reason,omitempty"`
	ExpID    string           `json:"exp_id,omitempty"` // the worker experiment that ran the controls
	Admitted bool             `json:"admitted"`
	Controls []traceTaskCheck `json:"controls"`
	Guards   []traceTaskCheck `json:"guards"`
}

type traceTaskCheck struct {
	Name   string `json:"name"`
	Want   string `json:"want"`
	Got    string `json:"got"`
	Detail string `json:"detail,omitempty"`
}

type traceTaskResult struct {
	*tracebuild.Result
	Admission traceTaskAdmission `json:"admission"`
	Attempt   int                `json:"attempt"`
	// DraftSuite is the suite the draft sits in: on the hosted site a draft
	// suite of its own in the org's project (<suite>-draft-...), locally the
	// same id in the job's scratch folder.
	DraftSuite string `json:"draft_suite"` // 1, or 2 after the one retry with admission's feedback
}

// traceTaskAdmit runs admission for the one drafted task; tests replace it
// at this interface (the same as repoTaskAdmit). Hosted: admitOnWorker
// with record false (the readiness controls on a worker runner, the
// tasks.mine bridge; the server process has no Docker). Local: suite.Admit
// in process on benchRoot.
var traceTaskAdmit = func(ctx context.Context, env jobEnv, j *jobs.Job, benchRoot, suiteID, id string, log jobs.Logger) (hostedAdmitTask, string, error) {
	if env.Hosted {
		return admitOnWorker(ctx, env, j, suiteID, id, log, false)
	}
	rec, err := traceTaskAdmitLocal(ctx, env, benchRoot, suiteID, id)
	if err != nil {
		return hostedAdmitTask{Task: id}, "", err
	}
	return hostedAdmitTask{Task: id, Checks: rec.Checks}, "", nil
}

// traceTaskNoWorker is the reason shown when no worker can take admission.
const traceTaskNoWorker = "No worker online"

func traceTaskAdmitLocal(ctx context.Context, env jobEnv, benchRoot, suiteID, taskID string) (*schemas.AdmissionRecord, error) {
	if env.App == nil || env.App.reg == nil {
		return nil, errors.New(traceTaskNoWorker)
	}
	a := env.App
	s, err := loadSuite(ctx, benchRoot, suiteID)
	if err != nil {
		return nil, err
	}
	t := s.TaskByID(taskID)
	if t == nil {
		return nil, fmt.Errorf("the draft suite has no task %q", taskID)
	}
	if problems := suite.StaticValidation(ctx, s, taskID); suite.Errors(problems) > 0 {
		var msgs []string
		for _, p := range problems {
			msgs = append(msgs, fmt.Sprint(p))
		}
		return &schemas.AdmissionRecord{Checks: []schemas.AdmissionCheck{{Name: suite.StaticValidationCheck, Want: suite.GotPass, Got: suite.GotFail, Detail: strings.Join(msgs, "; ")}}}, nil
	}
	deps := suite.AdmitDeps{Logger: a.logger}
	for _, e := range a.reg.Environments {
		if e.GetName() == s.Environment {
			deps.Env = e
		}
	}
	for _, g := range a.reg.Graders {
		if g.GetName() == s.Grader {
			deps.Grader = g
		}
	}
	if deps.Env == nil || deps.Grader == nil {
		return nil, fmt.Errorf("suite %s: environment %q or grader %q is not registered", s.ID, s.Environment, s.Grader)
	}
	if err := a.ensureImages(ctx, s); err != nil {
		return nil, err
	}
	return suite.Admit(ctx, deps, s, t)
}

// admissionOf splits a record into controls and guards; admitted stays
// false (a person approves).
func admissionOf(checks []schemas.AdmissionCheck) traceTaskAdmission {
	out := traceTaskAdmission{Status: "ran", Controls: []traceTaskCheck{}, Guards: []traceTaskCheck{}}
	for _, c := range checks {
		tc := traceTaskCheck{Name: c.Name, Want: c.Want, Got: c.Got, Detail: c.Detail}
		if strings.HasPrefix(c.Name, "guard_") {
			out.Guards = append(out.Guards, tc)
		} else {
			out.Controls = append(out.Controls, tc)
		}
	}
	return out
}

// failedControl is the first control whose result is not the wanted one,
// as feedback text, or "".
func failedControl(a traceTaskAdmission) string {
	for _, c := range a.Controls {
		if c.Want != c.Got {
			return fmt.Sprintf("control %s: want %s got %s. %s", c.Name, c.Want, c.Got, c.Detail)
		}
	}
	return ""
}

func validateTraceTask(_ context.Context, env jobEnv, raw json.RawMessage) (any, string, error) {
	var p traceTaskParams
	if err := liveDecode(raw, &p); err != nil {
		return nil, "", err
	}
	if err := checkTraceTask(env, &p); err != nil {
		return nil, "", err
	}
	return p, "inbox/" + p.InboxID, nil
}

func checkTraceTask(env jobEnv, p *traceTaskParams) error {
	if p.Mode == traceTaskFinalize {
		switch {
		case !traceinbox.ValidItemID(p.InboxID):
			return liveInvalid("item %q is not an inbox item id", p.InboxID)
		case !scaffold.ValidID(p.Suite) || !scaffold.ValidID(p.TaskID):
			return liveInvalid("Name the benchmark and the task.")
		}
		return nil
	}
	if p.Mode != "" {
		return liveInvalid("mode %q: want finalize or nothing", p.Mode)
	}
	switch {
	case !traceinbox.ValidItemID(p.InboxID):
		return liveInvalid("item %q is not an inbox item id", p.InboxID)
	case !scaffold.ValidID(p.Suite):
		return liveInvalid("Pick a benchmark first.")
	case strings.TrimSpace(p.Model) == "":
		return liveInvalid("Pick a model first.")
	case p.BudgetUSD <= 0 || math.IsNaN(p.BudgetUSD) || math.IsInf(p.BudgetUSD, 0):
		return liveInvalid("Set a spending cap above zero.")
	}
	p.BudgetUSD = math.Min(p.BudgetUSD, traceTaskMaxUSD)
	p.Feedback = strings.TrimSpace(p.Feedback)
	if len(p.Feedback) > 2000 {
		p.Feedback = p.Feedback[:2000]
	}
	if _, err := os.Stat(filepath.Join(traceDraftInboxDir(env), traceinbox.ItemsDir, p.InboxID)); err != nil {
		return liveInvalid("No inbox item %s.", p.InboxID)
	}
	return nil
}

func runTraceTask(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, progress func(any)) (any, error) {
	var p traceTaskParams
	if err := liveDecode(j.Params, &p); err != nil {
		return nil, err
	}
	if err := checkTraceTask(env, &p); err != nil {
		return nil, err
	}
	if p.Mode == traceTaskFinalize {
		return finalizeTraceTask(ctx, env, j, p, log)
	}
	step := func(s string) {
		if progress != nil {
			progress(map[string]string{"step": s})
		}
	}
	store := traceinbox.NewFileStore(traceDraftInboxDir(env))
	sess, err := store.Session(p.InboxID)
	if err != nil {
		return nil, err
	}
	if openCaller == nil {
		return nil, errors.New("this server makes no model calls")
	}
	c, done, err := openCaller(ctx, env, j, p.modelParams)
	if err != nil {
		return nil, err
	}
	defer done()
	buildDir := filepath.Join(store.Dir(), traceinbox.ItemsDir, p.InboxID, TraceTaskBuildDir)
	if err := os.RemoveAll(buildDir); err != nil {
		return nil, err
	}
	var out traceTaskResult
	feedback := p.Feedback
	for attempt := 1; attempt <= 2; attempt++ {
		work := filepath.Join(buildDir, fmt.Sprintf("attempt-%d", attempt))
		if err := os.MkdirAll(work, 0o755); err != nil {
			return nil, err
		}
		if attempt == 2 {
			step("retry")
		}
		in := tracebuild.Input{Item: p.InboxID, Session: sess, Suite: p.Suite, WorkDir: work, Progress: step,
			Model: c, Limits: tracebuild.Limits{BudgetUSD: p.BudgetUSD}, Feedback: feedback}
		benchRoot := filepath.Join(work, "benchmarks")
		if root := traceTaskBenchRoot(env); root != "" {
			// the draft is a task of the target benchmark in the org's
			// project (added to suite.yaml, not admitted), so the worker can
			// admit it and the approve route finds it at task.path
			benchRoot, in.BenchRoot = root, root
			in.TaskID = traceTaskID(p.InboxID, attempt)
			if err := os.RemoveAll(filepath.Join(root, p.Suite, "tasks", in.TaskID)); err != nil {
				return nil, err
			}
		}
		res, err := tracebuild.Build(ctx, in)
		if err != nil {
			if attempt == 2 && out.Result != nil {
				out.Notes = append(out.Notes, "The retry with admission's feedback failed: "+err.Error())
				break
			}
			return nil, err
		}
		out = traceTaskResult{Result: res, Attempt: attempt, Admission: traceTaskAdmission{Status: "not_run", Controls: []traceTaskCheck{}, Guards: []traceTaskCheck{}}}
		step("admit")
		verdict, expID, err := traceTaskAdmit(ctx, env, j, benchRoot, in.Suite, res.TaskID, log)
		if err != nil {
			out.Admission.Reason = err.Error()
			if errors.Is(err, errNoRunner) || strings.Contains(err.Error(), "no launcher") || strings.Contains(err.Error(), "runner") {
				out.Admission.Reason = traceTaskNoWorker
			}
			break
		}
		out.Admission = admissionOf(verdict.Checks)
		out.Admission.ExpID = expID
		if len(verdict.Checks) == 0 && verdict.Reason != "" {
			out.Admission.Status, out.Admission.Reason = "not_run", verdict.Reason
			break
		}
		feedback = failedControl(out.Admission)
		if feedback == "" || p.Feedback != "" {
			break // every control holds, or this job already is the retry
		}
		if log != nil {
			log.Printf("admission refused attempt %d: %s; building once more", attempt, feedback)
		}
	}
	res := out.Result
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(buildDir, "result.json"), append(data, '\n'), 0o644); err != nil {
		return nil, err
	}
	if log != nil {
		log.Printf("built %s for %s: %s, shape %s, %d calls, $%.4f", res.TaskID, p.InboxID, res.Failure.Kind, res.Shape, res.ModelCalls, res.SpentUSD)
	}
	return out, nil
}

// traceTaskFinalize is the mode of the run after a person's approval.
const traceTaskFinalize = "finalize"

// traceTaskBenchRoot is the org project's benchmarks folder, or "" when the
// job has no app (tests write in the job's scratch folder).
var traceTaskBenchRoot = func(env jobEnv) string {
	if env.App == nil || env.App.cfg == nil {
		return ""
	}
	return env.App.cfg.Path(env.App.cfg.BenchmarksRoot)
}

// traceTaskID is the id of the built task of an inbox item.
func traceTaskID(item string, attempt int) string {
	var b strings.Builder
	for _, r := range strings.ToLower(item) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	short := b.String()
	if len(short) > 16 {
		short = short[:16]
	}
	return fmt.Sprintf("built-%s-%d", short, attempt)
}

// traceTaskFinal is the result of a finalize run.
type traceTaskFinal struct {
	Task      tracebuild.TaskRef `json:"task"`
	Admission traceTaskAdmission `json:"admission"`
	Failing   string             `json:"failing,omitempty"`
}

// traceTaskFinalAdmit admits one approved task and writes its lock entry
// when every check holds; tests replace it. Hosted: admitOnWorker with
// record true. Local: the CLI's task admit, which writes suite.lock.
var traceTaskFinalAdmit = func(ctx context.Context, env jobEnv, j *jobs.Job, suiteID, id string, log jobs.Logger) (hostedAdmitTask, string, error) {
	if env.Hosted {
		return admitOnWorker(ctx, env, j, suiteID, id, log, true)
	}
	_, err := runCLI(ctx, env, []string{"task", "admit", "-config", cliConfig, suiteID + "/" + id}, log)
	t := hostedAdmitTask{Task: id, Admitted: err == nil}
	if err != nil {
		t.Reason = err.Error()
	}
	return t, "", nil
}

// finalizeTraceTask runs admission again for an approved draft. It refuses
// when the approval record is absent: it never writes that record.
func finalizeTraceTask(ctx context.Context, env jobEnv, j *jobs.Job, p traceTaskParams, log jobs.Logger) (any, error) {
	root := traceTaskBenchRoot(env)
	if root == "" {
		return nil, errors.New("this server has no benchmarks folder")
	}
	raw, err := os.ReadFile(filepath.Join(root, p.Suite, "tasks", p.TaskID, "task.yaml"))
	if err != nil {
		return nil, err
	}
	var t schemas.Task
	if err := yaml.Unmarshal(raw, &t); err != nil {
		return nil, err
	}
	if t.Provenance == nil || t.Provenance.InstructionReviewed == nil || !*t.Provenance.InstructionReviewed {
		return nil, liveInvalid("A person must approve the instruction first: open the draft and press Approve.")
	}
	verdict, expID, err := traceTaskFinalAdmit(ctx, env, j, p.Suite, p.TaskID, log)
	if err != nil {
		if errors.Is(err, errNoRunner) {
			return nil, errors.New(traceTaskNoWorker)
		}
		return nil, err
	}
	out := traceTaskFinal{Task: tracebuild.TaskRef{Suite: p.Suite, ID: p.TaskID, Path: p.Suite + "/tasks/" + p.TaskID}, Admission: admissionOf(verdict.Checks)}
	out.Admission.ExpID = expID
	out.Admission.Admitted = verdict.Admitted
	if !verdict.Admitted {
		out.Failing = verdict.Reason
		for _, c := range append(slices.Clone(out.Admission.Controls), out.Admission.Guards...) {
			if c.Want != "" && c.Want != c.Got && out.Failing == "" {
				out.Failing = fmt.Sprintf("%s: want %s, got %s", c.Name, c.Want, c.Got)
			}
		}
	}
	return out, nil
}
