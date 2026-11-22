package main

// Hosted readiness checks with the full control set (lane Y07). Local
// `suite admit` runs every control of a task in the agent's place: the
// reference three times, the empty control, every mutant, every wrong
// answer of the hidden answer spec, the exploit and the two null agents
// (suite/admitscenario.go, suite/admission.go). The hosted suite.admit job
// runs the same controls as the arms of one bridge experiment on the org's
// worker, so a hosted record holds the same check names as a local one:
//
//	A              reference_1   the reference (want pass)
//	reference_2,3  reference_2,3 the reference again: determinism
//	empty          empty_control (empty_patch on a patch task)
//	mutant_N       mutant_N      the task's Nth mutant
//	wrong_answer_N wrong_answer_N the Nth wrong answer of the answer spec
//	exploit        exploit
//	B              null_noop     changes nothing
//	null_refuse    null_refuse   declines in words
//
// Arms A and B keep the ids the paired verdict needs. Every task runs every
// arm; a task without the Nth mutant or wrong answer prints that it has
// none, and that run is not one of its checks. A check the worker cannot
// run (the honest solution of an impossible task, the stale-notes overlay)
// is recorded as not run, and the task is not ready on it. The file checks
// (instruction edited, no secrets, verifier network) run in process.
//
// Every agent call of the experiment is scripted. A judged benchmark's
// judge must still grade the controls, or no judged task could ever be
// ready: with the organization's consent (judge_consent, the Build tab
// toggle) the experiment sets paid_judge, and the judge calls its provider
// on the organization's own key through the worker's gateway, as in an
// evaluation.

import (
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/graders"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
	"github.com/maximhq/bifrost/bench/utils"
)

// Readiness arms.
const (
	readinessRefRuns     = 3
	readinessMaxIndexed  = 8 // mutants and wrong answers run per task; more are recorded as not run
	readinessArmEmpty    = "empty"
	readinessArmExploit  = "exploit"
	readinessArmRefuse   = "null_refuse"
	readinessNotRun      = "not_run"
	readinessSlotTimeout = 20 * time.Second // added to the default timeout per slot
)

// readinessArm is one control arm of the hosted readiness experiment.
type readinessArm struct {
	ID    string // experiment arm id
	Kind  string // scripted kind (noop runs the noop agent file)
	Index int    // reference repeat, mutant or wrong answer number
	Label string
}

// readinessCheck is one admission check a task's record will hold: the arm
// whose run decides it, or why it was not run.
type readinessCheck struct {
	Name   string
	Arm    string
	Want   string
	NotRun string
}

// readinessTaskChecks is the planned check list of task t, in local
// admission's order.
func readinessTaskChecks(s *schemas.Suite, t *schemas.Task) []readinessCheck {
	kind := utils.ControlKind(s, t)
	var out []readinessCheck
	notRun := func(name, want, why string) {
		out = append(out, readinessCheck{Name: name, Want: want, NotRun: why})
	}
	if t.Controls.Honest != "" {
		notRun("honest_original_1", suite.GotPass, "not run on this worker: the honest solution of an impossible task is checked by bifrost-bench suite admit on your own machine")
	}
	for i := 1; i <= readinessRefRuns; i++ {
		out = append(out, readinessCheck{Name: fmt.Sprintf("reference_%d", i), Arm: referenceArm(i), Want: suite.GotPass})
	}
	empty := "empty_control"
	if kind == utils.ControlPatch {
		empty = "empty_patch"
	}
	out = append(out, readinessCheck{Name: empty, Arm: readinessArmEmpty, Want: suite.GotFail})
	for i := range t.Controls.Mutants {
		c := readinessCheck{Name: fmt.Sprintf("mutant_%d", i+1), Arm: fmt.Sprintf("mutant_%d", i+1), Want: suite.GotFail}
		if i >= readinessMaxIndexed {
			c.Arm, c.NotRun = "", fmt.Sprintf("not run on this worker: at most %d near misses are checked per task; remove some, or run bifrost-bench suite admit", readinessMaxIndexed)
		}
		out = append(out, c)
	}
	if kind == utils.ControlAnswer {
		wrong, err := utils.TaskWrongAnswers(t)
		if err != nil {
			notRun("wrong_answer_1", suite.GotFail, "the wrong answers could not be read: "+err.Error())
		}
		for i := range wrong {
			c := readinessCheck{Name: fmt.Sprintf("wrong_answer_%d", i+1), Arm: fmt.Sprintf("wrong_answer_%d", i+1), Want: suite.GotFail}
			if i >= readinessMaxIndexed {
				c.Arm, c.NotRun = "", fmt.Sprintf("not run on this worker: at most %d wrong answers are checked per task; remove some, or run bifrost-bench suite admit", readinessMaxIndexed)
			}
			out = append(out, c)
		}
	}
	out = append(out, readinessCheck{Name: "exploit", Arm: readinessArmExploit, Want: suite.GotFail})
	if kind == utils.ControlPatch && t.Notes.Stale != "" && t.Controls.Honest == "" {
		notRun("reference_stale_notes", suite.GotPass, "not run on this worker: the stale-notes overlay is checked by bifrost-bench suite admit on your own machine")
	}
	out = append(out,
		readinessCheck{Name: "null_noop", Arm: admitArmNoop, Want: suite.GotFail},
		readinessCheck{Name: "null_refuse", Arm: readinessArmRefuse, Want: suite.GotFail})
	return out
}

