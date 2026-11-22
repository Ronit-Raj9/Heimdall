package handlers

import (
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func TestBuildStatus(t *testing.T) {
	idx := testIndex()
	m := idx.manifests[testExp]
	runs := idx.Runs(testExp)
	edit := func(f func(r []RunView) []RunView) []RunView {
		return f(append([]RunView(nil), runs...))
	}
	tests := []struct {
		name   string
		runs   []RunView
		active bool
		state  schemas.ExperimentState
		slots  schemas.SlotProgress
	}{
		{"no runs", nil, false, schemas.ExperimentCreatedState, schemas.SlotProgress{Total: 4}},
		{"accepted, no runs yet", nil, true, schemas.ExperimentRunning, schemas.SlotProgress{Total: 4}},
		{"all sealed", runs, false, schemas.ExperimentDone, schemas.SlotProgress{Total: 4, Done: 4}},
		{"all sealed, still analyzing", runs, true, schemas.ExperimentAnalyzing, schemas.SlotProgress{Total: 4, Done: 4}},
		{"one in flight", edit(func(r []RunView) []RunView {
			r[0].Sealed = false
			return r
		}), false, schemas.ExperimentRunning, schemas.SlotProgress{Total: 4, Done: 3, Running: 1}},
		{"infra failure is failed, not done", edit(func(r []RunView) []RunView {
			r[1].Run.Outcome = schemas.OutcomeInfraError
			return r
		}), false, schemas.ExperimentDone, schemas.SlotProgress{Total: 4, Done: 3, Failed: 1}},
		{"retry after aborted_infra in flight", edit(func(r []RunView) []RunView {
			r[0].Run.Outcome = schemas.OutcomeAbortedInfra
			retry := r[0]
			retry.Sealed, retry.Run.Attempt, retry.Run.RunID, retry.Run.Outcome = false, 2, "r_retry", ""
			return append(r, retry)
		}), false, schemas.ExperimentRunning, schemas.SlotProgress{Total: 4, Done: 3, Running: 1}},
		{"stopped with slots left", runs[:2], false, schemas.ExperimentFailed, schemas.SlotProgress{Total: 4, Done: 2}},
		{"stopped with slots left, launcher active", runs[:2], true, schemas.ExperimentRunning, schemas.SlotProgress{Total: 4, Done: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := BuildStatus(m, tt.runs, StatusFacts{Active: tt.active})
			if st.State != tt.state || st.Slots != tt.slots {
				t.Fatalf("state %s slots %+v, want %s %+v", st.State, st.Slots, tt.state, tt.slots)
			}
			if st.State != schemas.ExperimentDone && st.FinishedAt != nil {
				t.Errorf("finished_at %v with state %s", st.FinishedAt, st.State)
			}
		})
	}
	t.Run("analysis id passes through", func(t *testing.T) {
		if st := BuildStatus(m, runs, StatusFacts{AnalysisID: "a_1"}); st.AnalysisID != "a_1" {
			t.Fatalf("analysis_id = %q", st.AnalysisID)
		}
	})
}

func TestSpentUSD(t *testing.T) {
	sealed := func(id string, outcome schemas.Outcome) RunView {
		return RunView{Sealed: true, Run: schemas.Run{RunID: id, Outcome: outcome}}
	}
	obs := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }
	tests := []struct {
		name     string
		runs     []RunView
		costs    map[string]schemas.Value
		state    schemas.ValueState
		v        float64
		bounds   *[2]float64
		reason   string
		observed bool
	}{
		{name: "nothing sealed is zero spent", state: schemas.ValueObserved, observed: true},
		{name: "sum of observed", runs: []RunView{sealed("r1", schemas.OutcomePass), sealed("r2", schemas.OutcomeFail)},
			costs: map[string]schemas.Value{"r1": obs(0.5), "r2": obs(0.25)}, state: schemas.ValueObserved, v: 0.75, observed: true},
		{name: "unsealed runs are left out", runs: []RunView{sealed("r1", schemas.OutcomePass), {Run: schemas.Run{RunID: "r2"}}},
			costs: map[string]schemas.Value{"r1": obs(0.5)}, state: schemas.ValueObserved, v: 0.5, observed: true},
		{name: "aborted before the agent started spent nothing", runs: []RunView{sealed("r1", schemas.OutcomeAbortedInfra), sealed("r2", schemas.OutcomePass)},
			costs: map[string]schemas.Value{"r2": obs(1)}, state: schemas.ValueObserved, v: 1, observed: true},
		{name: "missing metrics is unknown", runs: []RunView{sealed("r1", schemas.OutcomePass), sealed("r2", schemas.OutcomeCrash)},
			costs: map[string]schemas.Value{"r1": obs(1)}, state: schemas.ValueUnknown, reason: "no_metrics"},
		{name: "bounded unknowns keep bounds", runs: []RunView{sealed("r1", schemas.OutcomePass), sealed("r2", schemas.OutcomePass)},
			costs: map[string]schemas.Value{"r1": obs(1), "r2": schemas.UnknownWithin("telemetry_incomplete", schemas.EvidenceGateway, 0.1, 0.4)},
			state: schemas.ValueUnknown, bounds: &[2]float64{1.1, 1.4}, reason: "telemetry_incomplete"},
		{name: "not applicable is left out", runs: []RunView{sealed("r1", schemas.OutcomePass)},
			costs: map[string]schemas.Value{"r1": schemas.NotApplicable()}, state: schemas.ValueObserved, observed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := spentUSD(tt.runs, tt.costs)
			if got.State != tt.state || got.Reason != tt.reason {
				t.Fatalf("spent = %+v", got)
			}
			if v, ok := got.Float(); ok != tt.observed || (ok && v != tt.v) {
				t.Errorf("v = %v %v, want %v", v, ok, tt.v)
			}
			if (got.Bounds == nil) != (tt.bounds == nil) || (got.Bounds != nil && *got.Bounds != *tt.bounds) {
				t.Errorf("bounds = %v, want %v", got.Bounds, tt.bounds)
			}
		})
	}
}

