package handlers

import (
	"context"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// armVersionFromEvaluation is the agent version an experiment arm ran, read
// from the evaluation that created the experiment (the manifest's
// evaluation_id): the version of the evaluation arm the experiment arm maps
// to (reportcard.MapArms), or, for an arm the evaluation added on its own
// (a variant arm such as B of a score evaluation), the version of the
// evaluation's single arm. It reports false when the experiment did not come
// from an evaluation, the evaluation cannot be read, the arm maps to an arm
// of another agent, or the evaluation did not record a version.
//
// The evaluation is the record of what ran; re-hashing the manifest arm
// config misses whenever the planned config differs from the agent file
// (the planner fills in model, provider and gateway fields).
func armVersionFromEvaluation(evals ReportCardEvaluations, m *schemas.Manifest, arm string) (int, bool) {
	if evals == nil || m == nil || m.Experiment.EvaluationID == "" {
		return 0, false
	}
	ev, err := evals.Get(context.Background(), m.Experiment.EvaluationID)
	if err != nil || ev == nil {
		return 0, false
	}
	return evaluationArmVersion(ev, m, arm)
}

// evaluationArmVersion is armVersionFromEvaluation on an evaluation already
// read.
func evaluationArmVersion(ev *schemas.Evaluation, m *schemas.Manifest, arm string) (int, bool) {
	a, ok := m.Arms[arm]
	if !ok || arm == "compat" {
		return 0, false
	}
	var spec *schemas.EvaluationArm
	if label, ok := reportcard.MapArms(ev.Spec.Arms, m)[arm]; ok {
		for i := range ev.Spec.Arms {
			if ev.Spec.Arms[i].Label == label {
				spec = &ev.Spec.Arms[i]
				break
			}
		}
	} else if len(ev.Spec.Arms) == 1 {
		spec = &ev.Spec.Arms[0]
	}
	if spec == nil {
		return 0, false
	}
	agent, v := reportcard.AgentRef(spec.Agent)
	if spec.Version != 0 {
		v = spec.Version
	}
	if v <= 0 || (agent != "" && a.Config.ID != "" && agent != a.Config.ID) {
		return 0, false
	}
	return v, true
}
