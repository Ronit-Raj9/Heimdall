package metrics

import (
	"context"
	"fmt"
	"math"
	"regexp"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Reward is the name of the verifier reward metric.
const Reward = "reward"

// RewardDimensionPrefix prefixes the per-run value of each reward dimension:
// a verifier dimension "correctness" is reported as "reward_correctness".
const RewardDimensionPrefix = "reward_"

const (
	reasonNoReward = "verifier wrote no usable reward"
	maxDimensions  = 32
)

// dimensionRe is the dimension name charset the verifier_command grader
// enforces, checked again here because a grade on disk is only data.
var dimensionRe = regexp.MustCompile(`^[a-z0-9_]{1,40}$`)

var _ schemas.Metric = (*rewardMetric)(nil)

// rewardMetric reports the number a reward-format verifier wrote
// (Grade.Reward) and, through ComputeDerived, its named dimensions. Both are
// report-only: they are never blended into verified_success, which the grade's
// pass rule alone decides, and veto gates do not change them.
type rewardMetric struct{}

// GetName implements schemas.Part.
func (m *rewardMetric) GetName() string { return Reward }

// GetVersion implements schemas.Part.
func (m *rewardMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *rewardMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name:           Reward,
		Version:        version,
		Label:          "Verifier reward",
		Kind:           schemas.KindCount,
		HigherIsBetter: true,
		MinEvidence:    schemas.EvidenceOutcome,
		Description:    "Reward the suite's reward-format verifier wrote, report-only and never part of verified_success, over runs whose verifier reported a reward.",
		PS2:            false,
	}
}

// Compute implements schemas.Metric. A grade in another result format (junit,
// exit_code, or hidden_tests, which sets none) has no reward: not applicable.
// A reward-format grade without a number (the patch did not apply, the
// verifier timed out or wrote no usable file) is unknown, never 0, because
// the number was not observed; the run still fails verified_success.
func (m *rewardMetric) Compute(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	g := orEmpty(ev).Grade
	if v, bad := gradeProblem(g); bad {
		return v, nil
	}
	if g.ResultFormat != schemas.VerifierResultReward {
		return schemas.NotApplicable(), nil
	}
	if g.Reward == nil || !isFinite(*g.Reward) {
		reason := reasonNoReward
		if g.Reason != "" {
			reason = fmt.Sprintf("%s: %s", reasonNoReward, g.Reason)
		}
		return schemas.Unknown(reason, schemas.EvidenceOutcome), nil
	}
	return schemas.Observed(*g.Reward, schemas.EvidenceOutcome), nil
}

// ComputeDerived returns one report-only value per reward dimension, named
// RewardDimensionPrefix + dimension, for a reward-format grade that carries a
// reward. Dimension names are known only per run, so they cannot be
// registered metrics; a run with no dimensions, or no usable reward, returns
// none, and readers treat a missing name as unknown, never 0. Names outside
// [a-z0-9_]{1,40}, non-finite values and dimensions beyond the 32 the grader
// allows are dropped. It never fails.
func (m *rewardMetric) ComputeDerived(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (map[string]schemas.Value, error) {
	g := orEmpty(ev).Grade
	if _, bad := gradeProblem(g); bad || g.ResultFormat != schemas.VerifierResultReward || g.Reward == nil || len(g.Dimensions) > maxDimensions {
		return nil, nil
	}
	var out map[string]schemas.Value
	for name, v := range g.Dimensions {
		if !dimensionRe.MatchString(name) || !isFinite(v) {
			continue
		}
		if out == nil {
			out = make(map[string]schemas.Value, len(g.Dimensions))
		}
		out[RewardDimensionPrefix+name] = schemas.Observed(v, schemas.EvidenceOutcome)
	}
	return out, nil
}

// isFinite reports whether v is neither NaN nor an infinity.
func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// newRewardMetric returns the reward metric.
func newRewardMetric() *rewardMetric {
	return &rewardMetric{}
}