func TestPlanConditions(t *testing.T) {
	got := planConditions(testManifest())
	want := []schemas.PlanCondition{{Condition: "clean", Tasks: 2, Trials: 1, Slots: 4}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("planConditions = %+v, want %+v", got, want)
	}
}

// TestBuildStatusAdaptiveStoppedEarly: QC2-27-01. A task-rule adaptive plan
// whose required trials agree never releases its optional trials. Once the
// launcher stops, the experiment is done, not failed with slots left.
func TestBuildStatusAdaptiveStoppedEarly(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_ad", Adaptive: &schemas.AdaptivePlan{Rule: schemas.AdaptiveTask, Conditions: []string{"clean"},
		Metric: "verified_success", Alpha: 0.05, Futility: schemas.FutilityCurtail, Min: 1, Max: 2}}
	var runs []RunView
	metrics := map[string]runMetricsRead{}
	for _, task := range []string{"t1", "t2"} {
		for _, arm := range []string{"A", "B"} {
			for trial := 1; trial <= 2; trial++ {
				sl := schemas.Slot{SlotID: fmt.Sprintf("s_%s_%s_%d", task, arm, trial), ExpID: "e_ad", Task: task, Arm: arm,
					Condition: "clean", Trial: trial, Counted: true, Optional: trial > 1}
				m.Slots = append(m.Slots, sl)
				if trial == 1 {
					id := "r_" + sl.SlotID
					runs = append(runs, RunView{Slot: sl, Sealed: true, Run: schemas.Run{RunID: id, SlotID: sl.SlotID, ExpID: "e_ad", Attempt: 1, Outcome: schemas.OutcomePass}})
					metrics[id] = runMetricsRead{found: true, values: map[string]schemas.Value{"verified_success": schemas.Observed(1, schemas.EvidenceOutcome)}}
				}
			}
		}
	}
	skip := notRequiredSlots(m, runs, metrics)
	if len(skip) != 4 {
		t.Fatalf("not required %v, want the 4 optional trial-2 slots", skip)
	}
	st := BuildStatus(m, runs, StatusFacts{NotRequired: skip})
	if st.State != schemas.ExperimentDone || st.Slots != (schemas.SlotProgress{Total: 4, Done: 4}) {
		t.Fatalf("state %s slots %+v, want done 4/4", st.State, st.Slots)
	}
	if st := BuildStatus(m, runs, StatusFacts{}); st.State != schemas.ExperimentFailed {
		t.Fatalf("without the replay the stop reads %s, the case this guards", st.State)
	}
	if notRequiredSlots(&schemas.Manifest{ExpID: "e_fixed"}, runs, metrics) != nil {
		t.Fatal("fixed plan has no not-required slots")
	}
}

