package verdict

import (
	"fmt"
	"math"

	"github.com/maximhq/bifrost/bench/schemas"
)

// RuleTimeoutParity is the guard rule that holds a release when the arms hit
// their wall-clock budget at very different rates. The release policy accepts
// the same rule.
const RuleTimeoutParity = "timeout_parity"

// MeterTimeoutRate is the per-arm timeout rate meter.
const MeterTimeoutRate = "timeout_rate"

const (
	maxTimeoutGapPP  = 10.0
	timeoutRateFloor = 0.20
)

// TimeoutParityThreshold describes the rule for reports and policy results.
const TimeoutParityThreshold = "timeout rates at most 10 pp apart, or both at most 20%"

// TimeoutParity compares the timeout rates of armA and armB over the counted
// runs of condition, or of every condition when condition is "" or "all". It
// fails when the rates differ by more than 10 pp and either is above 20%: one
// arm then spends much of its budget against the clock, so its successes and
// costs are measured under a different regime and a difference between the
// arms may be the budget, not the change. It is undetermined when either arm
// has no run. A timed-out run is one whose outcome is timeout or whose agent
// hit its wall budget, which also catches a timed-out run whose partial diff
// still passed.
func TimeoutParity(runs []schemas.SealedRun, condition, armA, armB string) (schemas.GuardStatus, map[string]schemas.Ratio, string) {
	return timeoutParity(newRunIndex(runs), condition, armA, armB)
}

// timeoutParity is TimeoutParity over indexed counted runs.
func timeoutParity(idx runIndex, condition, armA, armB string) (schemas.GuardStatus, map[string]schemas.Ratio, string) {
	if condition == "all" {
		condition = ""
	}
	per := map[string]schemas.Ratio{
		armA: timeoutRatio(idx.armRuns(condition, armA)),
		armB: timeoutRatio(idx.armRuns(condition, armB)),
	}
	where := "every condition"
	if condition != "" {
		where = condition
	}
	a, b := per[armA], per[armB]
	if a.Den == 0 || b.Den == 0 {
		return schemas.GuardUndetermined, per, fmt.Sprintf("arm %s has %d runs and arm %s %d on %s; timeout parity needs both", armA, a.Den, armB, b.Den, where)
	}
	ra, rb := float64(a.Num)/float64(a.Den), float64(b.Num)/float64(b.Den)
	gap := 100 * (rb - ra)
	rates := fmt.Sprintf("timeout rate arm %s %.1f%% (%d of %d), arm %s %.1f%% (%d of %d), gap %+.1f pp on %s",
		armA, 100*ra, a.Num, a.Den, armB, 100*rb, b.Num, b.Den, gap, where)
	if math.Abs(gap) > maxTimeoutGapPP+zeroTolerance && max(ra, rb) > timeoutRateFloor+zeroTolerance {
		return schemas.GuardFail, per, rates + fmt.Sprintf(": more than %.0f pp apart with one arm above %.0f%%", maxTimeoutGapPP, 100*timeoutRateFloor)
	}
	return schemas.GuardPass, per, rates
}

// timeoutParityGuard evaluates the timeout_parity guard of B against A. A
// failure holds the release; it never makes the verdict REGRESSED or INVALID,
// because a timeout gap says the comparison ran under unequal pressure, not
// which arm is better.
func timeoutParityGuard(g schemas.GuardSpec, idx runIndex) schemas.GuardResult {
	status, _, detail := timeoutParity(idx, g.Condition, ArmBaseline, ArmCandidate)
	return schemas.GuardResult{Spec: g, Status: status, Detail: detail}
}

// TimedOut reports whether the run hit its wall-clock budget: outcome timeout,
// or an agent stopped at its wall budget whose partial diff still passed.
func TimedOut(r schemas.SealedRun) bool {
	return r.Run.Outcome == schemas.OutcomeTimeout || r.Run.Agent.TimedOut
}

// timeoutRatio counts the timed-out runs over the runs.
func timeoutRatio(runs []schemas.SealedRun) schemas.Ratio {
	r := schemas.Ratio{Den: len(runs)}
	for _, run := range runs {
		if TimedOut(run) {
			r.Num++
		}
	}
	return r
}

// timeoutRate is the timeout_rate meter for one arm: timed-out runs over
// counted runs, at outcome level.
func timeoutRate(runs []schemas.SealedRun) schemas.Value {
	if len(runs) == 0 {
		return schemas.NotApplicable()
	}
	r := timeoutRatio(runs)
	return schemas.Observed(float64(r.Num)/float64(r.Den), schemas.EvidenceOutcome)
}
