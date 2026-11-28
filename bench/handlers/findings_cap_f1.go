package handlers

import "github.com/maximhq/bifrost/bench/schemas"

// Lane F1: "Check fix" starts paid runs, so it takes a spending cap.

// findingsMaxRerunUSD is the highest cap a Check fix may ask for.
const findingsMaxRerunUSD = 50.0

const findingsBadCap = "Set a spending cap between $0 and $50."

// capRerunBudget sets the rerun's total cap to usd when one was given; the
// per-run reserve never exceeds the cap.
func capRerunBudget(e *schemas.Experiment, usd float64) {
	if usd <= 0 {
		return
	}
	e.Budget.TotalUSD = usd
	if e.Budget.ReserveUSD > usd {
		e.Budget.ReserveUSD = usd
	}
}
