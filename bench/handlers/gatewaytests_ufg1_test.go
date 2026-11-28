package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/gwfeature"
	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schemas"
)

// g1Source is a run index with analyses: decided ids and invalid reasons.
type g1Source struct {
	manifests map[string]*schemas.Manifest
	runs      map[string][]leaderboard.Run
	decided   map[string]bool
	invalid   map[string][]string
}

func (s g1Source) Experiments() []string {
	out := []string{}
	for id := range s.manifests {
		out = append(out, id)
	}
	return out
}
func (s g1Source) Manifest(id string) (*schemas.Manifest, bool) {
	m, ok := s.manifests[id]
	return m, ok
}
func (s g1Source) Runs(id string) []leaderboard.Run { return s.runs[id] }
func (s g1Source) RunFile(runID, name string) ([]byte, error) {
	return nil, fmt.Errorf("%s: %w", name, fs.ErrNotExist)
}
func (s g1Source) LatestDecision(id string) (schemas.Decision, bool) {
	if _, bad := s.invalid[id]; bad {
		return schemas.DecisionInvalid, true
	}
	return "", s.decided[id]
}
func (s g1Source) LatestInvalid(id string) (string, []string, bool) {
	r, bad := s.invalid[id]
	return "experiment", r, bad
}

// g1Launcher says which comparisons run now and why none could start.
type g1Launcher struct {
	Launcher
	active  map[string]bool
	problem map[string]string
}

func (l g1Launcher) Active(id string) bool                 { return l.active[id] }
func (l g1Launcher) RunProblem(id string) (string, string) { return l.problem[id], "" }

// g1Arms is the fixed result the test feature computes, per comparison.
var g1Arms = map[string][]gwfeature.ArmResult{}

func g1Rate(v float64) *float64 { return &v }

// g1Manifest is a budget test with slots runs per setup A and B.
func g1Manifest(id string, slots int, at time.Time) *schemas.Manifest {
	m := &schemas.Manifest{ExpID: id, CreatedAt: at, Arms: map[string]schemas.ArmManifest{"A": {Label: "today"}, "B": {Label: "changed"}}}
	m.Experiment.GatewayTest = &schemas.GatewayTest{Feature: schemas.GatewayFeatureBudgetCurve, Control: "A", Variants: []string{"B"}}
	for i := range slots {
		for _, arm := range []string{"A", "B"} {
			m.Slots = append(m.Slots, schemas.Slot{SlotID: fmt.Sprintf("%s-%s-%d", id, arm, i), Task: fmt.Sprintf("t%d", i), Arm: arm, Condition: schemas.ConditionClean, Trial: 1})
		}
	}
	return m
}

// g1Runs returns n sealed runs (and live unsealed ones) of a manifest.
func g1Runs(m *schemas.Manifest, sealed, live int) []leaderboard.Run {
	var out []leaderboard.Run
	for i, sl := range m.Slots {
		switch {
		case i < sealed:
			out = append(out, leaderboard.Run{Slot: sl, Run: schemas.Run{RunID: sl.SlotID + "-r", Attempt: 1}, Sealed: true})
		case i < sealed+live:
			out = append(out, leaderboard.Run{Slot: sl, Run: schemas.Run{RunID: sl.SlotID + "-r", Attempt: 1}})
		}
	}
	return out
}

func g1Feature(t *testing.T) {
	t.Helper()
	t.Cleanup(gwfeature.Replace(gwfeature.Feature{ID: schemas.GatewayFeatureBudgetCurve, Name: "Budgets", Compute: func(_ context.Context, in gwfeature.Input) gwfeature.Effect {
		return gwfeature.Effect{State: gwfeature.StateMeasured, Result: "saved", Headline: "Best value: 0.01 USD.", Arms: g1Arms[in.ExpID]}
	}}))
}

func g1Arm(arm string, runs int, rate float64) gwfeature.ArmResult {
	return gwfeature.ArmResult{Arm: arm, Label: arm, Runs: runs, Tasks: runs, SolveRate: g1Rate(rate)}
}

