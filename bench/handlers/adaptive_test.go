package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
	"github.com/valyala/fasthttp"
)

// adaptiveAnalyzer returns an analysis carrying every W7 field.
type adaptiveAnalyzer struct{}

func (adaptiveAnalyzer) Analyze(_ context.Context, expID, _ string) (*schemas.Analysis, error) {
	p, cost := 0.04, 12.5
	return &schemas.Analysis{ExpID: expID, Verdict: schemas.Verdict{
		Decision: schemas.DecisionInconclusive,
		Adaptive: &schemas.AdaptiveReport{Rule: schemas.AdaptiveSequential, MaxSlots: 16, RequiredSlots: 8, RunSlots: 8, RunsSaved: 8,
			StopReason: "futility", StopLook: 1, Looks: 2, DecisionAlpha: 0.0056,
			Tasks:       []schemas.AdaptiveTaskState{{Condition: "clean", Task: "t01", Trials: 2, Max: 4, Reason: "concordant"}},
			LookResults: []schemas.AdaptiveLook{{Look: 1, Tasks: 4, Alpha: 0.0056, PLow: &p, PHigh: &p, Outcome: "futility", BestCaseP: &p}}},
		MoreEvidence: &schemas.MoreEvidence{TargetPP: 10, Tasks: 20, TasksTotal: 24, SD: 0.3, SDAssumed: true, Runs: 80, CostUSD: &cost,
			CostBasis: "mean cost", Command: "heimdall extend -exp e_ad -tasks 20", Combination: schemas.ExtensionExploratory},
	}}, nil
}

func TestAdaptiveRoute(t *testing.T) {
	m := &schemas.Manifest{ExpID: "e_ad", Tier: &schemas.TierPlan{Name: "pr", Tasks: []string{"t01", "t02"}},
		Experiment: schemas.Experiment{Tier: "pr", Trials: &schemas.TrialsPolicy{Mode: schemas.TrialsAdaptive, Rule: schemas.AdaptiveSequential, Looks: 2},
			Extends: &schemas.ExtendsRef{ExpID: "e_first", ManifestHash: "h", Kind: "tasks", Rule: schemas.ExtensionExploratory}},
		Adaptive: &schemas.AdaptivePlan{Rule: schemas.AdaptiveSequential, Conditions: []string{"clean"}, Metric: "verified_success",
			Looks: [][]string{{"t01"}, {"t02"}}, LookAlphas: []float64{0.0056, 0.0444}, Alpha: 0.05, Futility: "curtail", Optional: 8, Required: 8},
		Slots: []schemas.Slot{{SlotID: "s1", Counted: true}, {SlotID: "s2", Counted: true, Optional: true}}}
	h := NewAdaptiveHandler(testMode, &fakeIndex{manifests: map[string]*schemas.Manifest{"e_ad": m}}, adaptiveAnalyzer{})
	var ctx fasthttp.RequestCtx
	ctx.SetUserValue("id", "e_ad")
	h.getAdaptive(&ctx)
	var body map[string]any
	if err := json.Unmarshal(ctx.Response.Body(), &body); err != nil || ctx.Response.StatusCode() != 200 {
		t.Fatalf("status %d: %s", ctx.Response.StatusCode(), ctx.Response.Body())
	}
	checkKeys(t, "AdaptiveView", body, loadKeys(t, "AdaptiveView"), true)
	for path, typ := range map[string]string{"trials": "TrialsPolicy", "plan": "AdaptivePlan", "report": "AdaptiveReport",
		"report.tasks[]": "AdaptiveTaskState", "report.look_results[]": "AdaptiveLook", "more_evidence": "MoreEvidence", "extends": "ExtendsRef"} {
		for i, obj := range objectsAt(t, body, path) {
			checkKeys(t, fmt.Sprintf("%s #%d", path, i), obj, loadKeys(t, typ), false)
		}
	}
	if body["max_slots"].(float64) != 2 || body["tier"] != "pr" {
		t.Fatalf("view %v", body)
	}
	var missing fasthttp.RequestCtx
	missing.SetUserValue("id", "e_missing")
	h.getAdaptive(&missing)
	if missing.Response.StatusCode() != 404 {
		t.Errorf("missing experiment status %d", missing.Response.StatusCode())
	}
}

func TestAdaptiveOfFixed(t *testing.T) {
	v := AdaptiveOf(&schemas.Manifest{ExpID: "e_f"}, nil)
	if v.Plan != nil || v.Report != nil || v.Tier != "" || v.TierTasks == nil {
		t.Fatalf("fixed view %+v", v)
	}
}

// TestAdaptiveReportFromReplayMatchesContract marshals reports the real
// replay produces (both rules) and checks them against the key lists.
func TestAdaptiveReportFromReplayMatchesContract(t *testing.T) {
	for _, rule := range []string{schemas.AdaptiveTask, schemas.AdaptiveSequential} {
		m := &schemas.Manifest{ExpID: "e_r"}
		plan := &schemas.AdaptivePlan{Rule: rule, Conditions: []string{"clean"}, Metric: "verified_success", Alpha: 0.05, Futility: schemas.FutilityCurtail, Min: 1, Max: 2}
		if rule == schemas.AdaptiveSequential {
			plan.Looks, plan.LookAlphas = [][]string{{"t1"}, {"t2"}}, []float64{0.01, 0.04}
		}
		for _, task := range []string{"t1", "t2"} {
			for _, arm := range []string{"A", "B"} {
				for trial := 1; trial <= 2; trial++ {
					opt := (rule == schemas.AdaptiveTask && trial > 1) || (rule == schemas.AdaptiveSequential && task == "t2")
					m.Slots = append(m.Slots, schemas.Slot{SlotID: fmt.Sprintf("s_%s_%s_%d", task, arm, trial), Task: task, Arm: arm, Condition: "clean", Trial: trial, Counted: true, Optional: opt})
				}
			}
		}
		m.Adaptive = plan
		rep := verdict.ReplayAdaptive(m, nil)
		raw, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		checkKeys(t, rule, body, loadKeys(t, "AdaptiveReport"), false)
		for _, path := range []string{"tasks[]", "look_results[]"} {
			if arr, ok := body[strings.TrimSuffix(path, "[]")].([]any); ok && len(arr) > 0 {
				typ := map[string]string{"tasks[]": "AdaptiveTaskState", "look_results[]": "AdaptiveLook"}[path]
				for i, obj := range objectsAt(t, body, path) {
					checkKeys(t, fmt.Sprintf("%s %s #%d", rule, path, i), obj, loadKeys(t, typ), false)
				}
			}
		}
	}
}
