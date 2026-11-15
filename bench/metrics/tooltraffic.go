package metrics

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ToolTrafficUnparsed is the name of the count of tool proxy traffic that
// was relayed without being parsed.
const ToolTrafficUnparsed = "tool_traffic_unparsed"

// reasonOldLedger is why a ledger written before the proxy recorded every
// request has no unparsed count: that proxy let such traffic through with no
// row at all, so zero would claim more than it saw.
const reasonOldLedger = "tool ledger written before the proxy recorded unparsed traffic"

var _ schemas.Metric = (*toolTrafficUnparsedMetric)(nil)

// toolTrafficUnparsedMetric counts the ledger rows the proxy marked
// unparsed: opaque requests (not JSON, not JSON-RPC, over the parse cap, of
// a content type or encoding it does not read, or to a path that is not the
// MCP endpoint), oversized stream events, and calls whose arguments or answer
// were over the cap. Each was still relayed, hashed and counted
// (docs/tool-proxy.md). Fewer is better: each one is traffic whose content
// no metric could read.
type toolTrafficUnparsedMetric struct{}

// GetName implements schemas.Part.
func (m *toolTrafficUnparsedMetric) GetName() string { return ToolTrafficUnparsed }

// GetVersion implements schemas.Part.
func (m *toolTrafficUnparsedMetric) GetVersion() string { return version }

// Descriptor implements schemas.Metric.
func (m *toolTrafficUnparsedMetric) Descriptor() schemas.MetricDescriptor {
	return schemas.MetricDescriptor{
		Name: ToolTrafficUnparsed, Version: version, Label: "Tool traffic relayed unparsed", Kind: schemas.KindCount,
		HigherIsBetter: false, MinEvidence: schemas.EvidenceBoundary,
		Description: "Tool proxy ledger rows of traffic the proxy relayed without parsing it (a body that is not JSON-RPC, over the 16 MiB parse cap, of a content type or encoding it does not read, sent to a path that is not the MCP endpoint, or an oversized stream event), each still hashed and counted in full, averaged over runs of tasks with proxied services or external APIs.",
	}
}

// Compute implements schemas.Metric.
func (m *toolTrafficUnparsedMetric) Compute(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (schemas.Value, error) {
	if !hasProxiedService(rc) && !hasExternalAPI(rc) {
		return schemas.NotApplicable(), nil
	}
	ev = orEmpty(ev)
	if ev.ToolCalls == nil {
		return schemas.Unknown(reasonNoToolLedger, schemas.EvidenceBoundary), nil
	}
	n, recorder := 0, len(ev.ToolCalls) == 0
	for _, c := range ev.ToolCalls {
		if c.Unparsed {
			n++
		}
		if c.Kind != "" || c.Unparsed || c.RequestBytes > 0 || c.ResponseBytes > 0 || c.ResponseContentType != "" {
			recorder = true
		}
	}
	if !recorder {
		return schemas.Unknown(reasonOldLedger, schemas.EvidenceBoundary), nil
	}
	return schemas.Observed(float64(n), schemas.EvidenceBoundary), nil
}

// newToolTrafficUnparsedMetric returns the tool_traffic_unparsed metric.
func newToolTrafficUnparsedMetric() *toolTrafficUnparsedMetric {
	return &toolTrafficUnparsedMetric{}
}
