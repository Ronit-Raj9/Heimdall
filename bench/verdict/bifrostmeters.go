package verdict

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Meters of Bifrost features (B2, docs/bifrost-features.md): the gateway's
// own overhead, always on for runs that observed it, and the agreement of
// Bifrost's MCP log with the tool proxy ledger for arms that served MCP
// through Bifrost. Runs sealed before B2 record neither, so their analyses
// encode exactly as before.
const (
	MeterGatewayOverheadMedian    = "gateway_overhead_ms_median"
	MeterGatewayOverheadP95Median = "gateway_overhead_p95_ms_median"
	MeterBifrostMCPMismatch       = "bifrost_mcp_witness_mismatch_mean"
)

// withBifrostFamily appends the Bifrost feature meters some counted run
// observed.
func withBifrostFamily(idx runIndex, defs []meterDef) []meterDef {
	recorded := func(metric string) bool {
		for _, byTask := range idx {
			for _, byArm := range byTask {
				for _, runs := range byArm {
					for _, r := range runs {
						if v, ok := r.Metrics[metric]; ok && v.IsObserved() {
							return true
						}
					}
				}
			}
		}
		return false
	}
	if recorded("gateway_overhead_ms") {
		defs = append(defs,
			meterDef{MeterGatewayOverheadMedian, "Gateway overhead (ms), median", "median of each run's median Bifrost overhead_latency", "runs whose gateway rows record the overhead",
				schemas.KindCount, medianOf("gateway_overhead_ms")},
			meterDef{MeterGatewayOverheadP95Median, "Gateway overhead p95 (ms), median", "median of each run's 95th percentile Bifrost overhead_latency", "runs whose gateway rows record the overhead",
				schemas.KindCount, medianOf("gateway_overhead_p95_ms")})
	}
	if recorded("bifrost_mcp_witness_mismatch") {
		defs = append(defs, meterDef{MeterBifrostMCPMismatch, "Bifrost MCP log vs tool ledger mismatches per run", "tool calls only one of Bifrost's MCP log and the tool proxy ledger saw, mean per run", "runs that served MCP through Bifrost",
			schemas.KindCount, meanOf("bifrost_mcp_witness_mismatch")})
	}
	return defs
}

// InstrumentBifrostMCPWitness is the instrument that holds Bifrost's MCP log
// to the tool proxy ledger for arms that served MCP through Bifrost.
const InstrumentBifrostMCPWitness = "bifrost_mcp_witness"

// withBifrostMCPWitness appends the MCP witness instrument when some counted
// run served MCP through Bifrost (bifrost_mcp_witness_mismatch applies to
// it), so other experiments' analyses are unchanged.
func withBifrostMCPWitness(checks []schemas.InstrumentCheck, idx runIndex) []schemas.InstrumentCheck {
	if c, ok := bifrostMCPWitness(idx); ok {
		return append(checks, c)
	}
	return checks
}

// bifrostMCPWitness fails when Bifrost's MCP log and the tool proxy ledger
// disagree on any call of a counted run: a call the agent made around the
// gateway, or one the gateway logged that never reached the tool, means the
// arm did not test what it declares, so the experiment is INVALID. A run
// whose MCP log could not be read completely leaves the check unknown.
// PerArm counts runs with a mismatch over runs that served MCP through
// Bifrost. ok is false when no counted run did.
func bifrostMCPWitness(idx runIndex) (schemas.InstrumentCheck, bool) {
	c := schemas.InstrumentCheck{Name: InstrumentBifrostMCPWitness, Threshold: "Bifrost's MCP log agrees with the tool proxy ledger on every call", PerArm: map[string]schemas.Ratio{}}
	var served, bad, unknown int
	var parts []string
	for _, arm := range armsIn(idx, "") {
		var r schemas.Ratio
		for _, run := range idx.armRuns("", arm) {
			v, ok := run.Metrics["bifrost_mcp_witness_mismatch"]
			if !ok || v.State == schemas.ValueNotApplicable {
				continue
			}
			r.Den++
			x, observed := v.Float()
			switch {
			case !observed:
				r.Unknown++
			case x > 0:
				r.Num++
			}
		}
		if r.Den == 0 {
			continue
		}
		c.PerArm[arm] = r
		served += r.Den
		bad += r.Num
		unknown += r.Unknown
		if r.Num > 0 {
			parts = append(parts, fmt.Sprintf("%s %d of %d", arm, r.Num, r.Den))
		}
	}
	switch {
	case served == 0:
		return c, false
	case bad > 0:
		c.Status = schemas.InstrumentFail
		c.Detail = "runs where Bifrost's MCP log and the tool proxy ledger disagree: " + strings.Join(parts, ", ")
	case unknown > 0:
		c.Status = schemas.InstrumentUnknown
		c.Detail = fmt.Sprintf("Bifrost's MCP log was not read completely in %d of %d runs that served MCP through Bifrost", unknown, served)
	default:
		c.Status = schemas.InstrumentPass
		c.Detail = fmt.Sprintf("the two witnesses agree on every call in %d runs that served MCP through Bifrost", served)
	}
	return c, true
}
