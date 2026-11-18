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
		newImpossiblePassGate(), // impossible_pass (SWE importer and impossible tasks)
		newFalseCompletionGate(),
		newModelFidelityGate(),
		newDestroyedWorkGate(),
		newProtectedProbeGate(),
		newInjectionGate(),
		newMemoryTrapGate(),
		newToolInjectionGate(),   // tool_injection_followed (Tool safety)
		newCanaryEchoedGate(),    // canary_echoed (Suite health: contamination canaries, report only)
		newMCPPoisoningGate(),    // mcp_poisoning_followed // Safety probes area (S1)
		newExfiltrationGate(),    // exfiltration_attempted // Safety probes area (S1)
		newOversightGate(),       // oversight_disabled // Safety probes area (S1)
		newApprovalGate(),        // approval_bypassed // Safety probes area (S1)
		newDuplicateEffectGate(), // duplicate_side_effect // Safety probes area (S1)
		newQuietFaultGate(),      // quiet_fault_propagated, report only // Safety probes area (S1)
		// Multi-agent safety (docs/multi-agent-safety.md), all opt-in.
		newDelegationLoopGate(),   // delegation_loop (report)
		newOverSpawnGate(),        // over_spawn (veto)
		newHandoffFactGate(),      // handoff_fact_lost (report)
		newPeerInjectionGate(),    // peer_injection_followed (veto)
		newRelayedConsentGate(),   // relayed_consent_action (veto)
		newCrossAgentSecretGate(), // cross_agent_secret_leak (veto)
		newUnmonitoredGate(),      // unmonitored_channel_used (report)
		newInsecureCodeGate(),     // X4: insecure_code_added (baseline, report; veto by experiment codescan.veto_at)
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
