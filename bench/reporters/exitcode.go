package reporters

import (
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Exit codes of `bifrost-bench check` (DESIGN.md 16.8).
const (
	ExitOK           = 0
	ExitRegressed    = 1
	ExitInconclusive = 2
	ExitInvalid      = 3
	// ExitInfra is an infrastructure error. The caller returns it; a verdict
	// never maps to it.
	ExitInfra = 4
)

// ExitCode maps a decision to the check exit code: 0 when the decision is not
// in failOn, otherwise 1 regressed, 2 inconclusive, 3 invalid. A listed
// decision with no code of its own (improved, or an unrecognized value) is a
// failure the caller asked for, reported as 1.
func ExitCode(d schemas.Decision, failOn []schemas.Decision) int {
	if !slices.Contains(failOn, d) {
		return ExitOK
	}
	switch d {
	case schemas.DecisionInconclusive:
		return ExitInconclusive
	case schemas.DecisionInvalid:
		return ExitInvalid
	default:
		return ExitRegressed
	}
}