// TestGatewayEffectStatesThroughHandler reads every feature test state
// through GET /experiments/{id}/gateway-effect, the answer the page reads.
func TestGatewayEffectStatesThroughHandler(t *testing.T) {
	g1Feature(t)
	at := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)
	src := g1Source{manifests: map[string]*schemas.Manifest{}, runs: map[string][]leaderboard.Run{}, decided: map[string]bool{}, invalid: map[string][]string{}}
	l := g1Launcher{active: map[string]bool{}, problem: map[string]string{}}
	add := func(id string, slots, sealed, live int) *schemas.Manifest {
		m := g1Manifest(id, slots, at)
		src.manifests[id], src.runs[id] = m, g1Runs(m, sealed, live)
		return m
	}
	// Running: 1 of 3 graded, the worker busy, a mid-run analysis says invalid (trials differ).
	add("e_running", 2, 1, 0)
	src.runs["e_running"] = src.runs["e_running"][:1]
	src.manifests["e_running"].Slots = src.manifests["e_running"].Slots[:3]
	l.active["e_running"] = true
	src.invalid["e_running"] = []string{"trials per arm differ on task t0"}
	g1Arms["e_running"] = []gwfeature.ArmResult{g1Arm("A", 1, 1)}
	// Refused before start: no run, the runner's reason.
	add("e_refused", 2, 0, 0)
	l.problem["e_refused"] = "the agent's image could not be built."
	// Broken after all runs: the analysis's reason.
	add("e_broken", 10, 20, 0)
	src.invalid["e_broken"] = []string{"the MCP log and the ledger disagree"}
	g1Arms["e_broken"] = []gwfeature.ArmResult{g1Arm("A", 10, 0.5), g1Arm("B", 10, 0.5)}
	// Nothing solved.
	add("e_none", 10, 20, 0)
	src.decided["e_none"] = true
	g1Arms["e_none"] = []gwfeature.ArmResult{g1Arm("A", 10, 0), g1Arm("B", 10, 0)}
	// Too few runs.
	add("e_few", 3, 6, 0)
	src.decided["e_few"] = true
	g1Arms["e_few"] = []gwfeature.ArmResult{g1Arm("A", 3, 0.5), g1Arm("B", 3, 0.5)}
	// A result.
	add("e_result", 10, 20, 0)
	src.decided["e_result"] = true
	g1Arms["e_result"] = []gwfeature.ArmResult{g1Arm("A", 10, 0.5), g1Arm("B", 10, 0.6)}

	env := OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode, Services: Services{Launcher: l}}
	h := gatewayTestsRouter(env, src)
	types := gwPageTypes(t)
	seen := map[string]any{}
	for _, tc := range []struct {
		id, state, result, headline string
	}{
		{"e_running", gwfeature.StateRunning, "", "Running, 1 of 3 runs graded."},
		{"e_refused", gwfeature.StateUnknown, "", "No result: no run started: the agent's image could not be built"},
		{"e_broken", gwfeature.StateNotApplied, "", "Broken: the MCP log and the ledger disagree. Fix it and run it again."},
		{"e_none", gwfeature.StateNotEnoughData, "", "No setup solved a task, so nothing can be recommended."},
		{"e_few", gwfeature.StateNotEnoughData, "", "Too few runs to tell. Nothing is recommended yet."},
		{"e_result", gwfeature.StateMeasured, "saved", "Best value: 0.01 USD."},
	} {
		st, out := secretsCall(h, "GET", APIPrefix+"/experiments/"+tc.id+"/gateway-effect", "")
		e := checkEffectShape(t, types, st, out)
		delete(e, "computed_at")
		seen[tc.id] = e
		res, _ := e["result"].(string)
		if e["state"] != tc.state || res != tc.result || e["headline"] != tc.headline {
			t.Errorf("%s: state %v result %q headline %q, want %s %q %q", tc.id, e["state"], res, e["headline"], tc.state, tc.result, tc.headline)
		}
		if tc.state != gwfeature.StateNotApplied && strings.Contains(fmt.Sprint(e["headline"], e["reason"]), "broken") {
			t.Errorf("%s says broken: %s", tc.id, out)
		}
	}
	// Without a launcher that knows, a started unsealed run still reads as running.
	add("e_live", 2, 1, 1)
	src.invalid["e_live"] = []string{"trials per arm differ on task t0"}
	g1Arms["e_live"] = []gwfeature.ArmResult{g1Arm("A", 1, 1)}
	bare := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: t.TempDir()}, Mode: testMode}, src)
	st, out := secretsCall(bare, "GET", APIPrefix+"/experiments/e_live/gateway-effect", "")
	if e := checkEffectShape(t, types, st, out); e["state"] != gwfeature.StateRunning || e["headline"] != "Running, 1 of 4 runs graded." {
		t.Errorf("live run without launcher: %s", out)
	}
	g1Fixture(t, "ufg1.effects.json", seen)
}

