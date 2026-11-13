package verdict

import (
	"fmt"
	"math"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// instrumentGates returns one reason per instrument gate that fired (DESIGN.md
// 9.4). Any reason makes the experiment INVALID. Coverage is checked on the
// conditions the decision reads (the primary condition and every
// no_regression guard condition); the other gates look at every counted run,
// and egress at every run, counted or not, because a successful outbound
// connection means the harness itself leaked.
func instrumentGates(m *schemas.Manifest, cfg settings, idx runIndex, runs []schemas.SealedRun) []string {
	var reasons []string
	for _, cond := range decisionConditions(cfg) {
		reasons = append(reasons, coverageReasons(idx, cond, cfg.primary.Metric)...)
	}
	reasons = append(reasons, egressReasons(runs)...)
	reasons = append(reasons, invalidateGateReasons(idx)...)
	reasons = append(reasons, trialReasons(idx)...)
	reasons = append(reasons, versionReasons(idx)...)
	reasons = append(reasons, manifestReasons(m, runs)...)
	return reasons
}

// decisionConditions returns the primary condition followed by every
// no_regression guard condition, without repeats.
func decisionConditions(cfg settings) []string {
	out := []string{cfg.primary.Condition}
	seen := map[string]bool{cfg.primary.Condition: true}
	for _, g := range cfg.guards {
		if g.Rule != RuleNoRegression {
			continue
		}
		c := g.Condition
		if c == "" {
			c = schemas.ConditionClean
		}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// coverageReasons checks graded coverage (runs with an observed primary value
// over runs) per arm: below 0.90 in either arm, or a gap above 5 pp, makes the
// comparison untrustworthy because the missing runs could be anything. An arm
// with no runs at all has no coverage to compute and is reported as such.
func coverageReasons(idx runIndex, condition, metric string) []string {
	var reasons []string
	cov := map[string]float64{}
	for _, arm := range []string{ArmBaseline, ArmCandidate} {
		runs := idx.armRuns(condition, arm)
		if len(runs) == 0 {
			reasons = append(reasons, fmt.Sprintf("arm %s has no counted runs on %s", arm, condition))
			continue
		}
		observed := 0
		for _, r := range runs {
			if metricValue(r, metric).IsObserved() {
				observed++
			}
		}
		c := float64(observed) / float64(len(runs))
		cov[arm] = c
		if c < minCoverage {
			reasons = append(reasons, fmt.Sprintf("graded coverage %.3f (%d of %d runs) in arm %s on %s is below %.2f",
				c, observed, len(runs), arm, condition, minCoverage))
		}
	}
	a, okA := cov[ArmBaseline]
	b, okB := cov[ArmCandidate]
	if okA && okB {
		if gap := 100 * math.Abs(a-b); gap > maxCoverageGapPP+zeroTolerance {
			reasons = append(reasons, fmt.Sprintf("graded coverage gap %.1f pp between arm %s (%.3f) and arm %s (%.3f) on %s is above %.0f pp",
				gap, ArmBaseline, a, ArmCandidate, b, condition, maxCoverageGapPP))
		}
	}
	return reasons
}

// egressReasons names every run where the egress gate fired.
func egressReasons(runs []schemas.SealedRun) []string {
	var ids []string
	for _, r := range runs {
		for _, g := range r.Gates {
			if g.Fired && g.Effect == schemas.GateInvalidate && g.Gate == GateEgress {
				ids = append(ids, runLabel(r))
				break
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("egress gate fired in %d run(s): %s", len(ids), strings.Join(ids, "; "))}
}

// invalidateGateReasons fires when any other invalidate gate fired in more
// than 5% of an arm's counted runs. A few isolated fires are tolerated; a
// systematic one (served model, fallback, cache hit) is an instrument fault.
func invalidateGateReasons(idx runIndex) []string {
	var reasons []string
	for _, arm := range armsIn(idx, "") {
		runs := idx.armRuns("", arm)
		fired := map[string]int{}
		for _, r := range runs {
			seen := map[string]bool{}
			for _, g := range r.Gates {
				if g.Fired && g.Effect == schemas.GateInvalidate && g.Gate != GateEgress && !seen[g.Gate] {
					seen[g.Gate] = true
					fired[g.Gate]++
				}
			}
		}
		for _, gate := range sortedKeys(fired) {
			rate := float64(fired[gate]) / float64(len(runs))
			if rate > maxInvalidateRate {
				reasons = append(reasons, fmt.Sprintf("gate %s fired in %d of %d runs (%.1f%%) of arm %s, above %.0f%%",
					gate, fired[gate], len(runs), 100*rate, arm, 100*maxInvalidateRate))
			}
		}
	}
	return reasons
}

// trialReasons fires when trials per arm differ within a (task, condition):
// the paired difference would weigh the arms unequally. Arms A and B must
// match, counting a missing arm as zero; any other arm present must match A.
func trialReasons(idx runIndex) []string {
	var reasons []string
	for _, cond := range sortedKeys(idx) {
		for _, task := range sortedKeys(idx[cond]) {
			byArm := idx[cond][task]
			want := len(byArm[ArmBaseline])
			unequal := len(byArm[ArmCandidate]) != want
			for arm, rs := range byArm {
				if arm != ArmBaseline && len(rs) != want {
					unequal = true
				}
			}
			if !unequal {
				continue
			}
			counts := make([]string, 0, len(byArm)+1)
			for _, arm := range armsWithBase(byArm) {
				counts = append(counts, fmt.Sprintf("arm %s %d", arm, len(byArm[arm])))
			}
			reasons = append(reasons, fmt.Sprintf("unequal trials on task %s condition %s: %s", task, cond, strings.Join(counts, ", ")))
		}
	}
	return reasons
}

// versionReasons fires when a task ran at more than one version in arms A and
// B: the comparison would not be of the same task.
func versionReasons(idx runIndex) []string {
	versions := map[string]map[string]bool{}
	for _, byTask := range idx {
		for task, byArm := range byTask {
			for _, arm := range []string{ArmBaseline, ArmCandidate} {
				for _, r := range byArm[arm] {
					if r.Slot.TaskVersion == "" {
						continue
					}
					if versions[task] == nil {
						versions[task] = map[string]bool{}
					}
					versions[task][r.Slot.TaskVersion] = true
				}
			}
		}
	}
	var reasons []string
	for _, task := range sortedKeys(versions) {
		if len(versions[task]) > 1 {
			reasons = append(reasons, fmt.Sprintf("task %s ran at more than one version: %s",
				task, strings.Join(sortedKeys(versions[task]), ", ")))
		}
	}
	return reasons
}

// manifestReasons fires when a counted run was sealed under a manifest other
// than the one being analysed.
func manifestReasons(m *schemas.Manifest, runs []schemas.SealedRun) []string {
	if m.Hash == "" {
		return nil
	}
	var ids []string
	for _, r := range runs {
		if r.Slot.Counted && r.Run.ManifestHash != "" && r.Run.ManifestHash != m.Hash {
			ids = append(ids, runLabel(r))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return []string{fmt.Sprintf("manifest mismatch: %d run(s) sealed under a manifest other than %s: %s",
		len(ids), m.Hash, strings.Join(ids, "; "))}
}

// runLabel names a run for a reason string from its fields.
func runLabel(r schemas.SealedRun) string {
	return fmt.Sprintf("%s (task %s, arm %s, condition %s, trial %d)", r.Run.RunID, r.Slot.Task, r.Slot.Arm, r.Slot.Condition, r.Slot.Trial)
}

// armsWithBase returns A and B followed by any other arm present, sorted.
func armsWithBase(byArm map[string][]schemas.SealedRun) []string {
	out := []string{ArmBaseline, ArmCandidate}
	for _, arm := range sortedKeys(byArm) {
		if arm != ArmBaseline && arm != ArmCandidate {
			out = append(out, arm)
		}
	}
	return out
}
