package packs

import "strings"

// Report card capabilities (contract section 4).
const (
	CapTerminal = "terminal"
	CapCoding   = "coding"
	CapMemory   = "memory"
	CapSupport  = "support"
	CapResearch = "research"
	CapVoice    = "voice"
	CapSafety   = "safety"
	CapTeam     = "team"
	CapGPU      = "gpu"
)

// Capabilities lists every report card capability in display order.
func Capabilities() []string {
	return []string{CapTerminal, CapCoding, CapMemory, CapSupport, CapResearch, CapVoice, CapSafety, CapTeam, CapGPU}
}

// idCapability matches words in a benchmark id first: they are more specific
// than the track (an AgentDojo import has track tool_use but is a safety
// benchmark). Order matters.
var idCapability = []struct {
	words []string
	cap   string
}{
	{[]string{"agentdojo", "injection", "jailbreak", "redteam", "red-team", "safety", "harmful", "-ib-swe", "impossible"}, CapSafety},
	{[]string{"voice", "speech", "audio"}, CapVoice},
	{[]string{"multi-agent", "multiagent", "swarm", "team"}, CapTeam},
	{[]string{"gpu", "cuda", "kernel"}, CapGPU},
	{[]string{"longmem", "locomo", "memory", "recall"}, CapMemory},
	{[]string{"browsecomp", "gaia", "research", "search"}, CapResearch},
	{[]string{"tau2", "tau-bench", "taubench", "support", "helpdesk", "customer", "mcpmark", "bfcl", "function-call"}, CapSupport},
	// "harbor" is only the importer: only these harbor benchmarks are
	// terminal tasks; any other harbor import goes by its track.
	{[]string{"hd-harbor-tb2", "hd-harbor-tb41-cpu", "hd-harbor-skills", "terminal", "shell", "bash"}, CapTerminal},
	{[]string{"swe-", "swebench", "coding", "humaneval", "livecodebench"}, CapCoding},
}

// trackCapability maps a suite track (schemas/scenario.go) to a capability.
var trackCapability = map[string]string{
	"coding":         CapCoding,
	"artifact":       CapCoding,
	"skills":         CapTerminal,
	"terminal":       CapTerminal,
	"computer_use":   CapTerminal,
	"browser":        CapResearch,
	"memory":         CapMemory,
	"support":        CapSupport,
	"tool_use":       CapSupport,
	"mcp":            CapSupport,
	"qa":             CapResearch,
	"research":       CapResearch,
	"safety":         CapSafety,
	"voice":          CapVoice,
	"ml_engineering": CapGPU,
}

// CapabilityOf is the report card capability of a benchmark: words in its id
// first, then its track, else coding (the default track).
func CapabilityOf(benchmarkID, track string) string {
	id := strings.ToLower(benchmarkID)
	for _, m := range idCapability {
		for _, w := range m.words {
			if strings.Contains(id, w) {
				return m.cap
			}
		}
	}
	if c, ok := trackCapability[strings.ToLower(strings.TrimSpace(track))]; ok {
		return c
	}
	return CapCoding
}