// g1Fixture keeps the handler's answers in the file the page tests read:
// it fails when the file differs, and UPDATE_GW_FIXTURES=1 rewrites it.
func g1Fixture(t *testing.T, name string, v any) {
	t.Helper()
	want, err := json.MarshalIndent(v, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	want = append(want, '\n')
	p := filepath.Join("..", "ui", "components", "gateway", "testdata", name)
	if os.Getenv("UPDATE_GW_FIXTURES") == "1" {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != string(want) {
		t.Fatalf("%s is not the handler's answer; run with UPDATE_GW_FIXTURES=1 (err %v)", p, err)
	}
}

// TestGatewayCardShowsNewestTestAndOlderResultOnlyWhenValid reads the
// report card: the newest test's state, an older result only with its date
// and only when it still holds.
func TestGatewayCardShowsNewestTestAndOlderResultOnlyWhenValid(t *testing.T) {
	g1Feature(t)
	old, newer := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC)
	card := func(oldArms []gwfeature.ArmResult) map[string]any {
		t.Helper()
		src := g1Source{manifests: map[string]*schemas.Manifest{}, runs: map[string][]leaderboard.Run{}, decided: map[string]bool{"e_old": true}, invalid: map[string][]string{}}
		mo := g1Manifest("e_old", 10, old)
		src.manifests["e_old"], src.runs["e_old"] = mo, g1Runs(mo, 20, 0)
		src.manifests["e_new"] = g1Manifest("e_new", 2, newer)
		g1Arms["e_old"] = oldArms
		root := t.TempDir()
		// A saved effect from before the rule change, newer on disk than any recompute.
		if err := gwfeature.Save(root, gwfeature.Effect{Feature: schemas.GatewayFeatureBudgetCurve, ExpID: "e_old", State: gwfeature.StateMeasured, Result: "saved",
			Headline: "Best value: 0.01 USD.", ComputedAt: time.Now().Add(time.Hour), Source: gwfeature.Source{Kind: "experiment", Text: "comparison e_old"}}); err != nil {
			t.Fatal(err)
		}
		l := g1Launcher{problem: map[string]string{"e_new": "the agent's image could not be built"}}
		h := gatewayTestsRouter(OrgEnv{Cfg: &lib.Config{Root: root}, Mode: testMode, Services: Services{Launcher: l}}, src)
		st, out := secretsCall(h, "GET", APIPrefix+"/gateway/report-card", "")
		c := gwShape(t, gwPageTypes(t), "GwReportCard", gwDecode(t, st, out), true)
		for _, raw := range c["lines"].([]any) {
			line := raw.(map[string]any)
			if line["feature"] == schemas.GatewayFeatureBudgetCurve {
				gwShape(t, gwPageTypes(t), "GwCardLine", line, false)
				return line
			}
		}
		t.Fatalf("no budget line: %s", out)
		return nil
	}
	lines := map[string]any{}
	line := card([]gwfeature.ArmResult{g1Arm("A", 10, 0.5), g1Arm("B", 10, 0.6)})
	lines["older_holds"] = line
	if line["exp_id"] != "e_new" || line["state"] != gwfeature.StateUnknown || line["result"] != nil ||
		line["headline"] != "No result: no run started: the agent's image could not be built" {
		t.Fatalf("line is not the newest test: %v", line)
	}
	o, _ := line["older"].(map[string]any)
	if o == nil || o["result"] != "saved" || o["exp_id"] != "e_old" || !strings.HasPrefix(fmt.Sprint(o["measured_at"]), "2026-11-12") {
		b, _ := json.Marshal(line)
		t.Fatalf("older result missing or without its date: %s", b)
	}
	// The older test solved nothing: under the current rules it has no result, so none is shown.
	line = card([]gwfeature.ArmResult{g1Arm("A", 10, 0), g1Arm("B", 10, 0)})
	if line["older"] != nil || line["result"] != nil {
		t.Fatalf("an older result that no longer holds is shown: %v", line)
	}
	lines["older_void"] = line
	g1Fixture(t, "ufg1.cardlines.json", lines)
}
