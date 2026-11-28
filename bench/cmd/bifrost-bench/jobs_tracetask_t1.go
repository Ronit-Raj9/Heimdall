package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/scaffold"
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

// Admission is the admission part of the job's result. Status is "not_run"
// until lane T1's admission step lands; Admitted is false until a person
// approves.
type traceTaskAdmission struct {
	Status   string           `json:"status"`
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
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		return nil, err
	}
	res, err := tracebuild.Build(ctx, tracebuild.Input{Item: p.InboxID, Session: sess, Suite: p.Suite, WorkDir: buildDir,
		Model: c, Limits: tracebuild.Limits{BudgetUSD: p.BudgetUSD}, Feedback: p.Feedback})
	if err != nil {
		return nil, err
	}
	out := traceTaskResult{Result: res, Admission: traceTaskAdmission{Status: "not_run", Controls: []traceTaskCheck{}, Guards: []traceTaskCheck{}}}
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
