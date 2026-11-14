package gates

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Egress is the name of the egress gate.
const Egress = "egress"

// Gateway manifest settings the egress gate reads. Preflight records them.
const (
	settingEgressProbe = "egress_probe"       // "blocked" when a test connection off the eval network failed
	settingEvalNetwork = "eval_network"       // the internal network agents must be attached to
	settingEgressError = "egress_probe_error" // why the probe did not run (a helper without python3, say)
	egressBlocked      = "blocked"
)

var _ schemas.Gate = (*egressGate)(nil)

// egressGate checks that the run was isolated: preflight proved outbound
// traffic other than the gateway is blocked, and the run's workspace sits on
// the configured internal network. If either is not shown, model calls may
// have bypassed the gateway witness, so the whole experiment is invalid rather
// than one run. Anything it cannot confirm counts as a failure.
type egressGate struct{}

// GetName implements schemas.Part.
func (g *egressGate) GetName() string { return Egress }

// GetVersion implements schemas.Part.
func (g *egressGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *egressGate) Effect() schemas.GateEffect { return schemas.GateInvalidate }

// Baseline implements schemas.Gate.
func (g *egressGate) Baseline() bool { return true }

// Check implements schemas.Gate.
func (g *egressGate) Check(_ context.Context, rc *schemas.RunContext, _ *schemas.Evidence) (*schemas.GateResult, error) {
	var settings map[string]string
	if rc != nil && rc.Manifest != nil && rc.Manifest.Gateway != nil {
		settings = rc.Manifest.Gateway.Settings
	}
	var problems []string
	switch probe, ok := settings[settingEgressProbe]; {
	case !ok:
		problems = append(problems, "egress probe not recorded in the gateway manifest")
	case probe != egressBlocked:
		p := fmt.Sprintf("egress probe is %q, not %q", probe, egressBlocked)
		if reason := settings[settingEgressError]; reason != "" {
			p += ": " + reason
		}
		problems = append(problems, p)
	}
	want := settings[settingEvalNetwork]
	got := ""
	if rc != nil && rc.Handle != nil {
		got = rc.Handle.Network
	}
	switch {
	case got == "":
		problems = append(problems, "run has no network recorded")
	case want == "":
		problems = append(problems, "eval network not recorded in the gateway manifest")
	case got != want:
		problems = append(problems, fmt.Sprintf("run network %q is not the eval network %q", got, want))
	}
	if len(problems) == 0 {
		return newResult(g, false, "", nil), nil
	}
	return newResult(g, true, strings.Join(problems, "; "), []string{"network:" + got}), nil
}

// newEgressGate returns the egress gate.
func newEgressGate() *egressGate {
	return &egressGate{}
}
