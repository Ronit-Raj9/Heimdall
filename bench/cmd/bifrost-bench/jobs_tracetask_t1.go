package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/tracebuild"
	"github.com/maximhq/bifrost/bench/traceinbox"
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
	modelParams
}

// traceTaskAdmission is the admission part of the job's result. Status is
// "ran", or "not_run" with Reason when no admission could run here.
// Admitted is false until a person approves.
type traceTaskAdmission struct {
	Status   string           `json:"status"`
	Reason   string           `json:"reason,omitempty"`
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
	Attempt   int                `json:"attempt"` // 1, or 2 after the one retry with admission's feedback
}

// errAdmitNeedsWorker: the hosted server process has no Docker.
var errAdmitNeedsWorker = errors.New("admission of a built draft is not dispatched to a worker yet; run it with heimdall task admit")

// traceTaskAdmit runs admission for the one drafted task under benchRoot.
// Tests replace it. The default runs suite.Admit in process where this
// process has Docker (the local server); on the hosted site it returns
// errAdmitNeedsWorker.
var traceTaskAdmit = func(ctx context.Context, env jobEnv, benchRoot, suiteID, taskID string) (*schemas.AdmissionRecord, error) {
	if env.Hosted || env.App == nil || env.App.reg == nil {
		return nil, errAdmitNeedsWorker
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
func admissionOf(rec *schemas.AdmissionRecord) traceTaskAdmission {
	out := traceTaskAdmission{Status: "ran", Controls: []traceTaskCheck{}, Guards: []traceTaskCheck{}}
	for _, c := range rec.Checks {
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

func runTraceTask(ctx context.Context, env jobEnv, j *jobs.Job, log jobs.Logger, _ func(any)) (any, error) {
	var p traceTaskParams
	if err := liveDecode(j.Params, &p); err != nil {
		return nil, err
	}
	if err := checkTraceTask(env, &p); err != nil {
		return nil, err
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
		res, err := tracebuild.Build(ctx, tracebuild.Input{Item: p.InboxID, Session: sess, Suite: p.Suite, WorkDir: work,
			Model: c, Limits: tracebuild.Limits{BudgetUSD: p.BudgetUSD}, Feedback: feedback})
		if err != nil {
			if attempt == 2 && out.Result != nil {
				out.Notes = append(out.Notes, "The retry with admission's feedback failed: "+err.Error())
				break
			}
			return nil, err
		}
		out = traceTaskResult{Result: res, Attempt: attempt, Admission: traceTaskAdmission{Status: "not_run", Controls: []traceTaskCheck{}, Guards: []traceTaskCheck{}}}
		rec, err := traceTaskAdmit(ctx, env, filepath.Join(work, "benchmarks"), p.Suite, res.TaskID)
		if err != nil {
			out.Admission.Reason = err.Error()
			break
		}
		out.Admission = admissionOf(rec)
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