// An experiment an evaluation created says so, so the lists can tell its
// single benchmark runs from comparisons a person started (D1.text).
func TestBuildStatusEvaluationID(t *testing.T) {
	idx := testIndex()
	m := *idx.manifests[testExp]
	if st := BuildStatus(&m, nil, StatusFacts{}); st.EvaluationID != "" {
		t.Fatalf("evaluation_id %q on a plain experiment", st.EvaluationID)
	}
	m.Experiment.EvaluationID = "ev_20261122_142833_4eaebd"
	if st := BuildStatus(&m, nil, StatusFacts{}); st.EvaluationID != "ev_20261122_142833_4eaebd" {
		t.Fatalf("evaluation_id %q, want the manifest's evaluation", st.EvaluationID)
	}
}

// A replay, fork or step run says which debug mode made it and from which
// run, so lists can hide it and tag it (N29, S-A1).
func TestBuildStatusCarriesDebugMode(t *testing.T) {
	m := *testIndex().manifests[testExp]
	if st := BuildStatus(&m, nil, StatusFacts{}); st.Debug != "" || st.DebugFromRun != "" {
		t.Fatalf("debug %q from %q on a plain experiment", st.Debug, st.DebugFromRun)
	}
	m.Experiment.Debug = &schemas.DebugSpec{Mode: "replay", FromRun: "r_1"}
	if st := BuildStatus(&m, nil, StatusFacts{}); st.Debug != "replay" || st.DebugFromRun != "r_1" {
		t.Fatalf("debug %q from %q, want replay from r_1", st.Debug, st.DebugFromRun)
	}
}

// multi_agent is set only when an arm runs a team or a multi-agent
// topology, so Compute blame is offered only there (N63).
func TestBuildStatusMultiAgent(t *testing.T) {
	m := *testIndex().manifests[testExp]
	if st := BuildStatus(&m, nil, StatusFacts{}); st.MultiAgent {
		t.Fatal("single-agent experiment reads multi_agent")
	}
	arms := map[string]schemas.ArmManifest{"A": m.Arms["A"], "B": m.Arms["B"]}
	b := arms["B"]
	b.Config.Topology = "single"
	arms["B"] = b
	m.Arms = arms
	if st := BuildStatus(&m, nil, StatusFacts{}); st.MultiAgent {
		t.Fatal("topology single reads multi_agent")
	}
	b.Config.Team = []schemas.TeamMember{{ID: "lead"}, {ID: "worker"}}
	arms["B"] = b
	if st := BuildStatus(&m, nil, StatusFacts{}); !st.MultiAgent {
		t.Fatal("team arm does not read multi_agent")
	}
	b.Config.Team, b.Config.Topology = nil, "orchestrator_worker"
	arms["B"] = b
	if st := BuildStatus(&m, nil, StatusFacts{}); !st.MultiAgent {
		t.Fatal("orchestrator_worker topology does not read multi_agent")
	}
}

// setups names each arm's agent, agent file and model, sorted by arm, so the
// Actions forms start from the comparison's own setup (N66).
func TestBuildStatusSetups(t *testing.T) {
	m := *testIndex().manifests[testExp]
	m.Experiment.Base = "agents/codex.yaml"
	m.Experiment.Arms = map[string]schemas.ArmSpec{"A": {Label: "a"}, "B": {Label: "b", Agent: "agents/claude.yaml"}}
	m.Arms = map[string]schemas.ArmManifest{
		"B": {Config: schemas.AgentConfig{ID: "claude-code", Model: "claude-x"}},
		"A": {Config: schemas.AgentConfig{ID: "codex", Model: "gpt-6-luna"}},
	}
	st := BuildStatus(&m, nil, StatusFacts{})
	want := []schemas.ArmSetup{
		{Arm: "A", Agent: "codex", AgentFile: "agents/codex.yaml", Model: "gpt-6-luna"},
		{Arm: "B", Agent: "claude-code", AgentFile: "agents/claude.yaml", Model: "claude-x"},
	}
	if fmt.Sprint(st.Setups) != fmt.Sprint(want) {
		t.Fatalf("setups %+v, want %+v", st.Setups, want)
	}
}

