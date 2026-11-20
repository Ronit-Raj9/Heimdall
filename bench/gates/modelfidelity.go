package gates

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ModelFidelity is the name of the model fidelity gate.
const ModelFidelity = "model_fidelity"

var _ schemas.Gate = (*modelFidelityGate)(nil)

// modelFidelityGate checks that every conversation request was answered by
// the model the run asked for, fresh. It is an instrument gate: one run firing
// it does not invalidate anything by itself; the verdict policy counts fired
// runs and declares the experiment invalid above a 5% rate, because then the
// arms were not measuring the configured model.
type modelFidelityGate struct{}

// GetName implements schemas.Part.
func (g *modelFidelityGate) GetName() string { return ModelFidelity }

// GetVersion implements schemas.Part.
func (g *modelFidelityGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *modelFidelityGate) Effect() schemas.GateEffect { return schemas.GateInvalidate }

// Baseline implements schemas.Gate. Every experiment is checked: the design
// promises INVALID above a 5% fidelity failure rate for any experiment
// whose model service is witnessed by the gateway log, not only those that
// opt in (audit 1-C-14). A run with no gateway log does not fire.
func (g *modelFidelityGate) Baseline() bool { return true }

// Check implements schemas.Gate. Per message row it flags a served model that
// differs from the requested one (after trimming a provider prefix such as
// "anthropic/"; a date suffix is a different model and is not trimmed), a
// fallback taken without a declared fallback, and a cache hit. A different
// served model is accepted only when the row is a fallback attempt and the
// served model is one the agent's gateway policy declared. Rows without a
// served model (errors, injected faults) are skipped for the model check. A
// cache hit is intended, and not flagged, for an arm that turns on Bifrost's
// semantic cache.
func (g *modelFidelityGate) Check(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if ev == nil || ev.Requests == nil {
		return newResult(g, false, "no gateway log", nil), nil
	}
	var fallbacks []string
	if rc != nil && rc.Agent != nil {
		fallbacks = rc.Agent.Gateway.Fallbacks
		if pol := rc.Agent.Gateway.Routing; pol != nil { // W9: the arm's routing policy decides which models may serve (modelfidelityrouting.go)
			return g.checkRouted(rc.Agent.Model, pol, ev), nil
		}
	}
	var swapped, undeclared, cached int
	var evidence []string
	var declared []string // X8 voice agents: gateway.models
	cacheOK := false
	if rc != nil && rc.Agent != nil {
		declared = rc.Agent.Gateway.Models
		cacheOK = rc.Agent.Gateway.Bifrost != nil && rc.Agent.Gateway.Bifrost.SemanticCache != nil
	}
	for _, r := range ev.Requests.Requests {
		if r.Kind == "audio" { // X8 voice agents: an audio row is a swap only when the served model is neither the requested nor a declared one
			if r.ModelServed != "" && !sameModel(r.ModelRequested, r.ModelServed) && !declaredModel(declared, r.ModelServed) {
				swapped++
				evidence = append(evidence, r.ID)
			}
			continue
		}
		if r.Kind != "message" && r.Kind != "" {
			continue
		}
		bad := false
		if r.ModelServed != "" && !sameModel(r.ModelRequested, r.ModelServed) && !declaredFallback(r, fallbacks) {
			swapped++
			bad = true
		}
		if r.FallbackIndex > 0 && len(fallbacks) == 0 {
			undeclared++
			bad = true
		}
		if r.CacheHit && !cacheOK {
			cached++
			bad = true
		}
		if bad {
			evidence = append(evidence, r.ID)
		}
	}
	if len(evidence) == 0 {
		return newResult(g, false, "", nil), nil
	}
	detail := fmt.Sprintf("%d request(s): %d served by another model, %d undeclared fallback, %d cache hit",
		len(evidence), swapped, undeclared, cached)
	return newResult(g, true, detail, evidence), nil
}

// newModelFidelityGate returns the model fidelity gate.
func newModelFidelityGate() *modelFidelityGate {
	return &modelFidelityGate{}
}

// sameModel compares two model ids, ignoring one leading provider segment on
// either side ("anthropic/claude-x" equals "claude-x"). Only the first segment
// is dropped, so "org/model-a" and "org/model-b" still differ.
func sameModel(requested, served string) bool {
	if requested == served {
		return true
	}
	r, rOK := dropProvider(requested)
	s, sOK := dropProvider(served)
	return (rOK && r == served) || (sOK && requested == s) || (rOK && sOK && r == s)
}

// declaredFallback reports whether r was served by a fallback the gateway
// policy declared.
func declaredFallback(r schemas.GatewayRequest, fallbacks []string) bool {
	if r.FallbackIndex <= 0 {
		return false
	}
	for _, f := range fallbacks {
		if sameModel(f, r.ModelServed) {
			return true
		}
	}
	return false
}

// dropProvider removes the first "/"-separated segment, if any.
func dropProvider(m string) (string, bool) {
	_, rest, ok := strings.Cut(m, "/")
	return rest, ok
}

// declaredModel reports whether model is one of the agent's declared extra
// models (X8 voice agents: gateway.models).
func declaredModel(declared []string, model string) bool {
	for _, d := range declared {
		if sameModel(d, model) {
			return true
		}
	}
	return false
}
