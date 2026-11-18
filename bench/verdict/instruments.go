package verdict

import (
	"fmt"
	"math"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Instrument check names (DESIGN.md 9.4). Usage completeness and dropped logs
// can make the experiment INVALID; the two parity checks and upstream errors
// only warn.
const (
	InstrumentUsageCompleteness = "usage_completeness"
	InstrumentDroppedLogs       = "dropped_logs"
	InstrumentTimeoutParity     = "timeout_parity"
	InstrumentBudgetParity      = "budget_parity"
	InstrumentUpstreamErrors    = "upstream_errors"
	// InstrumentGatewayHygiene checks the runners' own gateway preflight
	// records when a hosted manifest defers the preflight to runners
	// (Tenancy area). Other manifests keep the hygiene record of their own
	// preflight, which refused the plan on any violation.
	InstrumentGatewayHygiene = "gateway_hygiene"
	// InstrumentGatewayVersions warns when the counted runs of a deferred
	// manifest came from different runner gateways (bench part or gateway
	// version): the manifest freezes no gateway part, so mixed versions are
	// shown, never silent (Tenancy area).
	InstrumentGatewayVersions = "gateway_versions"
	// InstrumentRunnerImages warns when the counted runs of a deferred
	// manifest, planned without Docker and so with no frozen image ids, ran
	// on different image ids for the same role; unknown when a run records
	// none (Deploy area, engine/remoteimages.go).
	InstrumentRunnerImages = "runner_images"
)

// GateUpstreamErrors is the name of the report gate that counts a run's model
// calls the fault proxy could not deliver to the provider.
const GateUpstreamErrors = "upstream_errors"

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
	checks := []schemas.InstrumentCheck{usageCompleteness(idx), droppedLogs(m, idx), timeoutParityCheck(idx), budgetParity(m), upstreamErrors(idx)}
	if m != nil && m.Gateway.Deferred() { // Tenancy area: the runners' records stand in for the server's preflight
		checks = append(checks, runnerGatewayHygiene(idx), runnerGatewayVersions(idx), runnerImages(idx)) // Deploy area: runner_images
	}
	return withHardwareParity(checks, idx) // X7: GPU runs on different GPU models make the experiment INVALID (hardwareparity.go)
}

// runnerGatewayVersions groups the counted runs of a deferred manifest by
// the gateway their runner used: the bench part version and the gateway's
// own name and version, from each run's runner record. One version passes;
// more than one warns, naming each with its run count; a run without a
// record leaves the check unknown.
func runnerGatewayVersions(idx runIndex) schemas.InstrumentCheck {
	c := schemas.InstrumentCheck{Name: InstrumentGatewayVersions, Threshold: "one runner gateway version across the counted runs"}
	counts := map[string]int{}
	runs, missing := 0, 0
	for _, arm := range armsIn(idx, "") {
		for _, r := range idx.armRuns("", arm) {
			runs++
			g := r.RunnerGateway
			if g == nil || g.PartVersion == "" {
				missing++
				continue
			}
			counts[fmt.Sprintf("%s (%s %s)", g.PartVersion, g.Name, g.Version)]++
		}
	}
	keys := sortedKeys(counts)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %d runs", k, counts[k]))
	}
	switch {
	case runs == 0 || len(keys) == 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("gateway preflight deferred to runners: no runner gateway version recorded in %d counted runs", runs)
	case len(keys) > 1:
		c.Status = schemas.InstrumentWarn
		c.Detail = "counted runs came from different runner gateways: " + strings.Join(parts, "; ")
		if missing > 0 {
			c.Detail += fmt.Sprintf("; %d runs record none", missing)
		}
	case missing > 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("%d of %d counted runs record no runner gateway version; the others: %s", missing, runs, strings.Join(parts, "; "))
	default:
		c.Status = schemas.InstrumentPass
		c.Detail = "one runner gateway across the counted runs: " + strings.Join(parts, "; ")
	}
	return c
}

