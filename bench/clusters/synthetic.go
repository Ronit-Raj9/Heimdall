package clusters

import "github.com/maximhq/bifrost/bench/schemas"

// adapterScripted is the adapter type of the scripted control agents
// (oracle, noop, exploit, refuse, toy).
const adapterScripted = "scripted"

// Synthetic reports whether a run is synthetic and stays out of the default
// view: its experiment is a declared smoke run, its agent is a scripted
// control agent (by the manifest arm or, without one, the run's own agent
// record), or its proxy ledger labels a call as answered by the scripted
// model. The leaderboard applies the same rule per experiment and config
// and also fingerprints ledgers written before the upstream label existed;
// clusters read only the label, so such an old dry run against the scripted
// model shows as live. It is pure.
func Synthetic(m *schemas.Manifest, r schemas.SealedRun, ledger []schemas.LedgerEntry) bool {
	if m != nil && m.Experiment.NoPaidTraffic() {
		return true
	}
	if arm := armOf(m, r.Slot.Arm); arm != nil {
		if arm.Config.Adapter == adapterScripted || arm.Config.Scripted != nil {
			return true
		}
	} else if r.Run.Agent.Adapter == adapterScripted {
		return true
	}
	for _, e := range ledger {
		if e.Upstream == schemas.LedgerUpstreamScripted {
			return true
		}
	}
	return false
}

// armOf returns the manifest arm a slot ran, the compatibility arm for
// "compat", or nil when the manifest does not list it.
func armOf(m *schemas.Manifest, arm string) *schemas.ArmManifest {
	if m == nil {
		return nil
	}
	if arm == "compat" {
		return m.Compatibility
	}
	if a, ok := m.Arms[arm]; ok {
		return &a
	}
	return nil
}
