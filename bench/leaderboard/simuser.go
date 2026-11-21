package leaderboard

import (
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ReasonSimulatedUser is the exclusion reason of runs whose customer was
// played by a model (W3 customer support, docs/customer-support.md) on the
// default board, which ranks scripted-user runs, which are deterministic. A
// model-played user is a different, non-deterministic instrument, so its
// runs are never ranked beside scripted-user runs: they get their own
// board, one per simulator pin (Query.Customer simulated, Query.Simulator),
// labelled "simulated customer".
const ReasonSimulatedUser = "model-played user (not comparable with scripted-user runs)"

// Board customers (Query.Customer).
const (
	CustomerScripted  = "scripted"  // default: scripted-user and user-less runs
	CustomerSimulated = "simulated" // runs whose customer a pinned model played
)

// SimulatedCustomerLabel is the plain-words label of a simulated-customer
// board and its rows.
const SimulatedCustomerLabel = "simulated customer"

// SimulatorPin names the user simulator of a plan with an llm user
// simulator: provider/model@prompt_version, the parts every run of the pin
// shares. It is "" for a scripted or user-less plan.
func SimulatorPin(m *schemas.Manifest) string {
	if m == nil || m.Simulator == nil || m.Simulator.Mode != schemas.SimulatorLLM {
		return ""
	}
	s := m.Simulator
	pin := s.Model
	if s.Provider != "" {
		pin = s.Provider + "/" + pin
	}
	if s.PromptVersion != "" {
		pin += "@" + s.PromptVersion
	}
	if pin == "" {
		pin = schemas.SimulatorLLM
	}
	return pin
}

// SimulatorPins lists the simulator pins of src's experiments on suite
// (id@version), sorted.
func SimulatorPins(src Source, suite string) []string {
	var out []string
	for _, id := range src.Experiments() {
		m, ok := src.Manifest(id)
		if !ok || m == nil || m.SuiteID+"@"+m.SuiteVersion != suite {
			continue
		}
		if pin := SimulatorPin(m); pin != "" && !slices.Contains(out, pin) {
			out = append(out, pin)
		}
	}
	slices.Sort(out)
	return out
}

// simulatedUserReason returns ReasonSimulatedUser for a record of a plan
// with an llm user simulator, "" otherwise.
func simulatedUserReason(r *record) string {
	if r.exp != nil && SimulatorPin(r.exp.m) != "" {
		return ReasonSimulatedUser
	}
	return ""
}

// onCustomerBoard reports whether r belongs on the board of q: a simulated
// board keeps only runs of its simulator pin (any pin when q.Simulator is
// empty); the default board keeps every run (excludeReason then lists the
// model-played ones as excluded).
func onCustomerBoard(r *record, q Query) bool {
	if q.Customer != CustomerSimulated {
		return true
	}
	pin := ""
	if r.exp != nil {
		pin = SimulatorPin(r.exp.m)
	}
	return pin != "" && (q.Simulator == "" || pin == q.Simulator)
}
