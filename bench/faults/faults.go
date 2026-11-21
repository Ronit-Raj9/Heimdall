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
//   - the resilience faults of Catalog (lane X22): model brownout, slow and
//     flaky windows from generic gateway probes; tool rate limit, slow,
//     flaky, partial, removed and duplicate faults from the suite's own
//     probe of that tool proxy mode, else a generic one when the benchmark
//     has a tool proxy; expired login and API version change only from the
//     suite's own probe; and a compound brownout then rate limit schedule.
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

// Resilience faults (lane X22): faults that end, so recovery is measurable,
// and the tool failures real APIs have.
const (
	ModelBrownout       = "model-brownout"
	ModelSlow           = "model-slow"
	ModelFlaky          = "model-flaky"
	ToolRateLimit       = "tool-rate-limit"
	ToolSlow            = "tool-slow"
	ToolFlaky           = "tool-flaky"
	ToolPartial         = "tool-partial"
	ToolAuthExpired     = "tool-auth-expired"
	ToolRemoved         = "tool-removed"
	ToolInputDrift      = "tool-input-drift"
	ToolDuplicateEffect = "tool-duplicate-effect"
	CompoundBrownout    = "compound-brownout-rate-limit"
)

// Fault is one fault an evaluation can ask for, as the UI lists it.
type Fault struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Help  string `json:"help"`
	Layer string `json:"layer"` // model | tool | instructions | compound
	Group string `json:"group"` // Model | Tools | Instructions | Combined
	// NeedsProbe is set when the fault runs only on a benchmark that ships
	// its own probe for it (the fault must name a tool or its parameters).
	NeedsProbe bool `json:"needs_probe,omitempty"`
}

// Catalog lists every fault besides clean, in the order the UI shows them.
var Catalog = []Fault{
	{ID: ModelDown, Label: "Model down", Help: "The model stops answering after the first call, to see if the agent stops honestly instead of claiming success.", Layer: "model", Group: "Model"},
	{ID: ModelBrownout, Label: "Model brownout", Help: "The model fails for a few calls and then comes back, to see if the agent waits, retries and recovers.", Layer: "model", Group: "Model"},
	{ID: ModelSlow, Label: "Slow model", Help: "Model answers take 8 seconds to start, to see if the agent stays patient without piling up requests.", Layer: "model", Group: "Model"},
	{ID: ModelFlaky, Label: "Flaky model", Help: "About 30% of model calls fail at random, to see if the agent retries with backoff.", Layer: "model", Group: "Model"},
	{ID: BrokenTool, Label: "Broken tool", Help: "Some tool calls return errors, to see if the agent notices and works around them.", Layer: "tool", Group: "Tools"},
	{ID: ToolRateLimit, Label: "Rate limit", Help: "The tool allows 3 calls per 20 seconds and answers 429 with Retry-After, to see if the agent waits as told.", Layer: "tool", Group: "Tools"},
	{ID: ToolSlow, Label: "Slow tool", Help: "The first tool calls take 15 seconds, to see if the agent waits instead of retrying in parallel.", Layer: "tool", Group: "Tools"},
	{ID: ToolFlaky, Label: "Flaky tool", Help: "About 30% of tool calls fail at random, to see if the agent retries.", Layer: "tool", Group: "Tools"},
	{ID: ToolPartial, Label: "Partial results", Help: "Tool results are cut to 2 items with a next page cursor, to see if the agent fetches the rest.", Layer: "tool", Group: "Tools"},
	{ID: ToolAuthExpired, Label: "Expired login", Help: "The tool says the token expired until the agent renews it, to see if it re-authenticates.", Layer: "tool", Group: "Tools", NeedsProbe: true},
	{ID: ToolRemoved, Label: "Tool removed", Help: "The first tool the agent uses disappears after one call, to see if it finds another way.", Layer: "tool", Group: "Tools"},
	{ID: ToolInputDrift, Label: "API version change", Help: "A tool renames its parameters, to see if the agent reads the error and adapts.", Layer: "tool", Group: "Tools", NeedsProbe: true},
	{ID: ToolDuplicateEffect, Label: "Duplicate action", Help: "Tool calls reach the server twice, to see if the agent checks for duplicated changes.", Layer: "tool", Group: "Tools"},
	{ID: MisleadingInstruction, Label: "Misleading instructions", Help: "Stale or wrong notes are planted, to see if the agent checks before trusting them.", Layer: "instructions", Group: "Instructions"},
	{ID: CompoundBrownout, Label: "Brownout, then rate limit", Help: "The model browns out, and 20 seconds in the tool starts rate limiting, to see how the agent handles failures that stack.", Layer: "compound", Group: "Combined"},
}

// Names lists every fault name, clean first.
var Names = func() []string {
	out := []string{Clean}
	for _, f := range Catalog {
		out = append(out, f.ID)
	}
	return out
}()

// toolModes maps each tool fault name to the tool proxy mode it injects
// and the generic probe that runs it when the suite ships none ("" when the
// fault needs the suite's own probe).
var toolModes = map[string]struct{ mode, generic string }{
	ToolRateLimit:       {"rate_limit", schemas.GenericProbeToolRateLimit},
	ToolSlow:            {"slow", schemas.GenericProbeToolSlow},
	ToolFlaky:           {"flaky", schemas.GenericProbeToolFlaky},
	ToolPartial:         {"partial", schemas.GenericProbeToolPartial},
	ToolAuthExpired:     {"auth_expired", ""},
	ToolRemoved:         {"tool_removed", schemas.GenericProbeToolRemoved},
	ToolInputDrift:      {"input_drift", ""},
	ToolDuplicateEffect: {"duplicate_effect", schemas.GenericProbeToolDuplicate},
}

// modelModes maps each model fault name to its generic probe.
var modelModes = map[string]string{
	ModelBrownout: schemas.GenericProbeModelBrownout,
	ModelSlow:     schemas.GenericProbeModelSlow,
	ModelFlaky:    schemas.GenericProbeModelFlaky,
}

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
		case ModelBrownout, ModelSlow, ModelFlaky:
			add(modelModes[name])
		case ToolRateLimit, ToolSlow, ToolFlaky, ToolPartial, ToolAuthExpired, ToolRemoved, ToolInputDrift, ToolDuplicateEffect:
			if id, reason := toolFault(s, name); id != "" {
				add(id)
			} else {
				skipped = append(skipped, Skip{Fault: name, Reason: reason})
			}
		case CompoundBrownout:
			if HasToolProxy(s) {
				add(schemas.GenericProbeCompound)
			} else {
				skipped = append(skipped, Skip{Fault: name, Reason: fmt.Sprintf("benchmark %s has no tool service or external API behind the tool proxy, so the rate limit step has no tool call to limit", suiteID(s))})
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

// toolFault is the suite's own tool proxy probe injecting the fault's mode,
// else its generic probe when the benchmark has a tool proxy, else "" and
// the reason.
func toolFault(s *schemas.Suite, name string) (string, string) {
	tm := toolModes[name]
	if s != nil {
		for _, p := range s.Probes {
			if p.Layer == schemas.LayerTool && p.Apply.Tool != nil && p.Apply.Tool.Mode == tm.mode {
				return p.ID, ""
			}
		}
	}
	if tm.generic == "" {
		return "", fmt.Sprintf("benchmark %s has no %s probe; this fault must name the tool and its parameters, so the benchmark has to ship one", suiteID(s), tm.mode)
	}
	if !HasToolProxy(s) {
		return "", fmt.Sprintf("benchmark %s has no tool service or external API behind the tool proxy, so there is no tool call to fault", suiteID(s))
	}
	return tm.generic, ""
}
