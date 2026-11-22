package bifrost

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Per-evaluation budgets (lane C06). Each evaluation gets one Bifrost
// governance customer, named evalCustomerPrefix+the evaluation id, that
// carries the evaluation's hard budget (budget.total_usd) and, when set, a
// request rate limit (budget.requests_per_minute). Every run key of the
// evaluation is minted under that customer, so Bifrost itself refuses model
// calls once the evaluation has spent its budget, whatever the bench's own
// spend model says. The customer is deleted when the evaluation's scheduler
// call ends. Team runs keep their per-run team (team.go); the team is put
// under the evaluation's customer, so the evaluation cap holds for them too.

// evalCustomerPrefix names the evaluation customers the bench creates.
const evalCustomerPrefix = "heimdall-eval-"

// BifrostBudgetStopMessage is what an evaluation shows when Bifrost's
// governance budget stopped it.
const BifrostBudgetStopMessage = "stopped by Bifrost budget"

var _ schemas.EvaluationBudgetGateway = (*Gateway)(nil)

// OpenEvaluationBudget implements schemas.EvaluationBudgetGateway. It
// creates the evaluation's customer with a hard budget of usd and, when rpm
// > 0, a rate limit of rpm requests per minute, and remembers it so MintKey
// puts the evaluation's run keys under it. A second call for the same
// evaluation reuses the customer it made.
func (g *Gateway) OpenEvaluationBudget(ctx context.Context, evalID string, usd float64, rpm int) (string, error) {
	if evalID == "" {
		return "", errors.New("open evaluation budget: no evaluation id")
	}
	if usd <= 0 {
		return "", fmt.Errorf("open evaluation budget %s: budget must be above 0 USD", evalID)
	}
	if id := g.evalCustomerID(evalID); id != "" {
		return id, nil
	}
	body := map[string]any{
		"name":    evalCustomerPrefix + evalID,
		"budgets": []map[string]any{{"max_limit": usd, "reset_duration": budgetReset}},
	}
	if rpm > 0 {
		body["rate_limit"] = map[string]any{"request_max_limit": rpm, "request_reset_duration": "1m"}
	}
	var out struct {
		Customer struct {
			ID string `json:"id"`
		} `json:"customer"`
	}
	if err := g.call(ctx, http.MethodPost, "/api/governance/customers", body, &out); err != nil {
		return "", fmt.Errorf("open evaluation budget %s: create customer: %w", evalID, err)
	}
	if out.Customer.ID == "" {
		return "", fmt.Errorf("open evaluation budget %s: bifrost returned no customer id", evalID)
	}
	g.mu.Lock()
	if g.evalCust == nil {
		g.evalCust = map[string]string{}
	}
	g.evalCust[evalID] = out.Customer.ID
	g.mu.Unlock()
	g.logger.Info("evaluation budget opened", "evaluation", evalID, "customer_id", out.Customer.ID,
		"budget_usd", usd, "requests_per_minute", rpm)
	return out.Customer.ID, nil
}

// EvaluationBudgetSpent implements schemas.EvaluationBudgetGateway: the
// evaluation customer's budget usage as Bifrost counts it, and whether that
// budget is used up. An evaluation with no open budget reports zero.
func (g *Gateway) EvaluationBudgetSpent(ctx context.Context, evalID string) (spent, limit float64, exhausted bool, err error) {
	id := g.evalCustomerID(evalID)
	if id == "" {
		return 0, 0, false, nil
	}
	var out struct {
		Customer struct {
			Budgets []struct {
				MaxLimit     float64 `json:"max_limit"`
				CurrentUsage float64 `json:"current_usage"`
			} `json:"budgets"`
		} `json:"customer"`
	}
	if err := g.call(ctx, http.MethodGet, "/api/governance/customers/"+url.PathEscape(id), nil, &out); err != nil {
		return 0, 0, false, fmt.Errorf("evaluation budget %s: read customer %s: %w", evalID, id, err)
	}
	for _, b := range out.Customer.Budgets {
		spent, limit = b.CurrentUsage, b.MaxLimit
		if b.MaxLimit > 0 && b.CurrentUsage >= b.MaxLimit {
			return spent, limit, true, nil
		}
	}
	return spent, limit, false, nil
}

// CloseEvaluationBudget implements schemas.EvaluationBudgetGateway. It
// deletes the evaluation's customer; one already gone counts as deleted.
func (g *Gateway) CloseEvaluationBudget(ctx context.Context, evalID string) error {
	id := g.evalCustomerID(evalID)
	if id == "" {
		return nil
	}
	err := g.call(ctx, http.MethodDelete, "/api/governance/customers/"+url.PathEscape(id), nil, nil)
	if err != nil && !isStatus(err, http.StatusNotFound) {
		return fmt.Errorf("close evaluation budget %s: delete customer %s: %w", evalID, id, err)
	}
	g.mu.Lock()
	delete(g.evalCust, evalID)
	g.mu.Unlock()
	return nil
}

// evalCustomerID is the open customer of an evaluation, or "".
func (g *Gateway) evalCustomerID(evalID string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.evalCust[evalID]
}

// runEvalCustomer is the customer a run's keys belong to: its evaluation's,
// when one is open.
func (g *Gateway) runEvalCustomer(rc *schemas.RunContext) string {
	if rc == nil || rc.Run == nil || rc.Run.ExpID == "" {
		return ""
	}
	return g.evalCustomerID(rc.Run.ExpID)
}

// IsBifrostBudgetStop reports whether a Bifrost answer is a governance budget
// refusal: status 402, or the governance plugin's budget_exceeded type or
// "budget exceeded" reason in the body.
func IsBifrostBudgetStop(status int, body string) bool {
	if status == http.StatusPaymentRequired {
		return true
	}
	b := strings.ToLower(body)
	return strings.Contains(b, "budget_exceeded") || strings.Contains(b, "budget exceeded")
}
