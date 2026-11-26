package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	benchagents "github.com/maximhq/bifrost/bench/agents"
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/scaffold"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// writeJudgedRowSuite writes a judged question-and-answer benchmark "qa"
// under root the way the Build tab does: the qa template, the rubric judge
// pinned, then rows imported (one held out when heldOut).
func writeJudgedRowSuite(t *testing.T, root string, heldOut bool) string {
	t.Helper()
	files, err := scaffold.Suite(scaffold.SuiteOptions{ID: "qa", Template: scaffold.TemplateQA})
	if err != nil {
		t.Fatal(err)
	}
	if files, err = scaffold.BlankSuite(files); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "qa")
	if err := scaffold.WriteFiles(dir, files, false); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "suite.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if raw, err = scaffold.SetSuiteGrader(raw, scaffold.RowGraderJudge, &scaffold.JudgeConfig{Provider: "openai", Model: "gpt-x", Votes: 1}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := scaffold.ParseRows("csv", []byte("question,answer,split\n"+
		"How many days is the refund window?,The refund window is 30 days.,\n"+
		"Can I change my seat after check-in?,\"Yes, at the gate desk.\","+map[bool]string{true: "held_out", false: ""}[heldOut]+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, problems := scaffold.ImportRows(rows, scaffold.RowTaskOptions{Grader: scaffold.RowGraderJudge})
	if len(problems) > 0 || len(tasks) != 2 {
		t.Fatalf("import: %+v %+v", tasks, problems)
	}
	for _, tk := range tasks {
		if !tk.Generated || len(tk.WrongAnswers) == 0 {
			t.Fatalf("judged row %s has no generated wrong answers: %+v", tk.ID, tk)
		}
		if err := scaffold.WriteFiles(filepath.Join(dir, "tasks", tk.ID), tk.Files, false); err != nil {
			t.Fatal(err)
		}
		if raw, _, err = scaffold.AddTaskID(raw, tk.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "suite.yaml"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// readinessFixture loads the judged row benchmark.
func readinessFixture(t *testing.T) (*schemas.Suite, []*schemas.Task) {
	t.Helper()
	root := t.TempDir()
	writeJudgedRowSuite(t, root, false)
	s, err := loadSuite(context.Background(), root, "qa")
	if err != nil {
		t.Fatal(err)
	}
	return s, s.Tasks
}

// The arm set covers every control of the tasks, and every arm resolves to
// a valid scripted control config with its own hash input (kind and index).
func TestReadinessArmsResolve(t *testing.T) {
	s, tasks := readinessFixture(t)
	e := readinessExperiment("qa", bridgeOracleAgent, bridgeNoopAgent, s, tasks, schemas.SplitPublic, true)
	for _, id := range []string{"A", "B", "reference_2", "reference_3", "empty", "mutant_1", "wrong_answer_1", "exploit", "null_refuse"} {
		if _, ok := e.Arms[id]; !ok {
			t.Errorf("no arm %s: %v", id, e.Arms)
		}
	}
	if !e.PaidJudge || !e.NoPaidTraffic() || e.JudgeMayCall() != true {
		t.Fatalf("judged readiness must keep agents scripted and let the judge call: %+v", e)
	}
	base, err := benchagents.Files.ReadFile("oracle.yaml")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for id, a := range e.Arms {
		if a.Agent != "" {
			continue
		}
		cfg, err := engine.ResolveAgentConfig(base, a.Set)
		if err != nil {
			t.Fatalf("arm %s: %v", id, err)
		}
		key := cfg.Scripted.Kind + "/" + string(rune('0'+cfg.Scripted.Index))
		if prev, dup := seen[key]; dup {
			t.Fatalf("arms %s and %s resolve to the same control %s", prev, id, key)
		}
		seen[key] = id
	}
}

// Every check is named as local admission names it, a passing wrong answer
// refuses the task with a plain reason, and a check that cannot run is
// recorded as not run, never left out.
func TestReadinessVerdict(t *testing.T) {
	s, tasks := readinessFixture(t)
	e := readinessExperiment("qa", bridgeOracleAgent, bridgeNoopAgent, s, tasks, schemas.SplitPublic, true)
	var runs []bridgeRun
	for _, tk := range tasks {
		for arm := range e.Arms {
			o := "fail"
			if isReferenceArm(arm) {
				o = "pass"
			}
			if tk.ID == tasks[1].ID && arm == "wrong_answer_1" {
				o = "pass" // a judge that passes a wrong answer
			}
			runs = append(runs, bridgeRun{Task: tk.ID, Arm: arm, Outcome: o, RunID: "r-" + arm})
		}
	}
	got := readinessVerdict(s, tasks, runs, map[string]string{tasks[0].ID: "e_1", tasks[1].ID: "e_1"}, func(*schemas.Task) []schemas.AdmissionCheck {
		return []schemas.AdmissionCheck{{Name: "secret_scan", Want: "none", Got: "none", Passed: true}}
	})
	if !got[0].Admitted || got[1].Admitted {
		t.Fatalf("verdict %+v", got)
	}
	if !strings.Contains(got[1].Reason, "wrong answer 1 passed") {
		t.Fatalf("reason %q", got[1].Reason)
	}
	names := map[string]bool{}
	for _, c := range got[0].Checks {
		names[c.Name] = true
	}
	for _, n := range []string{"reference_1", "reference_2", "reference_3", "empty_control", "mutant_1", "wrong_answer_1", "exploit", "null_noop", "null_refuse", "secret_scan"} {
		if !names[n] {
			t.Errorf("no %s check: %+v", n, got[0].Checks)
		}
	}
	rec := admissionRecordOf(s, got[0])
	if !rec.Admitted || rec.ReferenceRepeats != 3 || len(rec.ReferenceResults) != 3 || rec.Checks[0].Name != suite.StaticValidationCheck {
		t.Fatalf("record %+v", rec)
	}

	// No runs at all: every control check is an error, not a pass.
	none := readinessVerdict(s, tasks[:1], nil, nil, nil)
	if none[0].Admitted || !strings.Contains(none[0].Reason, "did not run to the end") {
		t.Fatalf("no runs: %+v", none[0])
	}

	// More mutants than the worker checks: the extra ones are not run.
	big := *tasks[0]
	for i := 0; i < readinessMaxIndexed+1; i++ {
		big.Controls.Mutants = append(big.Controls.Mutants, big.Controls.Mutants[0])
	}
	var notRun int
	for _, c := range readinessTaskChecks(s, &big) {
		if c.NotRun != "" {
			notRun++
		}
	}
	if notRun != 2 {
		t.Fatalf("not run checks %d, want 2", notRun)
	}
}

// The judge call count is the reference runs plus every near miss and wrong
// answer the guard lets through, times the votes.
func TestReadinessJudgeCalls(t *testing.T) {
	s, tasks := readinessFixture(t)
	j := readinessJudgeOf(s, tasks)
	if !j.Judged || j.Provider != "openai" || j.Model != "gpt-x" {
		t.Fatalf("judge %+v", j)
	}
	// Per task: 3 reference runs, 1 mutant (the first wrong answer) and at
	// least one generated wrong answer, all reaching the judge; 1 vote.
	if j.Calls < 2*5 {
		t.Fatalf("calls %d, want at least 10", j.Calls)
	}
	plain := *s
	plain.Grading, plain.Judge = "", nil
	if readinessJudgeOf(&plain, tasks).Judged {
		t.Fatal("an exact-answer benchmark has no judge calls")
	}
}

// A judged benchmark's hosted check needs the consent to the judge calls;
// with it, the judge is paid and every split runs (held-out tasks in their
// own experiment).
func TestHostedSuiteAdmitJudgedConsent(t *testing.T) {
	initQuickRepo(t)
	env, l := hostedBridgeEnv(t, schemas.OutcomePass, schemas.OutcomeFail)
	writeJudgedRowSuite(t, env.App.cfg.Path(env.App.cfg.BenchmarksRoot), true)
	raw, _ := json.Marshal(hostedSuiteAdmitParams{Suite: "qa"})
	_, err := runHostedSuiteAdmit(context.Background(), env, &jobs.Job{Params: raw}, &bridgeTestLog{}, nil)
	// The refusal names the consent box in plain words (wave 4, I16), not the param.
	if err == nil || !strings.Contains(err.Error(), "Tick the box that agrees to the judge calls") || len(l.planned) != 0 {
		t.Fatalf("no consent: err %v, planned %d", err, len(l.planned))
	}
	raw, _ = json.Marshal(hostedSuiteAdmitParams{Suite: "qa", JudgeConsent: true})
	out, err := runHostedSuiteAdmit(context.Background(), env, &jobs.Job{Params: raw}, &bridgeTestLog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rep := out.(*hostedAdmitReport)
	if rep.Admitted != 2 || len(rep.ExpIDs) != 2 || rep.Judge == nil || rep.Judge.Calls == 0 {
		t.Fatalf("report %+v", rep)
	}
	if len(l.planned) != 2 || !l.planned[0].PaidJudge || l.planned[1].Conditions[0].Split != schemas.SplitHeldOut {
		t.Fatalf("planned %+v", l.planned)
	}
	s, err := loadSuite(context.Background(), env.App.cfg.Path(env.App.cfg.BenchmarksRoot), "qa")
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range s.Tasks {
		if !admittedIn(s.Lock, tk.ID) {
			t.Fatalf("%s not ready: %+v", tk.ID, s.Lock.Tasks[tk.ID].Admission)
		}
	}
}
