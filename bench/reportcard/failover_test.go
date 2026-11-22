package reportcard

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/failover"
	"github.com/maximhq/bifrost/bench/schemas"
)

// D6: the failover arm is found by its label: arm B in new evaluations,
// failover.ArmID in older ones.
func TestFailoverSectionFindsArmByLabel(t *testing.T) {
	for _, fb := range []string{"B", failover.ArmID} {
		src := newSource()
		out := schemas.GenericProbePrimaryOutage
		src.add("e_f", "s", []string{"A", fb}, []fixRun{
			pass("A", "t1", 1, nil),
			{arm: "A", task: "t1", cond: out, outcome: schemas.OutcomeFail},
			{arm: fb, task: "t1", cond: out, outcome: schemas.OutcomePass},
		})
		src.manifests["e_f"].Arms[fb] = schemas.ArmManifest{Label: "x on m" + failover.ArmLabelSuffix}
		ev := &schemas.Evaluation{ID: "ev_f", Spec: schemas.EvaluationSpec{Mode: "score", Arms: []schemas.EvaluationArm{{Label: "A"}},
			Failover: &schemas.FailoverOption{Enabled: true}}, Children: []schemas.EvaluationChild{{Benchmark: "s", ExperimentID: "e_f"}}}
		rep, err := failoverSection(context.Background(), src, ev)
		if err != nil || rep == nil {
			t.Fatalf("%s: report %+v err %v", fb, rep, err)
		}
		if !rep.SuccessWith || rep.SuccessWithout || !rep.Clean.TaskSuccess {
			t.Fatalf("%s: cells %+v", fb, rep)
		}
	}
}
