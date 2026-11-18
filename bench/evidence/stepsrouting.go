package evidence

import (
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// modelSwitch (W9, docs/routing-experiments.md) returns the model switch of
// one logical model turn and the model that answered it: "<prev> to <now>"
// when a successful row of the turn was served by another model than the
// previous answered turn, else "". A turn nothing answered keeps prev, so a
// failed turn between two answers never hides or invents a switch. Model ids
// compare without one leading provider segment. It is pure.
func modelSwitch(prev string, rows []schemas.GatewayRequest) (string, string) {
	served := ""
	for _, r := range rows {
		if r.Status == statusSuccess && r.ModelServed != "" {
			served = r.ModelServed
		}
	}
	if served == "" {
		return "", prev
	}
	if prev == "" || bareModel(prev) == bareModel(served) {
		return "", served
	}
	return prev + " to " + served, served
}

// bareModel drops one leading provider segment ("anthropic/claude-x").
func bareModel(m string) string {
	if _, rest, ok := strings.Cut(m, "/"); ok {
		return rest
	}
	return m
}
