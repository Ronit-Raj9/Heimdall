package handlers

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// supportIndex is a two-arm support experiment under an llm user (W3): A
// resolves 1 of 2 chats, B 2 of 2; one run of B escalated.
func supportIndex() *fakeIndex {
	m := &schemas.Manifest{ExpID: "e_sp", SuiteID: "acme-helpdesk", SuiteVersion: "1.0.0",
		Arms:      map[string]schemas.ArmManifest{"A": {Label: "baseline"}, "B": {Label: "candidate"}},
		Simulator: &schemas.SimulatorSpec{Mode: schemas.SimulatorLLM, Provider: "anthropic", Model: "claude-sim"}}
	obs := func(v float64) string { return fmt.Sprintf(`{"state":"observed","v":%g,"evidence":"outcome"}`, v) }
	metrics := func(ok, cost, esc, pol, turns, sim float64) []byte {
		return []byte(`{"verified_success":` + obs(ok) + `,"cost_usd":` + obs(cost) + `,"escalated":` + obs(esc) +
			`,"escalation_correct":{"state":"not_applicable"},"policy_violation":` + obs(pol) + `,"user_turns":` + obs(turns) + `,"simulator_cost_usd":` + obs(sim) + `}`)
	}
	run := func(id, arm, cond string) RunView {
		return RunView{Slot: schemas.Slot{SlotID: "s_" + id, ExpID: "e_sp", Arm: arm, Condition: cond, Counted: true},
			Run: schemas.Run{RunID: id, ExpID: "e_sp", Attempt: 1}, Sealed: true}
	}
	files := map[string]map[string][]byte{
		"r_a1": {FileMetrics: metrics(1, 0.10, 0, 0, 3, 0.01)},
		"r_a2": {FileMetrics: metrics(0, 0.10, 0, 1, 5, 0.01)},
		"r_b1": {FileMetrics: metrics(1, 0.20, 1, 0, 2, 0.02)},
		"r_b2": {FileMetrics: metrics(1, 0.20, 0, 0, 4, 0.02)},
		"r_bx": {FileMetrics: metrics(0, 9, 0, 1, 9, 9)},
	}
	return &fakeIndex{manifests: map[string]*schemas.Manifest{"e_sp": m}, files: files,
		runs: []RunView{run("r_a1", "A", "clean"), run("r_a2", "A", "clean"), run("r_b1", "B", "clean"), run("r_b2", "B", "clean"), run("r_bx", "B", "hostile-customer")}}
}

func TestExperimentSupport(t *testing.T) {
	idx := supportIndex()
	out, err := ExperimentSupport(idx, idx.manifests["e_sp"], "clean")
	if err != nil {
		t.Fatal(err)
	}
	if !out.Support || out.SimulatorLabel != "simulated user (model: anthropic/claude-sim)" || len(out.Arms) != 2 {
		t.Fatalf("summary %+v", out)
	}
	a, b := out.Arms[0], out.Arms[1]
	near := func(p *float64, want float64) bool { return p != nil && *p > want-1e-9 && *p < want+1e-9 }
	if a.Resolved != 1 || a.Decided != 2 || !near(a.Resolution, 0.5) || !near(a.CostPerResolution, 0.2) || !near(a.PolicyViolationRate, 0.5) {
		t.Errorf("arm A %+v", a)
	}
	if !near(b.Resolution, 1) || !near(b.CostPerResolution, 0.2) || !near(b.EscalationRate, 0.5) || !near(b.UserTurns, 3) ||
		!near(b.SimulatorCostUSD, 0.04) || b.EscalationCorrect != nil || b.Runs != 2 {
		t.Errorf("arm B %+v", b)
	}
}

func TestSupportRoute(t *testing.T) {
	h := NewSupportHandler(testMode, supportIndex())
	var ctx fasthttp.RequestCtx
	ctx.SetUserValue("id", "e_sp")
	h.getSupport(&ctx)
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil || ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	checkKeys(t, "SupportSummary", body, loadKeys(t, "SupportSummary"), true)
	for i, obj := range objectsAt(t, body, "arms[]") {
		checkKeys(t, fmt.Sprintf("arms #%d", i), obj, loadKeys(t, "SupportArm"), false)
	}
	var missing fasthttp.RequestCtx
	missing.SetUserValue("id", "e_missing")
	h.getSupport(&missing)
	if missing.Response.StatusCode() != 404 {
		t.Errorf("missing experiment status %d", missing.Response.StatusCode())
	}
}

func TestSupportNotApplicable(t *testing.T) {
	idx := &fakeIndex{manifests: map[string]*schemas.Manifest{"e_c": {ExpID: "e_c", Arms: map[string]schemas.ArmManifest{"A": {}}}}}
	out, err := ExperimentSupport(idx, idx.manifests["e_c"], "clean")
	if err != nil || out.Support || len(out.Arms) != 0 || out.SimulatorLabel != "scripted user" {
		t.Fatalf("coding experiment: %+v %v", out, err)
	}
}
