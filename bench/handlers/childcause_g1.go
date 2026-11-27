package handlers

import (
	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/schemas"
)

// fillChildCauses names why each skipped benchmark of ev was skipped
// (EvaluationChild.Cause) from its Skipped message.
func fillChildCauses(ev *schemas.Evaluation) {
	for i := range ev.Children {
		if c := &ev.Children[i]; c.Skipped != "" {
			c.Cause = engine.FailureCause(c.Skipped)
		}
	}
}
