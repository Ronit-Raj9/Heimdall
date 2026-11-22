package handlers

import (
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// First-look preset (lane Z01, audit/INFRA_I2_AGENT_ONBOARDING.md 4.11):
// the first evaluation of a newly connected agent. One benchmark, 5 tasks,
// 1 trial, clean only, score mode, 5 runs at a time for this evaluation
// only. It is never a release gate.

// First-look sizes (connect.FirstLook*, repeated here so the evaluation
// rules do not depend on the connect package).
const (
	firstLookTasks               = 5
	firstLookTrials              = 1
	firstLookConcurrency         = 5
	evaluationConcurrencyDefault = 2
)

// normalizeFirstLook applies and checks the first-look preset.
func normalizeFirstLook(spec *schemas.EvaluationSpec) error {
	spec.Preset = strings.TrimSpace(spec.Preset)
	switch spec.Preset {
	case "":
		return nil
	case schemas.EvaluationPresetFirstLook:
	default:
		return badf("preset %q is not known: use first_look or leave it empty", spec.Preset)
	}
	if spec.Mode != schemas.EvaluationModeScore {
		return badf("a first look scores one agent: mode must be score")
	}
	if spec.Pack != "" || len(spec.Benchmarks) != 1 {
		return badf("a first look runs exactly one benchmark: POST /connect/first-look picks it")
	}
	if spec.Gate != nil {
		return badf("a first look is too small to gate a release: remove the gate")
	}
	if spec.TasksPerBenchmark == 0 || spec.TasksPerBenchmark > firstLookTasks {
		spec.TasksPerBenchmark = firstLookTasks
	}
	spec.Trials = firstLookTrials
	spec.Faults = nil
	return nil
}

// evaluationConcurrency is how many runs of one child experiment run at
// once.
func evaluationConcurrency(spec schemas.EvaluationSpec) int {
	if spec.Preset == schemas.EvaluationPresetFirstLook {
		return firstLookConcurrency
	}
	return evaluationConcurrencyDefault
}
