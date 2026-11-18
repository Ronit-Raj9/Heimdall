package marble

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// experimentCapUSD is the total USD cap every arm of the template shares.
// Scripted controls spend nothing; it only has to be the same on every arm.
const experimentCapUSD = 1

// writeExperiment writes experiments/equal-budget.yaml in the suite folder:
// the multi-agent comparison docs/multi-agent.md asks for, at equal spend
// (fairness: equal_budget), with the single-agent baseline as arm A, the
// team as arm B, a best-of-N baseline and a broken team beside them. It
// uses the scripted controls, so it runs at no cost: the oracle runs the
// task's reference (alone, or as every member of the task's team), noop
// does nothing. A real comparison swaps in real agent configs.
func (w *writer) writeExperiment(tasks []string) error {
	dir := filepath.Join(w.suiteDir, "experiments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	quoted := make([]string, len(tasks))
	for i, t := range tasks {
		quoted[i] = q(t)
	}
	var b strings.Builder
	p := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }
	p("# Written by `bifrost-bench import marble` (docs/importers-marble.md).\n")
	p("# A team against its baselines at one total USD cap (fairness: equal_budget,\n")
	p("# docs/multi-agent.md): arm A runs one agent alone (topology single), arm B\n")
	p("# runs the task's team, every member the arm's agent (topology team). The\n")
	p("# verdict compares A and B; bon is the best-of-N baseline a \"multi-agent is\n")
	p("# better\" claim also needs, and broken is a team that must fail.\n")
	p("#\n")
	p("# With the scripted controls below it costs nothing and is a pipeline check,\n")
	p("# not a finding: the oracle runs the task's reference, noop does nothing.\n")
	p("# The bench has no generic best-of-N runner, so here bon is one oracle\n")
	p("# attempt labelled best_of_n; a real best_of_n arm's agent must sample N\n")
	p("# attempts and pick one by itself.\n")
	p("name: %s\n", q(w.suiteID+"-equal-budget"))
	p("suite: %s\n", q(w.suiteID+"@"+w.set.version))
	p("base: agents/oracle.yaml\n")
	p("fairness: equal_budget\n")
	p("arms:\n")
	p("  A: {label: single-agent baseline, set: {topology: single, budgets.usd: %d}}\n", experimentCapUSD)
	p("  B: {label: team, set: {topology: team, budgets.usd: %d}}\n", experimentCapUSD)
	p("  bon: {label: best-of-N baseline (one scripted attempt), set: {topology: best_of_n, budgets.usd: %d}}\n", experimentCapUSD)
	p("  broken: {label: broken team (every member does nothing), agent: agents/noop.yaml, set: {topology: team, budgets.usd: %d}}\n", experimentCapUSD)
	p("primary: {condition: clean, metric: verified_success}\n")
	p("guards:\n  - {rule: no_new_integrity_violation}\n")
	p("conditions:\n  - {id: clean, tasks: [%s], trials: 1}\n", strings.Join(quoted, ", "))
	p("budget: {total_usd: 0}\n")
	p("verdict: {policy: paired_signflip, alpha: 0.05, assumed_sd: 0.30}\n")
	p("seed: 20261118\n")
	p("concurrency: 1\n")
	p("manifest_mode: agent_exposure\n")
	return os.WriteFile(filepath.Join(dir, "equal-budget.yaml"), []byte(b.String()), 0o644)
}
