package handlers

import (
	"errors"

	"github.com/maximhq/bifrost/bench/lib"
)

// keyRefusalCode is the error code of a model key refusal (fix wave M1-44):
// budget_reached for a key past its budget, no_model_key otherwise.
func keyRefusalCode(err error) string {
	if errors.Is(err, ErrKeyOverBudget) {
		return lib.ErrCodeBudgetReached
	}
	return lib.ErrCodeNoModelKey
}
