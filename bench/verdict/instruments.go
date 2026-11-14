package verdict

import (
	"fmt"
	"math"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Instrument check names (DESIGN.md 9.4). Usage completeness and dropped logs
// can make the experiment INVALID; the two parity checks only warn.
const (
	InstrumentUsageCompleteness = "usage_completeness"
	InstrumentDroppedLogs       = "dropped_logs"
	InstrumentTimeoutParity     = "timeout_parity"
	InstrumentBudgetParity      = "budget_parity"
)

const (
	minUsageCompleteness = 0.95
	maxUsageGapPP        = 5.0
)

// instrumentChecks runs the measured instrument checks over the counted runs:
// usage completeness per arm, Bifrost's global drop counter, and the timeout
// and budget parity diagnostics. A failed check makes the experiment INVALID;
// an unknown one holds an IMPROVED verdict; a warning changes nothing unless
// the experiment declares the timeout_parity guard.
func instrumentChecks(m *schemas.Manifest, idx runIndex) []schemas.InstrumentCheck {
	return []schemas.InstrumentCheck{usageCompleteness(idx), droppedLogs(m, idx), timeoutParityCheck(idx), budgetParity(m)}
}

// instrumentReasons returns the detail of every failed check, as invalid
// reasons.
func instrumentReasons(checks []schemas.InstrumentCheck) []string {
	var out []string
	for _, c := range checks {
		if c.Status == schemas.InstrumentFail {
			out = append(out, c.Detail)
		}
	}
	return out
}

// anyInstrument reports whether some check has the status.
func anyInstrument(checks []schemas.InstrumentCheck, status schemas.InstrumentStatus) bool {
	for _, c := range checks {
		if c.Status == status {
			return true
		}
	}
	return false
}

// usageCompleteness is, per arm, the share of model requests (gateway message
// rows that are not errors) over every counted run that carry token counts.
// Tokens and cost rest on that usage, so an arm below 0.95, or arms more than
// 5 pp apart, make the experiment INVALID: the missing usage could hide any
// difference. A run whose request log is missing or incomplete has unknown
// usage (DESIGN.md 13.4): every row it has, and at least one, counts as a
// request without usage. A run whose agent never started and that has no log
// made no model request and is left out. An arm with no model requests at all
// is left out. With no gateway log in any counted run the check is unknown.
func usageCompleteness(idx runIndex) schemas.InstrumentCheck {
	c := schemas.InstrumentCheck{
		Name:      InstrumentUsageCompleteness,
		Threshold: fmt.Sprintf(">= %.2f in every arm, arms at most %.0f pp apart", minUsageCompleteness, maxUsageGapPP),
		PerArm:    map[string]schemas.Ratio{},
	}
	share := map[string]float64{}
	logs := 0
	for _, arm := range armsIn(idx, "") {
		var r schemas.Ratio
		for _, run := range idx.armRuns("", arm) {
			t := run.Gateway
			switch {
			case !agentRan(run):
			case t == nil:
				r.Den++
				r.Unknown++
			case !t.Complete:
				logs++
				n := max(t.Requests, 1)
				r.Den += n
				r.Unknown += n
			default:
				logs++
				r.Den += t.Requests
				r.Num += t.WithUsage
			}
		}
		c.PerArm[arm] = r
		if r.Den > 0 {
			share[arm] = float64(r.Num) / float64(r.Den)
		}
	}
	switch {
	case logs == 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = "usage completeness unknown: no counted run has a gateway request log"
		return c
	case len(share) == 0:
		c.Status = schemas.InstrumentNotApplicable
		c.Detail = "no arm made a model request"
		return c
	}
	var reasons []string
	for _, arm := range sortedKeys(share) {
		if share[arm] < minUsageCompleteness {
			r := c.PerArm[arm]
			reasons = append(reasons, fmt.Sprintf("usage completeness %.3f (%d of %d model requests carry usage, %d in runs with a missing or incomplete log) in arm %s is below %.2f",
				share[arm], r.Num, r.Den, r.Unknown, arm, minUsageCompleteness))
		}
	}
	lo, hi := extremeArms(share)
	if gap := 100 * (share[hi] - share[lo]); gap > maxUsageGapPP+zeroTolerance {
		reasons = append(reasons, fmt.Sprintf("usage completeness gap %.1f pp between arm %s (%.3f) and arm %s (%.3f) is above %.0f pp",
			gap, lo, share[lo], hi, share[hi], maxUsageGapPP))
	}
	if len(reasons) > 0 {
		c.Status = schemas.InstrumentFail
		c.Detail = strings.Join(reasons, "; ")
		return c
	}
	parts := make([]string, 0, len(share))
	for _, arm := range sortedKeys(share) {
		parts = append(parts, fmt.Sprintf("arm %s %.3f", arm, share[arm]))
	}
	c.Status = schemas.InstrumentPass
	c.Detail = "usage completeness " + strings.Join(parts, ", ")
	return c
}

// droppedLogs reads Bifrost's global drop counter, recorded at preflight and
// in every run's evidence at collect time. Any rise over the experiment means
// some log row was lost, and the lost row could belong to any run, so the
// experiment is INVALID (fail). The counter is global: a rise caused by
// traffic outside the experiment also fails, because the bench cannot tell
// whose row was lost. A run without the counter leaves the check unknown, never
// a pass, unless a rise is already seen; a run whose agent never started and
// that has no log never collected, and is left out. One reading cannot show a
// rise: it passes only when it is 0. A gateway restart resets the counter, so
// a fall from a nonzero reading fails too, the conservative reading.
func droppedLogs(m *schemas.Manifest, idx runIndex) schemas.InstrumentCheck {
	c := schemas.InstrumentCheck{Name: InstrumentDroppedLogs, Threshold: "no rise over the experiment"}
	var vals []int64
	if m != nil && m.Gateway != nil && m.Gateway.DroppedLogs != nil {
		vals = append(vals, *m.Gateway.DroppedLogs)
	}
	runs, missing := 0, 0
	for _, arm := range armsIn(idx, "") {
		for _, r := range idx.armRuns("", arm) {
			if !agentRan(r) {
				continue
			}
			runs++
			if r.Gateway == nil || r.Gateway.Dropped == nil {
				missing++
				continue
			}
			vals = append(vals, *r.Gateway.Dropped)
		}
	}
	if len(vals) == 0 {
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("gateway drop counter not recorded in any of %d runs", runs)
		return c
	}
	lo, hi := int64(math.MaxInt64), int64(math.MinInt64)
	for _, v := range vals {
		lo, hi = min(lo, v), max(hi, v)
	}
	readings := fmt.Sprintf("%d readings, counter %d to %d", len(vals), lo, hi)
	switch {
	case hi > lo:
		c.Status = schemas.InstrumentFail
		c.Detail = fmt.Sprintf("gateway dropped %d log rows during the experiment (%s)", hi-lo, readings)
	case missing > 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("gateway drop counter not recorded in %d of %d runs; no rise in the %s", missing, runs, readings)
	case len(vals) == 1 && hi > 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("one reading of the gateway drop counter (%d); a rise during the experiment cannot be ruled out", hi)
	default:
		c.Status = schemas.InstrumentPass
		c.Detail = "no rise in the gateway drop counter (" + readings + ")"
	}
	return c
}

// timeoutParityCheck is the timeout_parity rule of B against A over every
// condition, shown whether or not the experiment declares it as a guard: a
// failure warns here and holds the release only as a declared guard. Arms
// without runs have nothing to compare, and the coverage gate already names
// them.
func timeoutParityCheck(idx runIndex) schemas.InstrumentCheck {
	status, per, detail := timeoutParity(idx, "", ArmBaseline, ArmCandidate)
	c := schemas.InstrumentCheck{Name: InstrumentTimeoutParity, Threshold: TimeoutParityThreshold, PerArm: per, Detail: detail}
	switch status {
	case schemas.GuardPass:
		c.Status = schemas.InstrumentPass
	case schemas.GuardFail:
		c.Status = schemas.InstrumentWarn
	default:
		c.Status = schemas.InstrumentNotApplicable
	}
	return c
}

// budgetParity names every agent budget (wall_s, turns, cpus, mem_gb) that
// differs between the arms with no arm setting it. It recomputes the
// difference from the manifest's resolved arms, so a manifest frozen before
// the planner recorded budget_diffs is checked the same way. A difference
// warns: it is part of what the comparison measures, which the reader must
// know, but two agent configs may differ in budgets on purpose.
func budgetParity(m *schemas.Manifest) schemas.InstrumentCheck {
	c := schemas.InstrumentCheck{Name: InstrumentBudgetParity, Threshold: "equal across arms unless an arm sets it"}
	if m == nil || len(m.Arms) == 0 {
		c.Status = schemas.InstrumentNotApplicable
		c.Detail = "the manifest has no resolved arms"
		return c
	}
	diffs := schemas.BudgetDiffsOf(&m.Experiment, m.Arms)
	if len(diffs) == 0 {
		c.Status = schemas.InstrumentPass
		c.Detail = "wall_s, turns, cpus and mem_gb are equal across arms or set by an arm"
		return c
	}
	parts := make([]string, 0, len(diffs))
	for _, d := range diffs {
		vals := make([]string, 0, len(d.PerArm))
		for _, arm := range sortedKeys(d.PerArm) {
			vals = append(vals, arm+"="+d.PerArm[arm])
		}
		parts = append(parts, d.Field+" "+strings.Join(vals, ", "))
	}
	c.Status = schemas.InstrumentWarn
	c.Detail = strings.Join(parts, "; ") + ": differ between arms with no set: override, so the comparison includes them"
	return c
}

// agentRan reports whether the run reached its agent, or at least collected a
// gateway log. A slot whose every attempt aborted before the agent started is
// counted with its last attempt, which has neither.
func agentRan(r schemas.SealedRun) bool {
	return r.Gateway != nil || r.Run.Timing.AgentStarted != nil
}

// extremeArms returns the arms with the lowest and highest value, the first
// in sorted order on ties.
func extremeArms(vals map[string]float64) (lo, hi string) {
	for _, arm := range sortedKeys(vals) {
		if lo == "" || vals[arm] < vals[lo] {
			lo = arm
		}
		if hi == "" || vals[arm] > vals[hi] {
			hi = arm
		}
	}
	return lo, hi
}
