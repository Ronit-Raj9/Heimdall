package schemas

import "context"

// EvaluationBudgetGateway is a gateway that can enforce an evaluation's
// budget itself (C06): one governance owner per evaluation with a hard
// budget and an optional request rate limit, that every run key of the
// evaluation is minted under. The scheduler opens it when an evaluation
// starts and closes it when its scheduler call ends.
type EvaluationBudgetGateway interface {
	// OpenEvaluationBudget creates the evaluation's budget owner and returns
	// its gateway id.
	OpenEvaluationBudget(ctx context.Context, evalID string, usd float64, requestsPerMinute int) (string, error)
	// EvaluationBudgetSpent is the gateway's own count of the evaluation's
	// spend, its limit, and whether the budget is used up.
	EvaluationBudgetSpent(ctx context.Context, evalID string) (spent, limit float64, exhausted bool, err error)
	// CloseEvaluationBudget deletes the evaluation's budget owner.
	CloseEvaluationBudget(ctx context.Context, evalID string) error
}
