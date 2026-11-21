package main

import (
	"context"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/maximhq/bifrost/bench/verdict"
)

// A sealed one-arm experiment analyses to a score with an interval: no
// verdict, and a report with a Score section instead of the A/B tables.
func TestAnalyzeSingleArmScore(t *testing.T) {
	a, m := analysisFixture(t)
	st := a.store
	ctx := context.Background()
	one := &schemas.Manifest{ExpID: "e_one", Arms: map[string]schemas.ArmManifest{"A": {Label: "codex on m"}},
		Experiment: schemas.Experiment{Primary: m.Experiment.Primary, Arms: map[string]schemas.ArmSpec{"A": {Label: "codex on m"}}}}
	for _, task := range []string{"t01", "t02", "t03"} {
		one.Slots = append(one.Slots, schemas.Slot{SlotID: "s_" + task, PairID: "p_" + task, Task: task, TaskVersion: "1.0.0",
			Arm: "A", Condition: schemas.ConditionClean, Trial: 1, Counted: true})
	}
	var err error
	if one.Hash, err = utils.ManifestHash(one); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateExperiment(ctx, one); err != nil {
		t.Fatal(err)
	}
	for i := range one.Slots {
		sl := one.Slots[i]
		sl.ExpID = one.ExpID
		y, out := "1", schemas.OutcomePass
		if sl.Task == "t03" {
			y, out = "0", schemas.OutcomeFail
		}
		r := &schemas.Run{RunID: "r_one_" + sl.Task, ExpID: one.ExpID, SlotID: sl.SlotID, PairID: sl.PairID,
			Attempt: 1, Stage: schemas.StageSeal, ManifestHash: one.Hash, Outcome: out}
		if _, err := st.NewRun(ctx, &sl, r); err != nil {
			t.Fatal(err)
		}
		if err := st.WriteRunFile(ctx, r, "metrics.json", []byte(`{"verified_success":{"state":"observed","v":`+y+`,"evidence":"outcome"}}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Seal(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	an, report, err := a.analyze(ctx, "e_one", "")
	if err != nil {
		t.Fatal(err)
	}
	v := an.Verdict
	if v.Decision != schemas.DecisionScore || v.Score == nil || v.Score.Runs != 3 || v.Score.Mean == nil || *v.Score.Mean != 2.0/3 || v.Score.Interval == nil {
		t.Fatalf("verdict %+v reasons %v score %+v", v.Decision, v.InvalidReasons, v.Score)
	}
	if v.Score.Arm != verdict.ArmBaseline || !strings.Contains(string(report), "## Score") || strings.Contains(string(report), "## Primary endpoint") {
		t.Fatalf("report:\n%s", report)
	}
}
