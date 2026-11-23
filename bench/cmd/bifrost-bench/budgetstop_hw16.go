package main

import (
	"context"
	"encoding/json"
	"time"
)

// BudgetStop implements handlers.BudgetStopReporter (fix wave 3, N190): the
// evaluation page reads the scheduler's own budget stop (the budget_stop
// event, recorded in the run job's summary) instead of guessing it from the
// spend, so a stop before a run whose expected cost did not fit shows
// "Raise budget and continue" too. known is false when the experiment has
// no finished run job.
func (l *launcher) BudgetStop(expID string) (stopped bool, nextRunUSD float64, known bool) {
	if l.jobs == nil {
		return false, 0, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	j, err := l.jobs.Latest(ctx, l.org, kindExperimentRun, expTarget(expID))
	if err != nil || j == nil || !j.Status.Terminal() {
		return false, 0, false
	}
	var res expRunResult
	if len(j.Result) == 0 || json.Unmarshal(j.Result, &res) != nil || res.Summary == nil {
		return false, 0, false
	}
	return res.Summary.BudgetStopped || res.Summary.GatewayBudgetStopped, res.Summary.NextRunUSD, true
}
