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
//
// Every agent that carries gateway headers (adapters.CarriesGatewayHeaders:
// Claude Code, Codex, the mcploop agent, framework and remote_http agents
// that declare it) moves to the plugin; the rest keep the proxy plan. The
// provider failover outage (lane C07, the primary-outage probe) is a
// persistent plan narrowed to the primary model, so on the plugin path it
// rides as the same inline outage plan failover.Plan puts in Arm.Headers.
// A team run routes each member on its own key slot (routeMemberFaults).
func (p *Pipeline) routePluginFaults(ctx context.Context, st *runState) error {
	r, ok := p.reg.Fault.(pluginFaultRouter)
	if !ok || st.rc == nil || st.rc.Run == nil || st.rc.Key == nil || st.rc.Key.KeySlot == "" {
		return nil
	}
	hdrs, path, err := movePluginFaults(ctx, r, st.rc, st.rc.Agent, st.rc.Key.KeySlot, p.runOrg(st.rc))
	if err != nil {
		return err
	}
	st.rc.FaultHeaders = hdrs
	st.rc.Run.Gateway.FaultPath = path
	return nil
}

// movePluginFaults moves slot's plan to the plugin when agent carries the
// headers; it returns the headers (nil on the proxy path) and the path.
func movePluginFaults(ctx context.Context, r pluginFaultRouter, rc *schemas.RunContext, agent *schemas.AgentConfig, slot, org string) (map[string]string, string, error) {
	if !adapters.CarriesGatewayHeaders(agent) {
		return nil, bifrost.FaultPathProxy, nil
	}
	hdrs, path, err := r.MoveToPlugin(ctx, slot, runTags(rc, org))
	if err != nil {
		return nil, bifrost.FaultPathProxy, &partError{part: faultPart, err: fmt.Errorf("move fault plan of key slot %s to the heimdall plugin: %w", slot, err)}
	}
	if path != bifrost.FaultPathPlugin {
		return nil, path, nil
	}
	if rc.Probe != nil && rc.Probe.ID == schemas.GenericProbePrimaryOutage {
		asOutage(hdrs)
	}
	return hdrs, path, nil
}

// asOutage sends the provider failover outage (lane C07) the way
// failover.Plan writes it for Arm.Headers: plugin mode outage, not a plain
// error, so plugin logs name it a provider outage. Status, model and window
// are the armed plan's.
func asOutage(hdrs map[string]string) {
	raw, ok := hdrs[bifrost.HeaderFaultPlan]
	if !ok {
		return
	}
	pp, err := bifrost.DecodePluginPlan(raw)
	if err != nil || pp.Mode != bifrost.PluginModeError {
		return
	}
	pp.Mode = bifrost.PluginModeOutage
	hdrs[bifrost.HeaderFaultPlan] = bifrost.EncodePluginPlan(pp)
}

// routeMemberFaults is routePluginFaults for one team member: its own key
// slot and its own config decide its path, and mrc (the member's copy of
// the run context) gets its own headers, never the lead's. paths collects
// each member's path for teamFaultPath.
func (p *Pipeline) routeMemberFaults(ctx context.Context, mrc *schemas.RunContext, paths map[string]bool) error {
	mrc.FaultHeaders = nil
	r, ok := p.reg.Fault.(pluginFaultRouter)
	if !ok || mrc.Run == nil || mrc.Key == nil || mrc.Key.KeySlot == "" {
		return nil
	}
	hdrs, path, err := movePluginFaults(ctx, r, mrc, mrc.Agent, mrc.Key.KeySlot, p.runOrg(mrc))
	if err != nil {
		return err
	}
	mrc.FaultHeaders = hdrs
	paths[path] = true
	return nil
}

// teamFaultPath is the run's fault path over its members: plugin or proxy
// when all agree, mixed when some members carry the headers and some do
// not; "" when nothing was routed (proxy mode).
func teamFaultPath(paths map[string]bool) string {
	switch {
	case len(paths) == 0:
		return ""
	case len(paths) > 1:
		return bifrost.FaultPathMixed
	case paths[bifrost.FaultPathPlugin]:
		return bifrost.FaultPathPlugin
	}
	return bifrost.FaultPathProxy
}

// runTags are the x-heimdall-* tags of rc's run. org is the run's
// organization: the plugin stamps it on every usage row, and the ingest
// store keeps only rows of the run's organization (H01).
func runTags(rc *schemas.RunContext, org string) bifrost.RunTags {
	t := bifrost.RunTags{Org: org}
	if rc.Experiment != nil {
		t.EvaluationID = rc.Experiment.ID
	}
	if rc.Slot != nil {
		t.Arm = rc.Slot.Arm
	}
	return t
}
