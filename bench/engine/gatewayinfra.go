package engine

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A run whose agent never reached the model gateway is the bench's
// infrastructure, never the agent's score. The run got a Bifrost virtual key
// (so its model calls were meant to cross the gateway), the agent ran until
// its wall budget, and Bifrost's complete request log and the fault proxy
// ledger hold not one model call: the agent could not reach the gateway (an
// org network the gateway is not on, a sandbox with no name resolution) and
// retried until its time ran out. Sealed as infra_error, which is never
// scored and never rerun, with the reason in the run's failures.

// gatewayPart is the part a run's unreachable gateway is recorded under.
const gatewayPart = "model gateway"

// gatewayOutcome turns a timed-out run with no model call at the gateway
// into infra_error. Only a timeout changes: an agent that exited on its own
// with no model call (a crash, a refusal) is still the agent's outcome.
func gatewayOutcome(rc *schemas.RunContext, ev *schemas.Evidence, o schemas.Outcome) (schemas.Outcome, *schemas.PartFailure) {
	if o != schemas.OutcomeTimeout || rc == nil || rc.Run == nil || rc.Run.Gateway.VirtualKeyID == "" || ev == nil {
		return o, nil
	}
	if ev.Requests == nil || !ev.Requests.Complete || len(ev.Requests.Requests) > 0 || len(ev.Ledger) > 0 {
		return o, nil
	}
	pf := &schemas.PartFailure{Stage: schemas.StageRun, Part: gatewayPart,
		Error: fmt.Sprintf("no model call reached the Bifrost gateway (session %s) before the agent's time ran out: the gateway was not reachable from the agent's sandbox; excluded from scores as infrastructure", rc.Run.Gateway.SessionID)}
	return schemas.OutcomeInfraError, pf
}
