package verdict

import (
	"fmt"
	"math"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/stats"
	"github.com/maximhq/bifrost/bench/utils"
)

// One-arm experiments: a single setup scored on its own. There is nothing to
// compare it with, so the analysis carries no effect, no guards and no MDE:
// the decision is score (or invalid when the evidence does not hold) and
// Verdict.Score gives the primary metric with a task-clustered interval.

const (
	// ScoreConf is the level of a one-arm score's interval.
	ScoreConf = 0.95
	// ScoreResamples is how many task resamples the interval uses.
	ScoreResamples = 2000

	scoreMethodBootstrap = "task-clustered percentile bootstrap over tasks (95%)"
	scoreMethodExact     = "Clopper-Pearson exact interval over runs (95%, one task)"
	scoreMethodNone      = "no interval: fewer than two tasks with observed runs"
)

// SingleArm returns the only arm a manifest declares, or "" when it declares
// none or more than one. The compatibility arm and the A/A copy are never
// declared, so they do not count.
func SingleArm(m *schemas.Manifest) string {
	if m == nil || len(m.Experiment.Arms) != 1 {
		return ""
	}
	for id := range m.Experiment.Arms {
		return id
	}
	return ""
}

// decideSingle is the analysis of a one-arm experiment. Every evidence check
// that does not need a second arm still applies (seals, egress, invalidating
// gates, version mismatches, slot coverage, instrument checks, and
// graded coverage of the one arm), and any failure makes it INVALID, so a
// score is never shown over evidence that does not hold.
func decideSingle(m *schemas.Manifest, cfg settings, idx runIndex, runs, executed []schemas.SealedRun, arm string) schemas.Verdict {
	pc, pm := cfg.primary.Condition, cfg.primary.Metric
	score := singleScore(idx.cells(pc, arm, pm), pc, pm, arm, m.Experiment.Arms[arm].Label)
	checks := append(instrumentChecks(m, idx), slotCoverage(m, runs))
	invalid := evidenceReasons(executed)
	invalid = append(invalid, singleCoverageReasons(idx, pc, pm, arm)...)
	invalid = append(invalid, egressReasons(executed)...)
	gates := invalidateGateReasons(idx)
	invalid = append(invalid, gates...)
	invalid = append(invalid, versionReasons(idx)...)
	invalid = append(invalid, manifestReasons(m, runs)...)
	invalid = append(invalid, instrumentReasons(checks)...)
	decision := schemas.DecisionScore
	action := schemas.ActionNoDecision
	scope := ""
	if len(invalid) > 0 {
		decision, action = schemas.DecisionInvalid, schemas.ActionFixHarness
		scope = "experiment"
		if len(gates) > 0 && len(invalid) == len(gates) {
			scope = "runs" // fix wave 3, N215: as verdictFor, only the runs an invalidate gate fired on are untrustworthy
		}
	}
	return schemas.Verdict{
		Primary:           cfg.primary,
		Decision:          decision,
		Action:            action,
		Alpha:             cfg.alpha,
		RandomizationUnit: randomizationUnit,
		Guards:            []schemas.GuardResult{},
		InvalidReasons:    invalid,
		InvalidScope:      scope,
		Instruments:       checks,
		Integrity:         integrityStatus(idx),
		ManifestHash:      m.Hash,
		FrozenAt:          m.CreatedAt,
		Score:             &score,
	}
}

// singleCoverageReasons is graded coverage for the one arm: no counted runs,
// or fewer than 90% of them observed, means the missing runs could move the
// score anywhere.
func singleCoverageReasons(idx runIndex, condition, metric, arm string) []string {
	runs := idx.armRuns(condition, arm)
	if len(runs) == 0 {
		return []string{fmt.Sprintf("arm %s has no counted runs on %s", arm, condition)}
	}
	observed := 0
	for _, r := range runs {
		if decisionValue(metricValue(r, metric)).IsObserved() {
			observed++
		}
	}
	if c := float64(observed) / float64(len(runs)); c < minCoverage {
		return []string{fmt.Sprintf("graded coverage %.3f (%d of %d runs) in arm %s on %s is below %.2f",
			c, observed, len(runs), arm, condition, minCoverage)}
	}
	return nil
}

// singleScore reduces one arm's cells to its Score. The mean is over observed
// runs (each run weighs the same); the interval resamples tasks, keeping each
// task's runs together. A rate metric on one task gets the exact
// Clopper-Pearson interval over its runs; any other metric on one task gets
// none. Low and high fill every unknown at its lowest and highest possible
// value, and are absent when an unknown has no bounds.
func singleScore(cells map[string]cell, condition, metric, arm, label string) schemas.Score {
	s := schemas.Score{Arm: arm, Label: label, Condition: condition, Metric: metric, Conf: ScoreConf, Method: scoreMethodNone}
	var cs []stats.Cluster
	var all cell
	for _, task := range sortedKeys(cells) {
		c := cells[task]
		if c.n == 0 {
			continue
		}
		s.Tasks++
		all.n += c.n
		all.nObs += c.nObs
		all.obsSum += c.obsSum
		all.unkLo += c.unkLo
		all.unkHi += c.unkHi
		all.unbounded += c.unbounded
		if c.nObs > 0 {
			cs = append(cs, stats.Cluster{Sum: c.obsSum, N: c.nObs})
		}
	}
	s.Runs, s.Observed = all.n, all.nObs
	if all.nObs == 0 {
		return s
	}
	mean := all.obsSum / float64(all.nObs)
	s.Mean = &mean
	if all.unbounded == 0 {
		lo, _ := all.mean(fillLow)
		hi, _ := all.mean(fillHigh)
		s.Low, s.High = &lo, &hi
	}
	seed := uint64(len(cs))<<32 | uint64(all.nObs)
	if lo, hi, ok := stats.ClusterBootstrapCI(cs, ScoreConf, ScoreResamples, seed); ok {
		s.Interval, s.Method = &[2]float64{lo, hi}, scoreMethodBootstrap
	} else if len(cs) == 1 && RateMetric(metric) && isWhole(all.obsSum) {
		lo, hi := stats.ClopperPearson(int(math.Round(all.obsSum)), all.nObs, ScoreConf)
		s.Interval, s.Method = &[2]float64{lo, hi}, scoreMethodExact
	}
	return s
}

// isWhole reports whether x is a whole number, as a sum of 0/1 outcomes is.
func isWhole(x float64) bool { return math.Abs(x-math.Round(x)) < 1e-9 }

// decideSingleAnalysis is Decide for a one-arm experiment: the score verdict
// beside the same naive scoreboard, diagnostics, meters and arm metrics a
// comparison shows.
func decideSingleAnalysis(m *schemas.Manifest, runs []schemas.SealedRun, arm string) *schemas.Analysis {
	cfg := settingsFor(m)
	raw := runs
	runs = DecisionRuns(m, runs)
	idx := newRunIndex(runs)
	return &schemas.Analysis{
		AnalysisID:  utils.NewID("a", 10),
		ExpID:       m.ExpID,
		CreatedAt:   time.Now().UTC(),
		GradeSource: gradeSource(runs),
		Verdict:     decideSingle(m, cfg, idx, runs, raw, arm),
		Naive:       naiveScoreboard(idx, cfg.primary),
		Diagnostics: diagnostics(m, idx, cfg.primary),
		Meters:      meters(idx),
		ArmMetrics:  armMetrics(idx, cfg.primary.Condition),
	}
}