// arms_differ: two arms of the same agent version on different models read
// model, on the same model none (A/A); a changed agent reads agent (N165).
func TestStatusArmsDifferModel(t *testing.T) {
	m := *testIndex().manifests[testExp]
	cfg := schemas.AgentConfig{ID: "codex", Image: "img", Model: "gpt-6-luna"}
	other := cfg
	other.Model = "gpt-6-sol"
	m.Arms = map[string]schemas.ArmManifest{"A": {Config: cfg}, "B": {Config: other}}
	if st := BuildStatus(&m, nil, StatusFacts{}); st.ArmsDiffer != "model" {
		t.Fatalf("arms_differ %q, want model", st.ArmsDiffer)
	}
	other.Model, other.Gateway.Provider = cfg.Model, "openrouter"
	m.Arms = map[string]schemas.ArmManifest{"A": {Config: cfg}, "B": {Config: other}}
	if st := BuildStatus(&m, nil, StatusFacts{}); st.ArmsDiffer != "model" {
		t.Fatalf("provider change: arms_differ %q, want model", st.ArmsDiffer)
	}
	m.Arms = map[string]schemas.ArmManifest{"A": {Config: cfg}, "B": {Config: cfg}}
	if st := BuildStatus(&m, nil, StatusFacts{}); st.ArmsDiffer != "none" {
		t.Fatalf("A/A: arms_differ %q, want none", st.ArmsDiffer)
	}
	m.Arms = map[string]schemas.ArmManifest{"A": {Config: cfg}}
	if st := BuildStatus(&m, nil, StatusFacts{}); st.ArmsDiffer != "" {
		t.Fatalf("one arm: arms_differ %q, want empty", st.ArmsDiffer)
	}
}

func TestStatusArmsDifferAgent(t *testing.T) {
	m := *testIndex().manifests[testExp]
	cfg := schemas.AgentConfig{ID: "codex", Image: "img", Model: "gpt-6-luna"}
	other := cfg
	other.Image = "img2"
	m.Arms = map[string]schemas.ArmManifest{"A": {Config: cfg}, "B": {Config: other}}
	if st := BuildStatus(&m, nil, StatusFacts{}); st.ArmsDiffer != "agent" {
		t.Fatalf("arms_differ %q, want agent", st.ArmsDiffer)
	}
}

// With no runs, an ended run job and a run problem (every worker refused,
// or no slot could start) the comparison reads failed with the problem, not
// "created" with "Waiting for the first run to start" (N151).
func TestStatusFailedWithRunProblem(t *testing.T) {
	m := testIndex().manifests[testExp]
	st := BuildStatus(m, nil, StatusFacts{Problem: "The worker's copy of your project does not have this benchmark yet.", RunJobID: "job_1"})
	if st.State != schemas.ExperimentFailed || st.Problem == "" || st.RunJobID != "job_1" {
		t.Fatalf("state %s problem %q job %q, want failed with the problem", st.State, st.Problem, st.RunJobID)
	}
	if st := BuildStatus(m, nil, StatusFacts{Problem: "x", Active: true}); st.State != schemas.ExperimentRunning {
		t.Fatalf("active job: state %s, want running", st.State)
	}
	if st := BuildStatus(m, nil, StatusFacts{Problem: "x", Cancelled: true}); st.State != schemas.ExperimentCancelled {
		t.Fatalf("cancelled job: state %s, want cancelled", st.State)
	}
	if st := BuildStatus(m, nil, StatusFacts{}); st.State != schemas.ExperimentCreatedState {
		t.Fatalf("no problem: state %s, want created", st.State)
	}
}
