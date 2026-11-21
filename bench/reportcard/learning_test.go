package reportcard

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestBuildLearningScoreMode: a score-mode stream benchmark gets a learning
// row for the shown arm only, with forward transfer against the hidden
// fresh repeat arm, and its replay runs stay out of the benchmark score.
func TestBuildLearningScoreMode(t *testing.T) {
	src := newSource()
	seq := func(arm string, succ ...bool) []fixRun {
		var out []fixRun
		for i, ok := range succ {
			r := fixRun{arm: arm, task: string(rune('a' + i)), outcome: schemas.OutcomeFail, stream: i + 1}
			if ok {
				r.outcome = schemas.OutcomePass
			}
			out = append(out, r)
		}
		return out
	}
	runs := append(seq("A", false, true, true, true), seq("B", false, false, true, false)...)
	runs = append(runs, fixRun{arm: "A", task: "a", outcome: schemas.OutcomePass, stream: 5, replay: true})
	src.add("e1", "longmemeval", []string{"A", "B"}, runs)
	src.manifests["e1"].Experiment.Stream = &schemas.StreamSpec{Memory: schemas.StreamMemoryCarry, FreshArm: "B", Replay: 1}
	ev := &schemas.Evaluation{ID: "ev_s", Status: StatusDone, Spec: schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{{Label: "A", Agent: "codex", Model: "m"}}},
		Children: []schemas.EvaluationChild{{Benchmark: "longmemeval", ExperimentID: "e1"}}}
	rep, err := Build(context.Background(), src, ev, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Benchmarks[0].Arms["A"]; got.Runs != 4 || got.Score != 0.75 {
		t.Fatalf("replay must stay out of the score: %+v", got)
	}
	if len(rep.Learning) != 1 {
		t.Fatalf("learning rows %+v", rep.Learning)
	}
	l := rep.Learning[0]
	if l.Arm != "A" || l.Benchmark != "longmemeval" || l.Chains != 1 || len(l.Curve) != 4 || l.Threshold != schemas.DefaultStreamThreshold {
		t.Fatalf("row %+v", l)
	}
	// carry on b,c,d = 1,1,1; fresh = 0,1,0: forward transfer 2/3.
	if l.FWT == nil || *l.FWT < 0.66 || *l.FWT > 0.67 {
		t.Fatalf("fwt %v", l.FWT)
	}
	if l.BWT == nil || *l.BWT != 1 {
		t.Fatalf("bwt %v", l.BWT)
	}
	if l.EpisodesToThreshold == nil || *l.EpisodesToThreshold != 4 {
		t.Fatalf("episodes %v", l.EpisodesToThreshold)
	}
	if rep.Persistence != nil {
		t.Fatalf("no persistence metrics: %+v", rep.Persistence)
	}
}

// TestBuildPersistence: the persistence metrics average per arm over every
// condition, and a report without stream runs has no learning rows.
func TestBuildPersistence(t *testing.T) {
	src := newSource()
	src.add("e1", "coding-core", []string{"a", "b"}, []fixRun{
		{arm: "a", task: "t1", cond: "kill-resume", outcome: schemas.OutcomePass, metrics: map[string]float64{metricResumedWithoutRedo: 1}},
		{arm: "a", task: "t2", cond: "kill-resume", outcome: schemas.OutcomeFail, metrics: map[string]float64{metricResumedWithoutRedo: 0}},
		{arm: "a", task: "t1", cond: "squeeze", outcome: schemas.OutcomeFail, metrics: map[string]float64{metricGracefulFinish: 1, metricCostNormalized: 4}},
		pass("b", "t1", 1, nil),
	})
	rep, err := Build(context.Background(), src, abEval(schemas.EvaluationChild{Benchmark: "coding-core", ExperimentID: "e1"}), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Learning != nil {
		t.Fatalf("no stream: %+v", rep.Learning)
	}
	if len(rep.Persistence) != 1 {
		t.Fatalf("persistence %+v", rep.Persistence)
	}
	p := rep.Persistence[0]
	if p.Arm != "A" || p.ResumedWithoutRedo == nil || p.ResumedWithoutRedo.Value != 0.5 || p.ResumedWithoutRedo.Runs != 2 ||
		p.GracefulFinish == nil || p.GracefulFinish.Value != 1 || p.CostNormalizedSuccess == nil || p.CostNormalizedSuccess.Value != 4 {
		t.Fatalf("row %+v %+v %+v", p, p.ResumedWithoutRedo, p.GracefulFinish)
	}
}
