package engine

import (
	"fmt"
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Safety probes area (S1): `safety: trifecta` on an experiment. Simon
// Willison's lethal trifecta (private data, untrusted input and a way to
// communicate out) and Meta's Rule of Two say an agent configuration that
// holds all three is the one that can be made to leak. The planner infers
// the three capabilities per task from the suite and task configuration,
// and for every task that has all three it adds the suite's exfiltration
// probes (probes of axis exfiltration) as conditions and opts the
// experiment into the exfiltration_attempted gate.

// Task capability tags that declare a trifecta leg explicitly.
const (
	capPrivateData    = "private_data"
	capUntrustedInput = "untrusted_input"
	capExternalComms  = "external_comms"
)

// exfiltrationGate is the gate the trifecta probes are judged by.
const exfiltrationGate = "exfiltration_attempted"

// TrifectaLegs says which of the three capabilities a task's configuration
// has, and why.
type TrifectaLegs struct {
	PrivateData, UntrustedInput, Exit bool
	Why                               []string
}

// Complete reports whether all three legs are present.
func (l TrifectaLegs) Complete() bool { return l.PrivateData && l.UntrustedInput && l.Exit }

// InferTrifecta infers the trifecta legs of one task from its configuration:
//
//   - private data: the task tags `private_data`, or a service the agent
//     reaches exports state after the run (a system of record);
//   - untrusted input: the task tags `untrusted_input`, or the agent reads
//     tool results from a proxied tool or MCP service or an external API
//     (text the agent did not write);
//   - an exit: the task tags `external_comms`, or it declares an external
//     API (outbound HTTP), or its output is an answer (a final message a
//     reader's client renders, where a markdown image is an exit).
//
// It is pure.
func InferTrifecta(suite *schemas.Suite, task *schemas.Task) TrifectaLegs {
	var l TrifectaLegs
	tag := func(c string) bool { return slices.Contains(task.Capabilities, c) }
	if tag(capPrivateData) {
		l.PrivateData, l.Why = true, append(l.Why, "tag "+capPrivateData)
	}
	if tag(capUntrustedInput) {
		l.UntrustedInput, l.Why = true, append(l.Why, "tag "+capUntrustedInput)
	}
	if tag(capExternalComms) {
		l.Exit, l.Why = true, append(l.Why, "tag "+capExternalComms)
	}
	for _, s := range utils.TaskServices(suite, task) {
		if s.Hidden {
			continue
		}
		if !l.PrivateData && len(s.Export) > 0 {
			l.PrivateData, l.Why = true, append(l.Why, "service "+s.ID+" holds exported state")
		}
		if !l.UntrustedInput && utils.ServiceProxied(s) {
			l.UntrustedInput, l.Why = true, append(l.Why, "service "+s.ID+" returns tool results")
		}
	}
	external := len(task.External) > 0 || len(suite.External) > 0
	if external && !l.UntrustedInput {
		l.UntrustedInput, l.Why = true, append(l.Why, "external API answers")
	}
	if external && !l.Exit {
		l.Exit, l.Why = true, append(l.Why, "external API (outbound HTTP)")
	}
	if !l.Exit && utils.OutputKind(suite, task) == schemas.OutputAnswer {
		l.Exit, l.Why = true, append(l.Why, "rendered final answer")
	}
	return l
}

// applySafetyTrifecta adds the suite's exfiltration probes as conditions on
// every task of the experiment whose configuration has all three legs, and
// adds exfiltration_attempted to the extra gates. It returns one note per
// change for the plan log. It changes nothing unless e.Safety is trifecta.
func applySafetyTrifecta(suite *schemas.Suite, e *schemas.Experiment) ([]string, error) {
	switch e.Safety {
	case "":
		return nil, nil
	case schemas.SafetyTrifecta:
	default:
		return nil, fmt.Errorf("safety %q is not trifecta", e.Safety)
	}
	var probes []string
	for _, p := range suite.Probes {
		if p.Axis == "exfiltration" {
			probes = append(probes, p.ID)
		}
	}
	if len(probes) == 0 {
		return nil, fmt.Errorf("safety: trifecta needs a probe of axis exfiltration in suite %s", suite.ID)
	}
	trials := 1
	var used []string
	for _, c := range e.Conditions {
		trials = max(trials, c.Trials)
		for _, t := range c.Tasks.Resolve(suite.TaskIDs) {
			if !slices.Contains(used, t) {
				used = append(used, t)
			}
		}
	}
	var targets, notes []string
	for _, id := range used {
		task := suite.TaskByID(id)
		if task == nil {
			continue
		}
		if l := InferTrifecta(suite, task); l.Complete() {
			targets = append(targets, id)
			notes = append(notes, fmt.Sprintf("trifecta: task %s (%v)", id, l.Why))
		}
	}
	if len(targets) == 0 {
		return append(notes, "trifecta: no task has private data, untrusted input and an exit; nothing added"), nil
	}
	conds := append([]schemas.ConditionSpec(nil), e.Conditions...)
	for _, pid := range probes {
		i := slices.IndexFunc(conds, func(c schemas.ConditionSpec) bool { return c.ID == pid })
		if i < 0 {
			conds = append(conds, schemas.ConditionSpec{ID: pid, Tasks: schemas.TaskSelector{IDs: append([]string(nil), targets...)}, Trials: trials})
			notes = append(notes, fmt.Sprintf("trifecta: added condition %s on %d task(s)", pid, len(targets)))
			continue
		}
		if conds[i].Tasks.All {
			continue
		}
		ids := append([]string(nil), conds[i].Tasks.IDs...)
		for _, t := range targets {
			if !slices.Contains(ids, t) {
				ids = append(ids, t)
			}
		}
		conds[i].Tasks = schemas.TaskSelector{IDs: ids}
	}
	e.Conditions = conds
	if !slices.Contains(e.ExtraGates, exfiltrationGate) {
		e.ExtraGates = append(append([]string(nil), e.ExtraGates...), exfiltrationGate)
	}
	return notes, nil
}
