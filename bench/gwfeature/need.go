package gwfeature

import (
	"fmt"
	"math"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The least data each feature test's verdict rules need, stated on the start
// form before anything is paid for (lane H2). The numbers are the rules'
// own constants: GuardMinRuns (store.go), budgetMinRuns (budget.go),
// MinTasks (paired.go), mcpPoisonMinRuns (mcptools.go) and overheadMinRows
// (overhead.go).

// DefaultCallsPerRun is the model calls one run makes when this agent has no
// measured history: the live overhead test of 2026-11-28 made 12 calls in 3
// runs.
const DefaultCallsPerRun = 4.0

// Need is what a feature's result needs, and the smallest size that reaches it.
type Need struct {
	Feature      string  `json:"feature"`
	RunsPerSetup int     `json:"runs_per_setup,omitempty"` // runs on every setup (tasks times trials)
	PairedTasks  int     `json:"paired_tasks,omitempty"`   // tasks with results on both setups
	ModelCalls   int     `json:"model_calls,omitempty"`    // model calls with Bifrost's time breakdown
	PoisonRuns   int     `json:"poison_runs,omitempty"`    // poison probe runs per setup, one per task
	FixedTrials  int     `json:"fixed_trials,omitempty"`   // the test always runs this many trials
	MaxTasks     int     `json:"max_tasks,omitempty"`      // the test runs at most this many tasks
	CallsPerRun  float64 `json:"calls_per_run,omitempty"`  // model calls per run used for ModelCalls
	CallsFrom    string  `json:"calls_from,omitempty"`     // history | default
	Tasks        int     `json:"default_tasks"`            // smallest size that reaches the need
	Trials       int     `json:"default_trials"`
}

// NeedCheck is a Need against one chosen size.
type NeedCheck struct {
	Need
	ChosenTasks   int    `json:"tasks"`
	ChosenTrials  int    `json:"trials"`
	ExpectedCalls int    `json:"expected_calls,omitempty"`
	Status        string `json:"status"`            // ok | unlikely | impossible
	Line          string `json:"line"`              // what the result needs, for the form
	Problem       string `json:"problem,omitempty"` // why this size cannot or may not reach it
}

// NeedOf is the need of a feature. callsPerRun at or below 0 uses
// DefaultCallsPerRun.
func NeedOf(feature string, callsPerRun float64) Need {
	n := Need{Feature: feature, Tasks: MinTasks, Trials: 2}
	switch feature {
	case schemas.GatewayFeatureOverhead:
		n.CallsFrom = "history"
		if !(callsPerRun > 0) {
			callsPerRun, n.CallsFrom = DefaultCallsPerRun, "default"
		}
		n.ModelCalls, n.FixedTrials, n.MaxTasks, n.CallsPerRun = overheadMinRows, 1, overheadMaxTasks, callsPerRun
		n.Trials = 1
		n.Tasks = min(int(math.Ceil(float64(overheadMinRows)/callsPerRun)), overheadMaxTasks)
		return n
	case schemas.GatewayFeatureBudgetCurve:
		n.RunsPerSetup = budgetMinRuns
	case schemas.GatewayFeatureSemanticCache:
		n.RunsPerSetup = GuardMinRuns
	case schemas.GatewayFeatureMCPTools:
		n.RunsPerSetup, n.PairedTasks, n.PoisonRuns = GuardMinRuns, MinTasks, mcpPoisonMinRuns
		n.Tasks, n.Trials = mcpPoisonMinRuns, 1 // the poison probe runs one trial per task
	case schemas.GatewayFeatureComplexityRouter, schemas.GatewayFeaturePromptVersion, schemas.GatewayFeatureSkillVersion, schemas.GatewayFeatureProviders:
		n.RunsPerSetup, n.PairedTasks = GuardMinRuns, MinTasks
	default: // a feature this file does not know states no need
		return Need{Feature: feature}
	}
	for n.Tasks*n.Trials < n.RunsPerSetup {
		n.Tasks++
	}
	return n
}

// Check states the need for a size. A size that cannot reach a runs or tasks
// need is impossible: the number of runs is fixed by the size. A size whose
// expected model calls fall short is only unlikely: an agent may make more
// calls than measured. The poison rate is a side line, so too few poison
// runs is unlikely, not impossible.
func (n Need) Check(tasks, trials int, poison bool) NeedCheck {
	if n.FixedTrials > 0 {
		trials = n.FixedTrials
	}
	if trials < 1 {
		trials = 1
	}
	if n.MaxTasks > 0 && tasks > n.MaxTasks {
		tasks = n.MaxTasks
	}
	c := NeedCheck{Need: n, ChosenTasks: tasks, ChosenTrials: trials, Status: "ok"}
	if n.RunsPerSetup == 0 && n.ModelCalls == 0 {
		return c
	}
	if n.ModelCalls > 0 {
		c.ExpectedCalls = int(math.Round(float64(tasks) * n.CallsPerRun))
		c.Line = fmt.Sprintf("Needs about %d model calls: about %d tasks with this agent.", n.ModelCalls, n.Tasks)
		if c.ExpectedCalls < n.ModelCalls {
			c.Status = "unlikely"
			c.Problem = fmt.Sprintf("%d tasks make about %d of the %d model calls needed. Pick %d tasks.", tasks, c.ExpectedCalls, n.ModelCalls, n.Tasks)
		}
		return c
	}
	c.Line = fmt.Sprintf("Needs %d runs per setup: %d tasks, %s.", n.RunsPerSetup, n.Tasks, trialWord(n.Trials))
	if n.PairedTasks > 0 && tasks < n.PairedTasks {
		c.Status = "impossible"
		c.Problem = fmt.Sprintf("%d tasks cannot give a result: %d are needed.", tasks, n.PairedTasks)
		return c
	}
	if tasks*trials < n.RunsPerSetup {
		c.Status = "impossible"
		c.Problem = fmt.Sprintf("%d runs per setup cannot give a result: %d are needed.", tasks*trials, n.RunsPerSetup)
		return c
	}
	if poison && n.PoisonRuns > 0 && tasks < n.PoisonRuns {
		c.Status = "unlikely"
		c.Problem = fmt.Sprintf("The poison rate needs %d tasks, %d are picked.", n.PoisonRuns, tasks)
	}
	return c
}

// CallsPerRun is the mean model calls per sealed run over this agent's past
// gateway tests, newest experiments first, and 0 when there are none.
func CallsPerRun(src leaderboard.Source, base string) float64 {
	if src == nil || base == "" {
		return 0
	}
	calls, runs := 0, 0
	ids := src.Experiments()
	for i := len(ids) - 1; i >= 0 && runs < 200; i-- {
		m, ok := src.Manifest(ids[i])
		if !ok || m == nil || m.Experiment.Base != base || m.Experiment.GatewayTest == nil {
			continue
		}
		rs, err := LoadRuns(src, ids[i], false)
		if err != nil {
			continue
		}
		for _, r := range rs {
			if !r.RowsComplete {
				continue
			}
			n := 0
			for _, row := range r.Rows {
				if row.Kind == "message" {
					n++
				}
			}
			if n > 0 {
				calls, runs = calls+n, runs+1
			}
		}
	}
	if runs == 0 {
		return 0
	}
	return float64(calls) / float64(runs)
}

func trialWord(n int) string {
	if n == 1 {
		return "1 trial"
	}
	return fmt.Sprintf("%d trials", n)
}
