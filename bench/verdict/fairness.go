package verdict

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// InstrumentFairnessEqualBudget is the instrument check of an experiment that
// declared fairness: equal_budget. A failed check makes the experiment
// INVALID: a comparison where some run spent past its arm's cap, or where the
// arms had different caps, is not at equal spend.
const InstrumentFairnessEqualBudget = "fairness_equal_budget"

// MetricBudgetExceeded is the per-run metric that says whether a run's cost
// went over its arm's total USD cap (bench/metrics multiagent.go).
const MetricBudgetExceeded = "budget_exceeded"

// fairnessTolerance absorbs float summing error, so a run that spent its cap
// exactly is not over it.
const fairnessTolerance = 1e-9

// fairnessFor builds the verdict's fairness report and its instrument check
// for an experiment that declared a fairness rule; an experiment without one
// gets neither, so its verdict stays byte-identical. Under equal_budget the
// check fails when the arms' caps are missing or differ (the planner refuses
// that, so only an edited manifest gets here) or when any counted run that
// started went over its arm's cap; it is unknown when no run went over but
// some run's spend against its cap could not be read. A claim that
// multi-agent is better is allowed only with a single-agent and a best-of-N
// baseline arm and a passing check.
func fairnessFor(m *schemas.Manifest, idx runIndex) (*schemas.FairnessReport, []schemas.InstrumentCheck) {
	if m == nil || m.Experiment.Fairness == "" {
		return nil, nil
	}
	rule := m.Experiment.Fairness
	rep := &schemas.FairnessReport{Rule: rule, CapsUSD: map[string]float64{}, Topologies: map[string]string{}}
	c := schemas.InstrumentCheck{Name: InstrumentFairnessEqualBudget, Threshold: "every arm has the same total USD cap above 0; no run's cost above its arm's cap"}
	if rule != schemas.FairnessEqualBudget {
		c.Status = schemas.InstrumentFail
		c.Detail = fmt.Sprintf("fairness %q is not a known rule", rule)
		rep.Notes = append(rep.Notes, c.Detail)
		return rep, []schemas.InstrumentCheck{c}
	}
	problems := fairnessCaps(m, rep)
	c.PerArm = map[string]schemas.Ratio{}
	unknown, runs := 0, 0
	for _, arm := range sortedKeys(m.Arms) {
		limit := rep.CapsUSD[arm]
		var r schemas.Ratio
		if limit <= 0 {
			c.PerArm[arm] = r // no cap to compare with: already a problem above
			continue
		}
		for _, run := range idx.armRuns("", arm) {
			if !agentRan(run) {
				continue
			}
			r.Den++
			over, known := runOverCap(run, limit)
			switch {
			case !known:
				r.Unknown++
			case over:
				r.Num++
				rep.Exceeded = append(rep.Exceeded, run.Run.RunID)
				problems = append(problems, fmt.Sprintf("fairness %s: run %s exceeded its arm's cap (arm %s, %.2f USD)", rule, run.Run.RunID, arm, limit))
			}
		}
		c.PerArm[arm] = r
		unknown, runs = unknown+r.Unknown, runs+r.Den
	}
	switch {
	case len(problems) > 0:
		c.Status, c.Detail = schemas.InstrumentFail, strings.Join(problems, "; ")
	case unknown > 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("fairness %s: spend against the cap unknown in %d of %d runs; none exceeded it", rule, unknown, runs)
		rep.Notes = append(rep.Notes, c.Detail)
	default:
		c.Status = schemas.InstrumentPass
		c.Detail = fmt.Sprintf("fairness %s: no run exceeded its arm's cap (%d runs)", rule, runs)
	}
	rep.SingleAgentBaseline, rep.BestOfNBaseline = fairnessBaselines(m.Arms)
	if !rep.SingleAgentBaseline {
		rep.Notes = append(rep.Notes, "no single-agent baseline arm (topology single, or no team)")
	}
	if !rep.BestOfNBaseline {
		rep.Notes = append(rep.Notes, "no best-of-N single-agent baseline arm (topology best_of_n)")
	}
	rep.ClaimAllowed = rep.SingleAgentBaseline && rep.BestOfNBaseline && c.Status == schemas.InstrumentPass
	return rep, []schemas.InstrumentCheck{c}
}

// fairnessCaps fills the report's caps and topologies from the manifest arms
// and returns a problem for each arm without a cap and for caps that differ.
// An arm's topology is the one it declared, else team for an arm with a team
// and single for one without.
func fairnessCaps(m *schemas.Manifest, rep *schemas.FairnessReport) []string {
	var problems, caps []string
	distinct := map[float64]bool{}
	for _, arm := range sortedKeys(m.Arms) {
		cfg := m.Arms[arm].Config
		limit := cfg.Budgets.USD
		rep.CapsUSD[arm] = limit
		rep.Topologies[arm] = topologyOf(cfg)
		caps = append(caps, fmt.Sprintf("%s=%.2f", arm, limit))
		distinct[limit] = true
		if limit <= 0 {
			problems = append(problems, fmt.Sprintf("fairness %s: arm %s has no total USD cap (budgets.usd)", rep.Rule, arm))
		}
	}
	if len(distinct) > 1 {
		problems = append(problems, fmt.Sprintf("fairness %s: arms differ in total USD cap: %s", rep.Rule, strings.Join(caps, " ")))
	}
	return problems
}

// topologyOf is an arm's declared topology, else team or single by whether
// it has a team.
func topologyOf(cfg schemas.AgentConfig) string {
	switch {
	case cfg.Topology != "":
		return cfg.Topology
	case len(cfg.Team) > 0:
		return schemas.TopologyTeam
	default:
		return schemas.TopologySingle
	}
}

// fairnessBaselines reports whether some arm is a single-agent baseline
// (topology single, or no topology and no team) and whether some arm is a
// best-of-N baseline.
func fairnessBaselines(arms map[string]schemas.ArmManifest) (single, bestOfN bool) {
	for _, a := range arms {
		switch topologyOf(a.Config) {
		case schemas.TopologySingle:
			single = true
		case schemas.TopologyBestOfN:
			bestOfN = true
		}
	}
	return single, bestOfN
}

// runOverCap reports whether a run spent past limit and whether that is
// known. The run's budget_exceeded metric decides when observed; its cost_usd
// decides too when it, or its bounds, lie wholly on one side of the cap.
// Either one saying over is enough.
func runOverCap(run schemas.SealedRun, limit float64) (over, known bool) {
	if v, ok := run.Metrics[MetricBudgetExceeded].Float(); ok {
		over, known = v >= 1, true
	}
	cost := run.Metrics[MetricCostUSD]
	if v, ok := cost.Float(); ok {
		return over || v > limit+fairnessTolerance, true
	}
	if b := cost.Bounds; b != nil && cost.State == schemas.ValueUnknown {
		switch {
		case b[0] > limit+fairnessTolerance:
			return true, true
		case b[1] <= limit+fairnessTolerance:
			return over, true
		}
	}
	return over, known
}
