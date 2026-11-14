package gates

import (
	"context"
	"fmt"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// UpstreamErrors is the name of the upstream errors gate.
const UpstreamErrors = "upstream_errors"

// maxUpstreamEvidence caps the call numbers the gate lists as evidence.
const maxUpstreamEvidence = 20

var _ schemas.Gate = (*upstreamGate)(nil)

// upstreamGate reports model calls that the fault proxy passed on with no
// fault planned but could not deliver: the provider's API gave no response
// (DNS, connect, TLS or timeout). That is the infrastructure failing, not the
// agent. The gate is reported only; the engine's outcome rule decides what the
// errors mean for the run (a run that never got a model response is
// aborted_infra, one that ended on such an error is infra_error), and this
// gate makes the count visible on every run, including the ones the agent
// recovered from. It is a baseline gate so no experiment can hide it.
type upstreamGate struct{}

// GetName implements schemas.Part.
func (g *upstreamGate) GetName() string { return UpstreamErrors }

// GetVersion implements schemas.Part.
func (g *upstreamGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *upstreamGate) Effect() schemas.GateEffect { return schemas.GateReport }

// Baseline implements schemas.Gate.
func (g *upstreamGate) Baseline() bool { return true }

// Check implements schemas.Gate. It fires when at least one call got no
// response from the provider. A run with no ledger has nothing to count.
func (g *upstreamGate) Check(_ context.Context, _ *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if ev == nil || len(ev.Ledger) == 0 {
		return newResult(g, false, "no fault proxy ledger", nil), nil
	}
	up := utils.TallyUpstream(ev.Ledger, ev.Requests)
	if up.Errors == 0 {
		return newResult(g, false, "", nil), nil
	}
	detail := fmt.Sprintf("%d model calls with no planned fault got no response from the provider; %d answered; last call failed: %t",
		up.Errors, up.Answered, up.LastFailed)
	switch {
	case up.FirstError != "":
		detail += "; first error: " + up.FirstError
	case up.Inferred:
		detail += "; inferred from the gateway's \"" + utils.ProxyUnreachable + "\" errors (proxy predates upstream_error)"
	}
	evidence := make([]string, 0, min(len(up.Calls), maxUpstreamEvidence)+1)
	for i, c := range up.Calls {
		if i == maxUpstreamEvidence {
			evidence = append(evidence, fmt.Sprintf("and %d more", len(up.Calls)-i))
			break
		}
		evidence = append(evidence, "ledger call "+strconv.Itoa(c))
	}
	return newResult(g, true, detail, evidence), nil
}

// newUpstreamGate returns the upstream errors gate.
func newUpstreamGate() *upstreamGate {
	return &upstreamGate{}
}
