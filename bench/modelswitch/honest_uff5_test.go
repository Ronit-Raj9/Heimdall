package modelswitch

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// threeModels is the H4 run of round 3: three models on tasks, one trial
// set (two trials each in the fake), pass decides each run.
func threeModels(t *testing.T, tasks []string, pass func(arm, task string) bool) *Report {
	t.Helper()
	src := newSource()
	arms := []armSpec{
		{id: "A", provider: "openai", model: "gpt-5.4-mini", cost: ptr(0.0005), latency: 100},
		{id: "B", provider: "openai", model: "gpt-5.4-nano", cost: ptr(0.0003), latency: 100},
		{id: "C", provider: "openai", model: "gpt-5-nano", cost: ptr(0.001), latency: 100},
	}
	src.add("exp_h4", "bigfinancebench", arms, tasks, pass)
	ev := &schemas.Evaluation{ID: "ev_h4", Status: schemas.EvaluationDone, Spec: schemas.EvaluationSpec{Mode: schemas.EvaluationModeModelSwitch, Trials: 2, Arms: []schemas.EvaluationArm{
		{Label: "A", Agent: "bare-model", Model: "gpt-5.4-mini", Provider: "openai"},
		{Label: "B", Agent: "bare-model", Model: "gpt-5.4-nano", Provider: "openai"},
		{Label: "C", Agent: "bare-model", Model: "gpt-5-nano", Provider: "openai"},
	}}, Children: []schemas.EvaluationChild{{Benchmark: "bigfinancebench", ExperimentID: "exp_h4", Status: "done"}}}
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func TestNothingSolvedRecommendsNothing(t *testing.T) {
	rep := threeModels(t, []string{"t1", "t2", "t3"}, func(string, string) bool { return false })
	r := rep.Recommendation
	if r.Action != ActionNothingSolved || r.Model != "" || r.Summary != NothingSolvedSummary {
		t.Fatalf("nothing solved must recommend nothing: %+v", r)
	}
	if rep.Best != "" {
		t.Fatalf("no model is best when none solved a task, got %q", rep.Best)
	}
	ex := Routing(rep, RoutingOptions{})
	if ex.Ready || len(ex.Config.Governance.RoutingRules) != 0 {
		t.Fatalf("no routing rule when nothing was solved: %+v", ex)
	}
}

func TestTooFewRunsRecommendsNothing(t *testing.T) {
	// 4 tasks, 2 trials: 8 runs per model, under MinRunsPerModel.
	rep := threeModels(t, []string{"t1", "t2", "t3", "t4"}, func(arm, task string) bool { return arm == "A" })
	r := rep.Recommendation
	if r.Action != ActionNotEnough || !strings.HasPrefix(r.Summary, TooFewRunsSummary) {
		t.Fatalf("under %d runs per model: %+v", MinRunsPerModel, r)
	}
}

func TestEnoughRunsStillRecommends(t *testing.T) {
	rep := threeModels(t, []string{"t1", "t2", "t3", "t4", "t5"}, func(arm, task string) bool { return arm == "A" })
	if a := rep.Recommendation.Action; a != ActionKeep && a != ActionSwitch {
		t.Fatalf("10 runs per model with solves gets an answer: %+v", rep.Recommendation)
	}
}
