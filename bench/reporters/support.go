package reporters

import (
	"bufio"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// writeSupport labels a report of a plan with a simulated user (W3 customer
// support, docs/customer-support.md): which user the conversations had, that
// verified success is called resolution on a support suite, and that results
// under another user are not comparable. The per-arm scorecard (resolution,
// cost per resolution, escalation and policy violation rates) is on the
// verdict page and at GET /experiments/{id}/support. Nothing is written for
// a plan without a simulated user.
func writeSupport(w *bufio.Writer, m *schemas.Manifest) {
	if m == nil || m.Simulator == nil {
		return
	}
	label := "scripted user"
	if m.Simulator.Mode == schemas.SimulatorLLM {
		label = "simulated user (model: " + m.Simulator.Provider + "/" + m.Simulator.Model + ")"
	}
	fmt.Fprintf(w, "> customer: %s. Verified success is resolution here, and cost per verified success is cost per resolution. Results under a different simulated user are not comparable with these.\n\n", label)
}