// referenceArm is the arm of reference run i: A carries the first.
func referenceArm(i int) string {
	if i == 1 {
		return admitArmOracle
	}
	return fmt.Sprintf("reference_%d", i)
}

// readinessArms is the arm set covering every check of tasks.
func readinessArms(s *schemas.Suite, tasks []*schemas.Task) []readinessArm {
	mutants, wrong := 0, 0
	for _, t := range tasks {
		for _, c := range readinessTaskChecks(s, t) {
			var n int
			switch {
			case c.Arm == "":
			case strings.HasPrefix(c.Arm, "mutant_"):
				fmt.Sscanf(c.Arm, "mutant_%d", &n)
				mutants = max(mutants, n)
			case strings.HasPrefix(c.Arm, "wrong_answer_"):
				fmt.Sscanf(c.Arm, "wrong_answer_%d", &n)
				wrong = max(wrong, n)
			}
		}
	}
	var out []readinessArm
	for i := 1; i <= readinessRefRuns; i++ {
		out = append(out, readinessArm{ID: referenceArm(i), Kind: adapters.ScriptedOracle, Index: i, Label: fmt.Sprintf("right answer, try %d", i)})
	}
	out = append(out, readinessArm{ID: readinessArmEmpty, Kind: adapters.ScriptedEmpty, Label: "empty answer"})
	for i := 1; i <= mutants; i++ {
		out = append(out, readinessArm{ID: fmt.Sprintf("mutant_%d", i), Kind: adapters.ScriptedMutant, Index: i, Label: fmt.Sprintf("near miss %d", i)})
	}
	for i := 1; i <= wrong; i++ {
		out = append(out, readinessArm{ID: fmt.Sprintf("wrong_answer_%d", i), Kind: adapters.ScriptedWrongAnswer, Index: i, Label: fmt.Sprintf("wrong answer %d", i)})
	}
	return append(out,
		readinessArm{ID: readinessArmExploit, Kind: adapters.ScriptedExploit, Label: "gaming attempt"},
		readinessArm{ID: admitArmNoop, Kind: adapters.ScriptedNoop, Label: "does nothing"},
		readinessArm{ID: readinessArmRefuse, Kind: adapters.ScriptedRefuse, Label: "refuses"})
}

