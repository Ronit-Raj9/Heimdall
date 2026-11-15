package gates

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
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
		note := utils.RemoteEgressNote(rc) // Remote area: records the one host a remote_http agent may reach; "" otherwise
		if ext := externalEgressNote(rc); ext != "" {
			note = strings.TrimPrefix(note+"; "+ext, "; ")
		}
		return newResult(g, false, note, nil), nil
	}
	return newResult(g, true, strings.Join(problems, "; "), []string{"network:" + got}), nil
}

// newEgressGate returns the egress gate.
func newEgressGate() *egressGate {
	return &egressGate{}
}

// externalEgressNote records the external APIs a run declared (Suite.External,
// Task.External): each one's host and the mode it ran in. They are allowed
// by design, not a leak: only the tool proxy reaches them, and only in live
// or record mode, each through a per-run forwarder to exactly that host; in
// replay mode nothing leaves the bench. It is "" for a run without external
// APIs.
func externalEgressNote(rc *schemas.RunContext) string {
	if rc == nil {
		return ""
	}
	refs := utils.ExternalAPIs(rc.Suite, rc.Task)
	if len(refs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		target, err := utils.ExternalTarget(r.API.BaseURL)
		if err != nil {
			target = "invalid base_url"
		}
		how := ""
		switch mode, err := utils.RunExternalMode(rc, r); {
		case err != nil:
			how = "mode unresolved: " + err.Error()
		case mode == schemas.ExternalReplay:
			how = "replay, no egress"
		default:
			how = mode + ", tool proxy only, through a per-run forwarder; not repeatable"
		}
		parts = append(parts, r.API.ID+" -> "+target+" ("+how+")")
	}
	return "external APIs: " + strings.Join(parts, "; ")
}
