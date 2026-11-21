// Package faults maps the fault names of an agent-first evaluation
// (AGENTFIRST_CONTRACT.md section 3) to experiment conditions on one
// benchmark:
//
//   - "clean" is always the first condition.
//   - "model-down" is a model outage from the fault proxy behind Bifrost. It
//     works on any benchmark: the suite's own unavailable_model gateway probe
//     when it has one, else the generic probe schemas.GenericProbeModelDown,
//     which Suite.ProbeByID resolves at plan and run time.
//   - "broken-tool" is an error injected on tool calls. The suite's own
//     broken_tool probe when it has one (tool proxy, tool shim or adapter
//     hook), else the generic tool proxy probe schemas.GenericProbeBrokenTool
//     when the benchmark routes tool calls through the tool proxy, else
//     skipped.
//   - "misleading-instruction" is the suite's misleading_instruction probe
//     (stale notes and the like) when it has one, else skipped.
//
// It is pure: it reads the loaded suite and writes nothing.
package faults

import (
	"fmt"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Fault names an evaluation may ask for.
const (
	Clean                 = schemas.ConditionClean
	ModelDown             = "model-down"
	BrokenTool            = "broken-tool"
	MisleadingInstruction = "misleading-instruction"
)

// Names lists every fault name, clean first.
var Names = []string{Clean, ModelDown, BrokenTool, MisleadingInstruction}

// Skip is a fault that cannot run on a benchmark, with the reason in plain
// words.
type Skip struct {
	Fault  string `json:"fault"`
	Reason string `json:"reason"`
}

// Conditions maps fault names to the condition ids of an experiment on s.
// The result always starts with clean and never repeats an id. A fault that
// cannot run on s, or a name that is not a fault, is in skipped instead.
func Conditions(names []string, s *schemas.Suite) (conds []string, skipped []Skip) {
	conds = []string{schemas.ConditionClean}
	add := func(id string) {
		if !slices.Contains(conds, id) {
			conds = append(conds, id)
		}
	}
	seen := map[string]bool{}
	for _, raw := range names {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		switch name {
		case Clean:
		case ModelDown:
			add(modelDown(s))
		case BrokenTool:
			if id, reason := brokenTool(s); id != "" {
				add(id)
			} else {
				skipped = append(skipped, Skip{Fault: name, Reason: reason})
			}
		case MisleadingInstruction:
			if id := pick(s, schemas.FamilyMisleadingInstruction, "stale-notes", schemas.LayerFixture); id != "" {
				add(id)
			} else {
				skipped = append(skipped, Skip{Fault: name, Reason: fmt.Sprintf("benchmark %s has no misleading or stale notes probe", suiteID(s))})
			}
		default:
			skipped = append(skipped, Skip{Fault: name, Reason: fmt.Sprintf("%q is not a fault; use one of %s", name, strings.Join(Names, ", "))})
		}
	}
	return conds, skipped
}

// modelDown is the suite's own whole-run gateway outage probe (model-outage
// first), else the generic one.
func modelDown(s *schemas.Suite) string {
	var first string
	if s != nil {
		for _, p := range s.Probes {
			if p.PS2Family != schemas.FamilyUnavailableModel || p.Layer != schemas.LayerGateway || p.Apply.Fault == nil || p.Apply.Agent != "" {
				continue
			}
			if p.ID == "model-outage" {
				return p.ID
			}
			if first == "" {
				first = p.ID
			}
		}
	}
	if first != "" {
		return first
	}
	return schemas.GenericProbeModelDown
}

// brokenTool is the suite's own broken_tool probe (tool proxy first, then
// tool shim, then any other layer), else the generic tool proxy probe when
// the benchmark has a tool proxy, else "" and the reason.
func brokenTool(s *schemas.Suite) (string, string) {
	if s == nil {
		return "", "no benchmark"
	}
	for _, layer := range []schemas.ProbeLayerName{schemas.LayerTool, schemas.LayerToolShim, ""} {
		for _, p := range s.Probes {
			if p.PS2Family == schemas.FamilyBrokenTool && (layer == "" || p.Layer == layer) {
				return p.ID, ""
			}
		}
	}
	if HasToolProxy(s) {
		return schemas.GenericProbeBrokenTool, ""
	}
	return "", fmt.Sprintf("benchmark %s has no broken-tool probe and no tool service or external API behind the tool proxy, so there is no tool call to break", suiteID(s))
}

// pick is the suite's probe of family, preferring id, then layer, then the
// first in suite order; "" when it has none.
func pick(s *schemas.Suite, family schemas.PS2Family, id string, layer schemas.ProbeLayerName) string {
	if s == nil {
		return ""
	}
	var first, inLayer string
	for _, p := range s.Probes {
		if p.PS2Family != family {
			continue
		}
		if p.ID == id {
			return p.ID
		}
		if inLayer == "" && p.Layer == layer {
			inLayer = p.ID
		}
		if first == "" {
			first = p.ID
		}
	}
	if inLayer != "" {
		return inLayer
	}
	return first
}

// HasToolProxy reports whether runs of s start the per-run tool proxy: some
// suite or task service is proxied (a tool or MCP service not hidden), or the
// suite or a task declares an external API.
func HasToolProxy(s *schemas.Suite) bool {
	if s == nil {
		return false
	}
	proxied := func(svcs []schemas.ServiceSpec) bool {
		return slices.ContainsFunc(svcs, func(sv schemas.ServiceSpec) bool { return !sv.Hidden && utils.ServiceProxied(sv) })
	}
	if len(s.External) > 0 || proxied(s.Services) {
		return true
	}
	for _, t := range s.Tasks {
		if t != nil && (len(t.External) > 0 || proxied(t.Services)) {
			return true
		}
	}
	return false
}

func suiteID(s *schemas.Suite) string {
	if s == nil {
		return "?"
	}
	return s.ID
}
