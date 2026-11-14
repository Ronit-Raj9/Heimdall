// Package gates holds the deterministic integrity gates (docs/DESIGN.md 11).
//
// A gate reads only the run context and the collected evidence: the exported
// diff (its file list and the diff text in the run folder), the grade, the
// claim, the gateway rows and the manifest. It never looks inside the agent's
// container, so an agent cannot talk its way past a gate. Gates that read the
// diff fail with an error when the diff is missing rather than passing a run
// they could not inspect.
package gates

import "github.com/maximhq/bifrost/bench/schemas"

// version is shared by every gate in this package. Bump it with any change to
// a gate's firing rule so results from different rules are never compared.
const version = "1.0.0"

// Builtin returns the gates that ship with the bench, baseline gates first.
// Each call builds new instances so callers never share state.
func Builtin() []schemas.Gate {
	return []schemas.Gate{
		newProtectedPathsGate(),
		newTestInfraGate(),
		newRegradeGate(),
		newNullAgentsGate(),
		newEgressGate(),
		newUpstreamGate(),
		newFalseCompletionGate(),
		newModelFidelityGate(),
		newDestroyedWorkGate(),
		newProtectedProbeGate(),
		newInjectionGate(),
		newMemoryTrapGate(),
	}
}

// BaselineNames returns the names of the gates no experiment can switch off,
// in Builtin order.
func BaselineNames() []string {
	var names []string
	for _, g := range Builtin() {
		if g.Baseline() {
			names = append(names, g.GetName())
		}
	}
	return names
}

// newResult builds a gate result stamped with the gate's name, version and
// effect, so no gate can report under another gate's identity.
func newResult(g schemas.Gate, fired bool, detail string, evidence []string) *schemas.GateResult {
	return &schemas.GateResult{
		Gate:     g.GetName(),
		Version:  g.GetVersion(),
		Effect:   g.Effect(),
		Fired:    fired,
		Detail:   detail,
		Evidence: evidence,
	}
}
