package verdict

import (
	"slices"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// setupArms returns the experiment's setups in id order: every arm except an
// A/A copy of arm A (hasAAArm), which measures noise and is not a setup.
func setupArms(m *schemas.Manifest) []string {
	aa := hasAAArm(m)
	var out []string
	for arm := range manifestArms(m) {
		if aa && arm == ArmAA {
			continue
		}
		out = append(out, arm)
	}
	slices.Sort(out)
	return out
}

// decideSetups turns v, the verdict verdictFor built, into the verdict of a
// comparison of three or more setups (fix wave 3, N244). There is no
// B-against-A story: the instrument checks and invalid reasons stay as
// verdictFor found them (they span every arm), and the decision comes from
// the same matrix the Setups tab shows (leaderboard.Matrix: every pair by
// the exact sign-flip test, Holm-adjusted, tiers read off the leader):
//
//   - invalid when there is an invalid reason;
//   - inconclusive when the leader cannot be told apart from another setup,
//     no pair could ever be separated, or a setup has no graded run;
//   - leader when one setup is significantly ahead of every other.
//
// A leader is never releasable (D12): its action is no_decision, or hold when
// a veto gate fired in one of the leader's runs. Safety lists, per setup, the
// tasks with such a run.
func decideSetups(m *schemas.Manifest, cfg settings, idx runIndex, runs []schemas.SealedRun, setups []string, v *schemas.Verdict) {
	sv := &schemas.SetupsVerdict{TopGroup: []string{}, Behind: []string{}, Pairs: []schemas.MatrixPair{}}
	for _, arm := range setups {
		if tasks := sortedKeys(vetoTasks(idx.armRuns("", arm))); len(tasks) > 0 {
			if sv.Safety == nil {
				sv.Safety = map[string][]string{}
			}
			sv.Safety[arm] = tasks
		}
	}
	complete := false
	q := leaderboard.MatrixQuery{Condition: cfg.primary.Condition, Alpha: cfg.alpha}
	if mx, err := leaderboard.Matrix(m, runs, false, q); err == nil && len(mx.Rows) > 0 {
		sv.Leader, sv.Separable, sv.Note = mx.Rows[0].Arm, mx.Separable, mx.Note
		for _, r := range mx.Rows {
			if r.Tier == leaderboard.TierBehind {
				sv.Behind = append(sv.Behind, r.Arm)
			} else {
				sv.TopGroup = append(sv.TopGroup, r.Arm)
			}
		}
		for _, p := range mx.Pairs {
			if p.ArmX == sv.Leader || p.ArmY == sv.Leader {
				sv.Pairs = append(sv.Pairs, p)
			}
		}
		complete = len(mx.Rows) == len(setups)
	}
	// Effect, MDE and tasks needed stay as verdictFor computed them (arm B
	// against arm A, descriptive only): reports and checks that read them keep
	// working, and no decision or headline of a setups comparison reads them.
	v.Setups, v.Basis = sv, ""
	_, flagged := sv.Safety[sv.Leader]
	switch {
	case len(v.InvalidReasons) > 0:
		v.Decision, v.Action = schemas.DecisionInvalid, schemas.ActionFixHarness
	case !sv.Separable || !complete || len(sv.TopGroup) != 1:
		v.Decision, v.Action = schemas.DecisionInconclusive, schemas.ActionNoDecision
	case flagged:
		v.Decision, v.Action = schemas.DecisionLeader, schemas.ActionHold
	default:
		v.Decision, v.Action = schemas.DecisionLeader, schemas.ActionNoDecision
	}
}

// basisOf says what decided v (Verdict.Basis): the primary effect when it is
// significant at both bounds in the decision's direction, else a guard. Only
// regressed and improved verdicts have a basis.
func basisOf(v *schemas.Verdict, alpha float64) string {
	switch v.Decision {
	case schemas.DecisionRegressed:
		if regressedAtBoth(v.Effect, alpha) {
			return BasisEffect
		}
		return BasisGuard
	case schemas.DecisionImproved:
		if improvedAtBoth(v.Effect, alpha) {
			return BasisEffect
		}
		return BasisGuard
	}
	return ""
}
