package engine

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Gateway usage seal (lane H01). The heimdall Bifrost plugin posts one usage
// row per model call to the server's ingest route (handlers/gatewayusage.go,
// lane C03), which keeps them in memory for live runs. At seal the run's rows
// that carry the run's organization are written into the run folder as
// FileGatewayUsage, so the Through Bifrost panel (lane C09) still has them
// after a restart; rows tagged with another organization are left out.

// FileGatewayUsage is the run file holding the plugin's usage rows. It
// matches handlers.FileGatewayUsage.
const FileGatewayUsage = "gateway_usage.json"

// GatewayUsageSource returns the run's plugin usage rows whose org tag is
// org (empty org: rows with no org tag, a local install), encoded as the
// FileGatewayUsage JSON array. ok is false when there is no such row.
type GatewayUsageSource func(runID, org string) (data []byte, ok bool)

var gatewayUsageSrc atomic.Pointer[GatewayUsageSource]

// SetGatewayUsageSource sets the process's usage row source; nil clears it.
// The binary wires the ingest store in (cmd/bifrost-bench), since the engine
// does not import handlers.
func SetGatewayUsageSource(fn GatewayUsageSource) {
	if fn == nil {
		gatewayUsageSrc.Store(nil)
		return
	}
	gatewayUsageSrc.Store(&fn)
}

func gatewayUsageRows(runID, org string) ([]byte, bool) {
	fn := gatewayUsageSrc.Load()
	if fn == nil || runID == "" {
		return nil, false
	}
	return (*fn)(runID, org)
}

// runOrgGateway reports the organization a run's experiment is bound to
// (gateways/bifrost Gateway.RunOrg).
type runOrgGateway interface {
	RunOrg(rc *schemas.RunContext) (string, bool)
}

// runOrg is the organization of rc's run, empty when the gateway has none
// bound (a local install).
func (p *Pipeline) runOrg(rc *schemas.RunContext) string {
	g, ok := p.reg.Gateway.(runOrgGateway)
	if !ok || rc == nil {
		return ""
	}
	org, _ := g.RunOrg(rc)
	return org
}

// sealGatewayUsage writes the run's usage rows of org into its folder, when
// the source has any. A run without plugin rows gets no file.
func sealGatewayUsage(ctx context.Context, st schemas.Store, run *schemas.Run, org string) error {
	if run == nil {
		return nil
	}
	data, ok := gatewayUsageRows(run.RunID, org)
	if !ok {
		return nil
	}
	if err := st.WriteRunFile(ctx, run, FileGatewayUsage, data); err != nil {
		return wrapPart("store", st, fmt.Errorf("write %s: %w", FileGatewayUsage, err))
	}
	return nil
}