// runnerImages groups the image ids the runners recorded for the counted
// runs of a deferred manifest by role (Deploy area). One id per role passes;
// a role with more than one id warns, naming each id with its run count; a
// run without a record leaves the check unknown with the count.
func runnerImages(idx runIndex) schemas.InstrumentCheck {
	c := schemas.InstrumentCheck{Name: InstrumentRunnerImages, Threshold: "one image id per role across the counted runs"}
	byRole := map[string]map[string]int{}
	runs, missing := 0, 0
	for _, arm := range armsIn(idx, "") {
		for _, r := range idx.armRuns("", arm) {
			runs++
			if r.RunnerImages == nil {
				missing++
				continue
			}
			for role, id := range r.RunnerImages.IDs {
				if byRole[role] == nil {
					byRole[role] = map[string]int{}
				}
				byRole[role][id]++
			}
		}
	}
	var mixed, roles []string
	for _, role := range sortedKeys(byRole) {
		ids := byRole[role]
		parts := make([]string, 0, len(ids))
		for _, id := range sortedKeys(ids) {
			parts = append(parts, fmt.Sprintf("%s: %d runs", shortImageID(id), ids[id]))
		}
		if len(ids) > 1 {
			mixed = append(mixed, role+" -> "+strings.Join(parts, ", "))
		}
		roles = append(roles, role+" "+strings.Join(parts, ", "))
	}
	switch {
	case runs == 0 || (missing == runs):
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("images resolved on runners: no runner image record in %d counted runs (remote/worker.json images)", runs)
	case len(mixed) > 0:
		c.Status = schemas.InstrumentWarn
		c.Detail = "counted runs used different image ids for the same role: " + strings.Join(mixed, "; ")
		if missing > 0 {
			c.Detail += fmt.Sprintf("; %d runs record none", missing)
		}
	case missing > 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("%d of %d counted runs record no runner image ids; the others: %s", missing, runs, strings.Join(roles, "; "))
	default:
		c.Status = schemas.InstrumentPass
		c.Detail = fmt.Sprintf("one image id per role in all %d counted runs", runs)
		if len(roles) > 0 {
			c.Detail += ": " + strings.Join(roles, "; ")
		}
	}
	return c
}

// shortImageID trims a sha256:... image id to 12 hex characters for a
// detail line.
func shortImageID(id string) string {
	if s, ok := strings.CutPrefix(id, "sha256:"); ok && len(s) > 12 {
		return "sha256:" + s[:12]
	}
	return id
}

// runnerGatewayHygiene reads the gateway preflight each runner recorded for
// each counted run of a manifest that defers the preflight to runners. A
// violation fails the check (INVALID): the run's model traffic may have been
// rewritten, cached or rerouted. A run without a record leaves the check
// unknown with the count, never a pass: nothing shows that its gateway was
// checked. With no counted run it is unknown too.
func runnerGatewayHygiene(idx runIndex) schemas.InstrumentCheck {
	c := schemas.InstrumentCheck{Name: InstrumentGatewayHygiene,
		Threshold: "every run's runner gateway preflight: no MCP auto-inject with MCP clients, no semantic cache, no multi-target routing"}
	runs, missing := 0, 0
	var violations []string
	for _, arm := range armsIn(idx, "") {
		for _, r := range idx.armRuns("", arm) {
			runs++
			g := r.RunnerGateway
			if g == nil || g.Hygiene == nil {
				missing++
				continue
			}
			for _, v := range g.Hygiene.Violations {
				violations = append(violations, fmt.Sprintf("run %s: %s", r.Run.RunID, v))
			}
		}
	}
	switch {
	case len(violations) > 0:
		c.Status = schemas.InstrumentFail
		c.Detail = "runner gateway hygiene violated: " + strings.Join(violations, "; ")
	case runs == 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = "gateway preflight deferred to runners and no counted run carries a runner record"
	case missing > 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("gateway preflight deferred to runners: %d of %d counted runs carry no runner gateway record (remote/worker.json gateway)", missing, runs)
	default:
		c.Status = schemas.InstrumentPass
		c.Detail = fmt.Sprintf("runner gateway preflight clean in all %d counted runs", runs)
	}
	return c
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

// upstreamErrors counts, per arm, the counted runs on which the
// upstream_errors gate fired: model calls the fault proxy passed on with no
// fault planned but could not deliver to the provider. It only warns. Such
// runs never score against the agent (the engine seals a run that never got a
// model response as aborted_infra and one that ended on such an error as
// infra_error), but the reader should know the provider was unreachable
// during the experiment, and in which arm. A run sealed before the gate
// existed carries no result and is left out; with no run carrying it the
// check is not applicable.
func upstreamErrors(idx runIndex) schemas.InstrumentCheck {
	c := schemas.InstrumentCheck{Name: InstrumentUpstreamErrors, Threshold: "no run with upstream errors", PerArm: map[string]schemas.Ratio{}}
	var fired, carried int
	var parts []string
	for _, arm := range armsIn(idx, "") {
		var r schemas.Ratio
		for _, run := range idx.armRuns("", arm) {
			for _, g := range run.Gates {
				if g.Gate != GateUpstreamErrors {
					continue
				}
				r.Den++
				if g.Fired {
					r.Num++
				}
				break
			}
		}
		if r.Den == 0 {
			continue
		}
		c.PerArm[arm] = r
		fired += r.Num
		carried += r.Den
		if r.Num > 0 {
			parts = append(parts, fmt.Sprintf("%s %d of %d", arm, r.Num, r.Den))
		}
	}
	switch {
	case carried == 0:
		c.Status = schemas.InstrumentNotApplicable
		c.Detail = "no counted run carries the upstream_errors gate"
	case fired == 0:
		c.Status = schemas.InstrumentPass
		c.Detail = fmt.Sprintf("the provider answered every passed model call in %d runs", carried)
	default:
		c.Status = schemas.InstrumentWarn
		c.Detail = "runs with model calls the provider never answered: " + strings.Join(parts, ", ") +
			"; they never score against the agent (aborted_infra or infra_error when it could not go on)"
	}
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
