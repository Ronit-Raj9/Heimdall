package handlers

import (
	"context"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/compat"
	"github.com/maximhq/bifrost/bench/schemas"
)

// checkArmsCompatible checks every setup of a normalized spec against every
// chosen benchmark before anything is saved (fix wave 3, N187). It loads
// each benchmark once and asks evalCompat for each setup's agent; the
// answer lists every pair that cannot run, one sentence each, or is "" when
// all can. A benchmark that cannot be loaded is left to launchChild, which
// skips it with its reason (and keeps its own check for scheduled repeats).
func (h *evaluationsHandler) checkArmsCompatible(ctx context.Context, spec *schemas.EvaluationSpec) string {
	s := h.env.Services
	if s.Suites == nil {
		return ""
	}
	agents := h.agentConfigs(ctx)
	var refusals []string
	seen := map[string]bool{}
	for _, b := range spec.Benchmarks {
		suiteID, _, _ := strings.Cut(b, "@")
		if seen[suiteID] {
			continue
		}
		seen[suiteID] = true
		st, err := s.Suites.LoadSuite(ctx, suiteID)
		if err != nil || st == nil {
			continue
		}
		for _, a := range spec.Arms {
			ok, missing, reason := evalCompat(agents["agents/"+a.Agent+".yaml"], st)
			if ok {
				continue
			}
			if reason == "" {
				reason = compat.Reason(a.Agent, suiteID, missing)
			}
			refusals = append(refusals, fmt.Sprintf("Setup %s (%s) cannot run %s: %s. Remove the benchmark or pick another baseline.",
				a.Label, a.Agent, suiteID, compatWhy(reason)))
		}
	}
	return strings.Join(refusals, " ")
}

// compatWhy is the reason part of a compatibility refusal ("Agent x cannot
// run benchmark y: <why>."), without the trailing full stop.
func compatWhy(reason string) string {
	why := strings.TrimSpace(reason)
	if i := strings.Index(strings.ToLower(why), "cannot run benchmark "); i >= 0 {
		if j := strings.Index(why[i:], ": "); j >= 0 {
			why = why[i+j+2:]
		}
	}
	return strings.TrimRight(why, ". ")
}
