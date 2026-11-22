package engine

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/adapters"
	"github.com/maximhq/bifrost/bench/gateways/bifrost"
	"github.com/maximhq/bifrost/bench/schemas"
)

// pluginFaultRouter is the plugin fault mode controller (lane C04,
// gateways/bifrost PluginFaults). The engine moves a run's armed model
// fault plan to the heimdall Bifrost plugin only for an agent that sends
// extra gateway headers; every other run keeps the fault proxy plan, so a
// fault is never silently dropped.
type pluginFaultRouter interface {
	MoveToPlugin(ctx context.Context, slot string, tags bifrost.RunTags) (map[string]string, string, error)
}

// routePluginFaults runs just before the agent's plan is built (after the
// arm stage), so the armed plan is the one the headers carry. It records
// the path that applied on the run. A no-op in the default proxy mode.
func (p *Pipeline) routePluginFaults(ctx context.Context, st *runState) error {
	r, ok := p.reg.Fault.(pluginFaultRouter)
	if !ok || st.rc == nil || st.rc.Run == nil || st.rc.Key == nil || st.rc.Key.KeySlot == "" {
		return nil
	}
	path := bifrost.FaultPathProxy
	var hdrs map[string]string
	if adapters.CarriesGatewayHeaders(st.rc.Agent) {
		var err error
		hdrs, path, err = r.MoveToPlugin(ctx, st.rc.Key.KeySlot, runTags(st.rc))
		if err != nil {
			return &partError{part: faultPart, err: fmt.Errorf("move fault plan of key slot %s to the heimdall plugin: %w", st.rc.Key.KeySlot, err)}
		}
	}
	if path == bifrost.FaultPathPlugin {
		st.rc.FaultHeaders = hdrs
	} else {
		st.rc.FaultHeaders = nil
	}
	st.rc.Run.Gateway.FaultPath = path
	return nil
}

func runTags(rc *schemas.RunContext) bifrost.RunTags {
	var t bifrost.RunTags
	if rc.Experiment != nil {
		t.EvaluationID = rc.Experiment.ID
	}
	if rc.Slot != nil {
		t.Arm = rc.Slot.Arm
	}
	return t
}
