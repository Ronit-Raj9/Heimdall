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
//     (stale notes and the like) when it has one, else skipped. An attack
//     probe (axis safety, or a hostile tool result) is never taken for it:
//     a prompt injection is an attack, scored under safety, not a fault.
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
			if id := misleading(s); id != "" {
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

// misleading is the suite's misleading-instruction probe that is not an
// attack: stale-notes first, then a fixture probe, then the first in suite
// order; "" when it has none.
func misleading(s *schemas.Suite) string {
	if s == nil {
		return ""
	}
	cp := *s
	cp.Probes = nil
	for _, p := range s.Probes {
		if !IsAttack(p) {
			cp.Probes = append(cp.Probes, p)
		}
	}
	return pick(&cp, schemas.FamilyMisleadingInstruction, "stale-notes", schemas.LayerFixture)
}

// Attack marks a condition that is an attack probe in Map: its runs count
// only in the safety section (attack resisted), never as a fault.
const Attack = "attack"

// axisSafety is the probe axis of attacks (prompt injection, secret
// canaries, protected paths, destructive bait).
const axisSafety = "safety"

// IsAttack reports whether probe p is an attack: a safety-axis probe or an
// attack delivered through a tool result.
func IsAttack(p *schemas.ProbeSpec) bool {
	if p == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(p.Axis), axisSafety) || p.PS2Family == schemas.FamilyHostileTool
}

// Map is the fault of each condition an evaluation asking for names can run
// on s, read from the same probe choice as Conditions: clean maps to Clean,
// each fault's condition id to its fault name, and every attack probe of s
// to Attack. A condition missing from the map is neither.
func Map(names []string, s *schemas.Suite) map[string]string {
	out := map[string]string{schemas.ConditionClean: Clean}
	if s != nil {
		for _, p := range s.Probes {
			if IsAttack(p) {
				out[p.ID] = Attack
			}
		}
	}
	for _, raw := range names {
		var id string
		switch name := strings.TrimSpace(raw); name {
		case ModelDown:
			id = modelDown(s)
		case BrokenTool:
			id, _ = brokenTool(s)
		case MisleadingInstruction:
			id = misleading(s)
		}
		if id != "" {
			out[id] = strings.TrimSpace(raw)
		}
	}
	return out
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
