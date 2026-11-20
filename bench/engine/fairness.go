package engine

import (
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// checkFairness enforces the experiment's fairness rule on the resolved arms
// before anything can start (docs/multi-agent.md). Under equal_budget every
// arm needs a total USD cap (budgets.usd, enforced at the gateway on the
// run's key across all of the arm's agents) and all caps must be equal; a
// team arm's member sub-budgets must fit under its cap, or one member's
// sub-budget would buy spend the cap was meant to bound. An unknown rule is
// refused rather than ignored. It then warns when the arms lack the
// baselines a "multi-agent is better" claim needs: a single-agent arm and a
// best-of-N single-agent arm at the same spend.
func checkFairness(e *schemas.Experiment, arms map[string]schemas.ArmManifest, logger schemas.Logger) error {
	switch e.Fairness {
	case "":
		return nil
	case schemas.FairnessEqualBudget:
	default:
		return fmt.Errorf("fairness %q is not a known rule (known: %s)", e.Fairness, schemas.FairnessEqualBudget)
	}
	ids := sortedKeys(arms)
	var errs []error
	caps := make([]string, 0, len(ids))
	distinct := map[float64]bool{}
	for _, id := range ids {
		cfg := arms[id].Config
		limit := cfg.Budgets.USD
		caps = append(caps, fmt.Sprintf("%s=%.2f", id, limit))
		distinct[limit] = true
		if limit <= 0 {
			errs = append(errs, fmt.Errorf("fairness %s: arm %s has no total USD cap (budgets.usd)", e.Fairness, id))
			continue
		}
		if r := cfg.Gateway.Routing; r != nil && r.Limits != nil && r.Limits.BudgetUSD > 0 { // 3-A-09: one USD limit per run key; the fairness cap would replace the routing treatment's
			errs = append(errs, fmt.Errorf("fairness %s: arm %s sets gateway.routing.limits.budget_usd %.2f, but the run key's USD limit is the fairness cap budgets.usd %.2f; drop one of them", e.Fairness, id, r.Limits.BudgetUSD, limit))
		}
		if sum := utils.ArmMemberBudgets(&cfg); sum > limit+fairnessTolerance {
			errs = append(errs, fmt.Errorf("fairness %s: arm %s member sub-budgets sum to %.2f USD, above its total cap %.2f", e.Fairness, id, sum, limit))
		}
	}
	if len(distinct) > 1 {
		errs = append(errs, fmt.Errorf("fairness %s: arms differ in total USD cap: %s", e.Fairness, strings.Join(caps, " ")))
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	single, bestOfN := fairnessBaselines(arms)
	if logger != nil {
		if !single || !bestOfN {
			logger.Warn("fairness equal_budget: a claim that multi-agent is better needs a single-agent baseline and a best-of-N single-agent baseline at the same spend",
				"experiment", e.ID, "single_agent_baseline", single, "best_of_n_baseline", bestOfN, "caps_usd", strings.Join(caps, " "))
		} else {
			logger.Info("fairness equal_budget: arms share one total USD cap, both baselines present", "experiment", e.ID, "caps_usd", strings.Join(caps, " "))
		}
	}
	return nil
}

// fairnessTolerance absorbs float summing error in member sub-budgets.
const fairnessTolerance = 1e-9

// fairnessBaselines reports whether some arm is a single-agent baseline
// (topology single, or no topology and no native or A2A team,
// utils.ArmTopology) and whether some arm is a best-of-N baseline (topology
// best_of_n).
func fairnessBaselines(arms map[string]schemas.ArmManifest) (single, bestOfN bool) {
	for _, a := range arms {
		cfg := a.Config
		switch utils.ArmTopology(&cfg) {
		case schemas.TopologySingle:
			single = true
		case schemas.TopologyBestOfN:
			bestOfN = true
		}
	}
	return single, bestOfN
}