// readinessExperiment is the hosted readiness experiment of tasks (one
// split): every control arm, one trial, every agent call scripted, the
// judge paid only when paidJudge.
func readinessExperiment(suiteRef, oracle, noop string, s *schemas.Suite, tasks []*schemas.Task, split string, paidJudge bool) *schemas.Experiment {
	id, _, _ := strings.Cut(suiteRef, "@")
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.ID)
	}
	arms := map[string]schemas.ArmSpec{}
	for _, a := range readinessArms(s, tasks) {
		if a.Kind == adapters.ScriptedNoop {
			arms[a.ID] = schemas.ArmSpec{Label: a.Label, Agent: noop}
			continue
		}
		set := map[string]any{"scripted.kind": a.Kind}
		if a.Index > 0 {
			set["scripted.index"] = a.Index
		}
		arms[a.ID] = schemas.ArmSpec{Label: a.Label, Set: set}
	}
	cond := schemas.ConditionSpec{ID: schemas.ConditionClean, Tasks: schemas.TaskSelector{IDs: ids}, Trials: 1}
	if split == schemas.SplitHeldOut {
		cond.Split = schemas.SplitHeldOut
	}
	return &schemas.Experiment{
		Name:             "admit-" + id,
		Suite:            suiteRef,
		Base:             oracle,
		Arms:             arms,
		Primary:          schemas.Endpoint{Condition: schemas.ConditionClean, Metric: "verified_success"},
		Guards:           []schemas.GuardSpec{{Rule: "no_new_integrity_violation"}},
		Conditions:       []schemas.ConditionSpec{cond},
		Verdict:          schemas.VerdictSpec{Policy: "paired_signflip", Alpha: 0.05, AssumedSD: 0.30},
		Seed:             1,
		Concurrency:      4,
		ManifestMode:     "agent_exposure",
		Smoke:            true,
		ScriptedUpstream: true,
		PaidJudge:        paidJudge,
	}
}

