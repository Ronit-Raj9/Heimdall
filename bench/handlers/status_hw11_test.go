package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
)

// A debug experiment's run is never counted (engine/planner.go), yet its
// status reads done with its run once it is sealed, and a stored analysis
// decision is never shown for it (fix wave 3, N247).
func TestStatusDebugCountsUncountedRun(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_fork", Experiment: schemas.Experiment{
		Debug: &schemas.DebugSpec{Mode: schemas.DebugFork, FromRun: "r_src", Call: 1},
	}, Slots: []schemas.Slot{{SlotID: "s_1", ExpID: "e_fork", Task: "t01", Arm: "A", Condition: "clean", Trial: 1, Counted: false}}}
	run := RunView{Slot: m.Slots[0], Sealed: true, Run: schemas.Run{RunID: "r_fork", SlotID: "s_1", ExpID: "e_fork", Attempt: 1,
		Outcome: schemas.OutcomePass, Stage: schemas.StageSeal}}
	st := BuildStatus(m, []RunView{run}, StatusFacts{})
	if st.State != schemas.ExperimentDone || st.Slots.Total != 1 || st.Slots.Done != 1 {
		t.Fatalf("debug status %s %+v, want done 1 of 1", st.State, st.Slots)
	}
	// An aborted_infra seal of a debug run is its one failed run.
	run.Run.Outcome = schemas.OutcomeAbortedInfra
	if st := BuildStatus(m, []RunView{run}, StatusFacts{}); st.State != schemas.ExperimentDone || st.Slots.Failed != 1 {
		t.Fatalf("aborted debug status %s %+v, want done with 1 failed", st.State, st.Slots)
	}
	// The list never carries the stale "invalid" of a debug experiment.
	idx := &decisionIndex{fakeIndex: &fakeIndex{manifests: map[string]*schemas.Manifest{"e_fork": m, testExp: testManifest()}}, decision: schemas.DecisionInvalid}
	d := fullDeps()
	d.index = idx
	var list schemas.ExperimentList
	if err := json.Unmarshal(newTestClient(t, d).do(t, "GET", "/api/bench/experiments", nil, nil).Body(), &list); err != nil {
		t.Fatal(err)
	}
	for _, e := range list.Experiments {
		switch {
		case e.ExpID == "e_fork" && e.Decision != "":
			t.Fatalf("debug experiment listed with decision %q", e.Decision)
		case e.ExpID == testExp && e.Decision != schemas.DecisionInvalid:
			t.Fatalf("plain experiment lost its decision: %q", e.Decision)
		}
	}
}

// decisionIndex is a fakeIndex whose every experiment has a stored decision.
type decisionIndex struct {
	*fakeIndex
	decision schemas.Decision
}

func (d *decisionIndex) LatestDecision(string) (schemas.Decision, bool) { return d.decision, true }

// A follow-up's status names the experiment it extends, so its page can
// link back (fix wave 3, N246).
func TestStatusExtends(t *testing.T) {
	m := testManifest()
	plain, _ := json.Marshal(BuildStatus(m, nil, StatusFacts{}))
	if strings.Contains(string(plain), `"extends"`) {
		t.Fatalf("extends on a plain experiment: %s", plain)
	}
	m.Experiment.Extends = &schemas.ExtendsRef{ExpID: "e_parent", Kind: "trials", Rule: "exploratory"}
	raw, err := json.Marshal(BuildStatus(m, nil, StatusFacts{}))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if body["extends"] != "e_parent" {
		t.Fatalf("extends = %v, want e_parent", body["extends"])
	}
}

// While an experiment still runs, its verdict is partial: the coverage rule
// counts runs that have not run yet (fix wave 3, N197).
func TestGetVerdictPartialWhileRunning(t *testing.T) {
	idx := testIndex()
	idx.runs[0].Sealed = false // one run in flight: running
	d := fullDeps()
	d.index = idx
	body := decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/experiments/e_test/verdict", nil, nil))
	if body["partial"] != true {
		t.Fatalf("running verdict partial = %v, want true", body["partial"])
	}
	checkKeys(t, "VerdictPayload", body, loadKeys(t, "VerdictPayload"), true)
	done := decodeBody(t, newTestClient(t, fullDeps()).do(t, "GET", "/api/bench/experiments/e_test/verdict", nil, nil))
	if _, ok := done["partial"]; ok {
		t.Fatalf("finished verdict carries partial: %v", done["partial"])
	}
}

// hw11HeldLauncher is a fakeLauncher whose run controls hold a model.
type hw11HeldLauncher struct {
	fakeLauncher
	state *runctl.State
	reads int
}

func (p *hw11HeldLauncher) Controls(context.Context, string) (*runctl.State, []runctl.Entry, error) {
	p.reads++
	return p.state, nil, nil
}
func (p *hw11HeldLauncher) Pause(context.Context, string, string) (*runctl.State, error) {
	return p.state, nil
}
func (p *hw11HeldLauncher) Unpause(context.Context, string, string) (*runctl.State, bool, error) {
	return p.state, false, nil
}
func (p *hw11HeldLauncher) PatchControls(context.Context, string, runctl.Patch) (*runctl.State, error) {
	return p.state, nil
}
func (p *hw11HeldLauncher) CancelRun(context.Context, string, string, string, string) (*runctl.State, error) {
	return p.state, nil
}
func (p *hw11HeldLauncher) RequeueRun(context.Context, string, string, string) (*runctl.State, error) {
	return p.state, nil
}

// The status says a running experiment is paused and which models are
// held, from the run controls; the state stays running (fix wave 3, N199).
func TestStatusCarriesPausedFromControls(t *testing.T) {
	idx := testIndex()
	idx.runs[0].Sealed = false
	l := &hw11HeldLauncher{state: &runctl.State{Settings: runctl.Settings{Mode: runctl.ModePaused, PausedModels: []string{"gpt-5.4-mini"}}}}
	var _ RunOperator = l
	d := fullDeps()
	d.index, d.launcher = idx, l
	c := newTestClient(t, d)
	body := decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test", nil, nil))
	models, _ := body["paused_models"].([]any)
	if body["state"] != "running" || body["paused"] != true || len(models) != 1 || models[0] != "gpt-5.4-mini" {
		t.Fatalf("status = state %v paused %v models %v", body["state"], body["paused"], body["paused_models"])
	}
	checkKeys(t, "ExperimentStatus", body, loadKeys(t, "ExperimentStatus"), true)
	// Read at most once a second per experiment.
	_ = decodeBody(t, c.do(t, "GET", "/api/bench/experiments/e_test", nil, nil))
	if l.reads != 1 {
		t.Fatalf("controls read %d times, want 1 (cached)", l.reads)
	}
	// Only held models: not paused, but the models are named.
	l2 := &hw11HeldLauncher{state: &runctl.State{Settings: runctl.Settings{Mode: runctl.ModeRunning, PausedModels: []string{"m1"}}}}
	d.launcher = l2
	body = decodeBody(t, newTestClient(t, d).do(t, "GET", "/api/bench/experiments/e_test", nil, nil))
	if _, ok := body["paused"]; ok || body["paused_models"] == nil {
		t.Fatalf("held model only: paused %v models %v", body["paused"], body["paused_models"])
	}
}
