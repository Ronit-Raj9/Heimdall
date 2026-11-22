package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Per-evaluation gateway budgets (C06, gateways/bifrost/evalbudget.go). When
// the registered gateway can enforce an evaluation budget itself, Run opens
// one for the evaluation's spend cap before any slot is leased, so every run
// key is minted under it and the gateway refuses model calls past the cap.
// After each run (local, GPU worker, or a remote worker's upload, accepted
// or rejected) the scheduler asks the gateway whether the budget is
// used up; if so it stops leasing like the bench's own spend cap, and the
// summary and events say the evaluation was stopped by the gateway budget.

// gatewayBudgetStopMessage is the event text when the gateway budget stopped
// the evaluation.
const gatewayBudgetStopMessage = "stopped by Bifrost budget"

// evalBudgetTimeout bounds each gateway budget call.
const evalBudgetTimeout = 10 * time.Second

// openEvalBudget opens the evaluation's gateway budget and returns the
// function that closes it. A gateway without the feature, an evaluation
// without a spend cap, or a failed open (logged) leaves the run as before:
// the bench's own spend cap still applies.
func (r *schedRun) openEvalBudget(ctx context.Context) func() {
	gw, ok := r.s.reg.Gateway.(schemas.EvaluationBudgetGateway)
	usd := r.capUSD()
	if !ok || usd <= 0 || r.m == nil {
		return func() {}
	}
	octx, cancel := context.WithTimeout(ctx, evalBudgetTimeout)
	id, err := gw.OpenEvaluationBudget(octx, r.m.ExpID, usd, r.m.Experiment.Budget.RequestsPerMinute)
	cancel()
	if err != nil {
		r.s.logger.Warn("gateway evaluation budget not opened; the bench spend cap still applies", "experiment", r.m.ExpID, "error", err.Error())
		return func() {}
	}
	r.mu.Lock()
	r.evalBudget = gw
	r.sum.GatewayBudgetID = id
	r.mu.Unlock()
	return func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), evalBudgetTimeout)
		defer cancel()
		if err := gw.CloseEvaluationBudget(cctx, r.m.ExpID); err != nil {
			r.s.logger.Warn("gateway evaluation budget not closed", "experiment", r.m.ExpID, "error", err.Error())
		}
	}
}

// checkEvalBudget stops leasing once the gateway reports the evaluation's
// budget used up. It reports whether it stopped the run now.
func (r *schedRun) checkEvalBudget(ctx context.Context) bool {
	r.mu.Lock()
	gw, done := r.evalBudget, r.sum.GatewayBudgetStopped
	r.mu.Unlock()
	if gw == nil || done {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	cctx, cancel := context.WithTimeout(ctx, evalBudgetTimeout)
	spent, limit, exhausted, err := gw.EvaluationBudgetSpent(cctx, r.m.ExpID)
	cancel()
	if err != nil {
		r.s.logger.Warn("gateway evaluation budget not read", "experiment", r.m.ExpID, "error", err.Error())
		return false
	}
	if !exhausted {
		return false
	}
	r.mu.Lock()
	if r.sum.GatewayBudgetStopped {
		r.mu.Unlock()
		return false
	}
	r.sum.GatewayBudgetStopped = true
	r.sum.StopReason = gatewayBudgetStopMessage
	if !r.stopped {
		r.stopLocked()
	}
	r.mu.Unlock()
	r.s.logger.Warn("gateway evaluation budget used up", "experiment", r.m.ExpID, "spent_usd", spent, "limit_usd", limit)
	r.emit(Event{Kind: EventBudgetStop, Message: fmt.Sprintf("%s: the evaluation's Bifrost budget of %.2f USD is used up (%.4f USD spent as Bifrost counts it); no more runs are started", gatewayBudgetStopMessage, limit, spent)})
	return true
}

// budgetRefusedCalls counts the run's model calls Bifrost refused because a
// budget was used up (schemas.GatewayRequest.BudgetRefused, set by the
// Bifrost gateway from bifrost.IsBifrostBudgetStop).
func budgetRefusedCalls(log *schemas.RequestLog) int {
	if log == nil {
		return 0
	}
	n := 0
	for _, r := range log.Requests {
		if r.BudgetRefused {
			n++
		}
	}
	return n
}