// readinessGroups splits tasks by split: a condition runs one split only,
// so held-out tasks need their own experiment. Public first.
func readinessGroups(tasks []*schemas.Task) [][]*schemas.Task {
	var pub, held []*schemas.Task
	for _, t := range tasks {
		if t.Split == schemas.SplitHeldOut {
			held = append(held, t)
		} else {
			pub = append(pub, t)
		}
	}
	var out [][]*schemas.Task
	for _, g := range [][]*schemas.Task{pub, held} {
		if len(g) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// readinessGot is a run outcome as an admission check result.
func readinessGot(outcome string) string {
	switch schemas.Outcome(outcome) {
	case schemas.OutcomePass:
		return suite.GotPass
	case schemas.OutcomeInvalidEvaluator:
		return suite.GotInvalidEvaluator
	case schemas.OutcomeInfraError, schemas.OutcomeAbortedInfra, schemas.OutcomeUnknown, "":
		return suite.GotError
	}
	return suite.GotFail
}

// readinessVerdict builds each task's checks from the runs of its
// experiments (runs of every group, keyed by task and arm). fileChecks adds
// the in-process file checks of a task.
func readinessVerdict(s *schemas.Suite, tasks []*schemas.Task, runs []bridgeRun, expOf map[string]string, fileChecks func(*schemas.Task) []schemas.AdmissionCheck) []hostedAdmitTask {
	byKey := map[string]bridgeRun{}
	for _, r := range runs {
		byKey[r.Task+"\x00"+r.Arm] = r
	}
	out := make([]hostedAdmitTask, 0, len(tasks))
	for _, t := range tasks {
		ht := hostedAdmitTask{Task: t.ID, Admitted: true}
		where := "on a worker in experiment " + expOf[t.ID]
		for _, c := range readinessTaskChecks(s, t) {
			chk := schemas.AdmissionCheck{Name: c.Name, Want: c.Want}
			switch r, ok := byKey[t.ID+"\x00"+c.Arm]; {
			case c.NotRun != "":
				chk.Got, chk.Detail = readinessNotRun, c.NotRun
			case !ok:
				chk.Got, chk.Detail = suite.GotError, "the run did not finish; see the experiment ("+where+")"
			default:
				chk.Got = readinessGot(r.Outcome)
				chk.Passed = chk.Got == chk.Want
				chk.Detail = "outcome " + r.Outcome + " " + where
				if r.RunID != "" {
					chk.Detail += ", run " + r.RunID
				}
				if r.Detail != "" && !chk.Passed {
					chk.Detail += " (" + r.Detail + ")"
				}
			}
			switch c.Name {
			case "reference_1":
				ht.Oracle = rawOutcome(byKey, t.ID, c.Arm)
			case "null_noop":
				ht.Noop = rawOutcome(byKey, t.ID, c.Arm)
			}
			if strings.HasPrefix(c.Name, "reference_") {
				ht.ReferenceResults = append(ht.ReferenceResults, chk.Got)
			}
			ht.Checks = append(ht.Checks, chk)
		}
		if fileChecks != nil {
			ht.Checks = append(ht.Checks, fileChecks(t)...)
		}
		for _, c := range ht.Checks {
			if !c.Passed {
				ht.Admitted = false
				if ht.Reason == "" {
					ht.Reason = readinessReason(c)
				}
			}
		}
		out = append(out, ht)
	}
	return out
}

func rawOutcome(byKey map[string]bridgeRun, task, arm string) string {
	return byKey[task+"\x00"+arm].Outcome
}

// readinessReason names the first failing check in plain words.
func readinessReason(c schemas.AdmissionCheck) string {
	what := map[string]string{
		"empty_control": "the empty answer", "empty_patch": "the empty answer", "exploit": "the gaming attempt",
		"null_noop": "doing nothing", "null_refuse": "refusing",
	}[c.Name]
	switch {
	case c.Got == readinessNotRun:
		return c.Name + " was " + c.Detail
	case strings.HasPrefix(c.Name, "reference_"):
		what = "the right answer (" + strings.ReplaceAll(c.Name, "_", " ") + ")"
	case strings.HasPrefix(c.Name, "mutant_"):
		what = "near miss " + strings.TrimPrefix(c.Name, "mutant_")
	case strings.HasPrefix(c.Name, "wrong_answer_"):
		what = "wrong answer " + strings.TrimPrefix(c.Name, "wrong_answer_")
	case what == "":
		return c.Name + ": got " + c.Got + ", wanted " + c.Want + detailSuffix(c.Detail)
	}
	switch {
	case c.Got == suite.GotInvalidEvaluator:
		return what + " could not be graded (invalid_evaluator)" + detailSuffix(c.Detail)
	case c.Got == suite.GotError:
		return what + " did not run to the end" + detailSuffix(c.Detail)
	case c.Want == suite.GotPass:
		return what + " did not pass: the reference answer or the check is broken" + detailSuffix(c.Detail)
	}
	return what + " passed: the check accepts an answer it should fail" + detailSuffix(c.Detail)
}

func detailSuffix(d string) string {
	if d == "" {
		return ""
	}
	return " (" + d + ")"
}

// readinessJudge is what a judged benchmark's readiness check asks of the
// judge: whether the judge grades its controls and about how many calls.
type readinessJudge struct {
	Judged   bool   `json:"judged"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Calls    int    `json:"calls,omitempty"`
}

// readinessJudgeOf is the judge use of a readiness check of tasks.
func readinessJudgeOf(s *schemas.Suite, tasks []*schemas.Task) readinessJudge {
	if !graders.JudgedSuite(s) {
		return readinessJudge{}
	}
	j := readinessJudge{Judged: true, Provider: s.Judge.Provider, Model: s.Judge.Model}
	for _, t := range tasks {
		j.Calls += graders.ReadinessJudgeCalls(s, t, readinessRefRuns)
	}
	return j
}

// readinessTimeout is the default wait of a readiness check: the bridge
// default plus a little per slot, at most the bridge maximum.
func readinessTimeout(slots int) time.Duration {
	return min(bridgeDefaultTimeout+time.Duration(slots)*readinessSlotTimeout, bridgeMaxTimeout)
}

// admissionRecordOf is the record of one task's hosted readiness check.
func admissionRecordOf(s *schemas.Suite, t hostedAdmitTask) *schemas.AdmissionRecord {
	rec := &schemas.AdmissionRecord{
		Admitted:         t.Admitted,
		At:               time.Now().UTC(),
		Grader:           s.Grader,
		Checks:           append([]schemas.AdmissionCheck{{Name: suite.StaticValidationCheck, Want: "clean", Got: "clean", Passed: true}}, t.Checks...),
		ReferenceRepeats: readinessRefRuns,
		ReferenceResults: t.ReferenceResults,
	}
	for _, c := range t.Checks {
		if !c.Passed {
			rec.Admitted = false
		}
	}
	return rec
}
