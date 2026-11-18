package leaderboard

import "github.com/maximhq/bifrost/bench/schemas"

// ReasonSimulatedUser is the exclusion reason of runs whose customer was
// played by a model (W3 customer support, docs/customer-support.md). The
// board ranks scripted-user runs, which are deterministic; a model-played
// user is a different, non-deterministic instrument, so its runs are never
// ranked beside scripted-user runs. Compare them inside one experiment
// (support scorecard) and check them with `simulator spread`.
const ReasonSimulatedUser = "model-played user (not comparable with scripted-user runs)"

// simulatedUserReason returns ReasonSimulatedUser for a record of a plan
// with an llm user simulator, "" otherwise.
func simulatedUserReason(r *record) string {
	if r.exp != nil && r.exp.m != nil && r.exp.m.Simulator != nil && r.exp.m.Simulator.Mode == schemas.SimulatorLLM {
		return ReasonSimulatedUser
	}
	return ""
}
