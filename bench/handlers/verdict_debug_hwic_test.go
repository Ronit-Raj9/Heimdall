package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// countAnalyzer is fakeAnalyzer that counts its calls.
type countAnalyzer struct {
	fakeAnalyzer
	calls int
}

func (c *countAnalyzer) Analyze(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, error) {
	c.calls++
	return c.fakeAnalyzer.Analyze(ctx, expID, gradeSource)
}

// GET /experiments/{id}/verdict of a debug run answers without running an
// analysis: a debug run is not scored (fix wave 3, N247 leftover).
func TestGetVerdictDebugRunNotAnalysed(t *testing.T) {
	idx := testIndex()
	idx.manifests[testExp].Experiment.Debug = &schemas.DebugSpec{Mode: schemas.DebugFork, FromRun: "r_t01_A", Call: 1}
	an := &countAnalyzer{}
	d := fullDeps()
	d.index, d.analyzer = idx, an
	res := newTestClient(t, d).do(t, "GET", "/api/bench/experiments/e_test/verdict", nil, nil)
	if res.StatusCode() != 404 || !strings.Contains(string(res.Body()), "Debug run (not scored)") {
		t.Fatalf("debug verdict: %d %s", res.StatusCode(), res.Body())
	}
	if an.calls != 0 {
		t.Fatalf("a debug run was analysed %d times", an.calls)
	}
}

// The experiment list shows a finished debug run as done with its run, not
// "created, 0 runs" (fix wave 3, N247 leftover): its one run is not counted
// in a verdict but counts as done.
func TestListDebugRunNotCreatedZeroRuns(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_fork", Experiment: schemas.Experiment{
		Debug: &schemas.DebugSpec{Mode: schemas.DebugFork, FromRun: "r_src", Call: 1},
	}, Slots: []schemas.Slot{{SlotID: "s_1", ExpID: "e_fork", Task: "t01", Arm: "A", Condition: "clean", Trial: 1, Counted: false}}}
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{"e_fork": m}, runs: []RunView{{Slot: m.Slots[0], Sealed: true,
		Run: schemas.Run{RunID: "r_fork", SlotID: "s_1", ExpID: "e_fork", Attempt: 1, Outcome: schemas.OutcomePass, Stage: schemas.StageSeal}}}}
	d := fullDeps()
	d.index = idx
	var list schemas.ExperimentList
	if err := json.Unmarshal(newTestClient(t, d).do(t, "GET", "/api/bench/experiments", nil, nil).Body(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Experiments) != 1 {
		t.Fatalf("list %+v", list)
	}
	e := list.Experiments[0]
	if e.State != schemas.ExperimentDone || e.Slots.Done != 1 || e.Slots.Total != 1 || e.Debug != schemas.DebugFork || e.Decision != "" {
		t.Fatalf("debug row %s %+v debug %q decision %q, want done 1 of 1 with no decision", e.State, e.Slots, e.Debug, e.Decision)
	}
}
