package gates

import (
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// FaultDelivery is the name of the fault delivery gate.
const FaultDelivery = "fault_delivery"

// faultDeliveryDetail is the detail of a fired fault delivery gate.
const faultDeliveryDetail = "the fault plan never reached the gateway: the agent did not send the heimdall headers"

// faultPathPlugin is the run's fault path when its model fault plan rode in
// the agent's request headers to the heimdall Bifrost plugin
// (gateways/bifrost.FaultPathPlugin; gates does not import the gateway).
const faultPathPlugin = "plugin"

var _ schemas.Gate = (*faultDeliveryGate)(nil)

// faultDeliveryGate checks, from sealed evidence only, that a model fault
// plan sent on the plugin path reached the gateway. On that path the fault
// depends on the agent forwarding the x-heimdall-* headers; an agent that
// drops them runs fault-free while the run still counts as a fault run, so
// resilience would look better than it is. The heimdall plugin tags every
// request it sees with heimdall_fault_plan_id, which the Bifrost logging
// plugin stores in the log row metadata (GatewayRequest.FaultPlanID). A run
// on the plugin path with a gateway fault condition, at least one model call
// and no row carrying its plan id is thrown out (effect invalidate). Runs on
// the proxy path never fire it: the proxy sits on the network path.
type faultDeliveryGate struct{}

// GetName implements schemas.Part.
func (g *faultDeliveryGate) GetName() string { return FaultDelivery }

// GetVersion implements schemas.Part.
func (g *faultDeliveryGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *faultDeliveryGate) Effect() schemas.GateEffect { return schemas.GateInvalidate }

// Baseline implements schemas.Gate: every run is checked; only plugin path
// fault runs can fire it.
func (g *faultDeliveryGate) Baseline() bool { return true }

// Check implements schemas.Gate.
func (g *faultDeliveryGate) Check(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if rc == nil || rc.Run == nil || rc.Run.Gateway.FaultPath != faultPathPlugin {
		return newResult(g, false, "", nil), nil
	}
	if rc.Probe == nil || (rc.Probe.Layer != schemas.LayerGateway && rc.Probe.Layer != schemas.LayerCompound) {
		return newResult(g, false, "", nil), nil
	}
	if ev == nil || ev.Requests == nil {
		return newResult(g, false, "no gateway log", nil), nil
	}
	prefix := rc.Run.RunID + ":"
	calls := 0
	var evidence []string
	for _, r := range ev.Requests.Requests {
		if r.Kind != "message" && r.Kind != "" {
			continue
		}
		calls++
		if r.FaultPlanID != "" && (rc.Run.RunID == "" || strings.HasPrefix(r.FaultPlanID, prefix)) {
			return newResult(g, false, "", nil), nil
		}
		evidence = append(evidence, r.ID)
	}
	if calls == 0 {
		return newResult(g, false, "", nil), nil
	}
	return newResult(g, true, faultDeliveryDetail, evidence), nil
}

// newFaultDeliveryGate returns the fault delivery gate.
func newFaultDeliveryGate() *faultDeliveryGate {
	return &faultDeliveryGate{}
}
