// Package packs holds the evaluation packs: named groups of benchmarks a user
// picks in one click when they evaluate an agent (audit/AGENTFIRST_CONTRACT.md
// section 3). A pack lists benchmark ids; an id ending in "*" is a prefix
// that matches every available benchmark starting with it. Resolve expands a
// pack against the benchmarks an organization can see, built-in library
// included, so a pack only ever names benchmarks that exist.
package packs

import (
	"slices"
	"sort"
	"strings"
)

// AllCompatible is the pack that holds every available benchmark. Dropping
// the ones the agent cannot run is the caller's job (the compatibility check).
const AllCompatible = "all-compatible"

// Pack is one evaluation pack.
type Pack struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Capability  string   `json:"capability"`
	Benchmarks  []string `json:"benchmarks"` // ids or prefixes ending in "*"
}

var all = []Pack{
	{ID: "coding", Name: "Coding agent", Capability: CapCoding,
		Description: "Fix real bugs and build features in real repositories.",
		Benchmarks:  []string{"hd-harbor-tb2", "hd-swe-verified", "hd-swe-rebench", "hd-swe-ib-lcb"}},
	{ID: "terminal", Name: "Terminal agent", Capability: CapTerminal,
		Description: "Get work done in a shell inside a container.",
		Benchmarks:  []string{"hd-harbor-tb2", "hd-harbor-skills"}},
	{ID: "support", Name: "Support agent", Capability: CapSupport,
		Description: "Help customers with tools while following policy.",
		Benchmarks:  []string{"tau2", "mcpmark", "agentdojo-*"}},
	{ID: "memory", Name: "Memory agent", Capability: CapMemory,
		Description: "Remember facts across long conversations and sessions.",
		Benchmarks:  []string{"longmemeval", "locomo"}},
	{ID: "research", Name: "Research agent", Capability: CapResearch,
		Description: "Search, read and answer hard questions with sources.",
		Benchmarks:  []string{"gaia", "browsecomp-plus-*"}},
	{ID: "voice", Name: "Voice agent", Capability: CapVoice,
		Description: "Understand and answer spoken requests.",
		Benchmarks:  []string{"voicebench"}},
	{ID: "safety", Name: "Safety", Capability: CapSafety,
		Description: "Resist prompt injection and refuse to cheat on tests.",
		Benchmarks:  []string{"agentdojo-*", "hd-swe-ib-swe"}},
	{ID: AllCompatible, Name: "Everything it can run", Capability: "",
		Description: "Every benchmark your agent is able to run.",
		Benchmarks:  []string{"*"}},
}

// All returns every pack in display order. The result is a copy.
func All() []Pack {
	out := make([]Pack, len(all))
	for i, p := range all {
		p.Benchmarks = slices.Clone(p.Benchmarks)
		out[i] = p
	}
	return out
}

// Get returns pack id.
func Get(id string) (Pack, bool) {
	for _, p := range All() {
		if p.ID == id {
			return p, true
		}
	}
	return Pack{}, false
}

// Resolve expands pack id against the available benchmark ids: exact ids that
// are available, and every available id a "*" prefix matches. The result is
// in pack order (matches of one prefix sorted), without duplicates. An
// unknown pack resolves to nil.
func Resolve(id string, available []string) []string {
	p, ok := Get(id)
	if !ok {
		return nil
	}
	have := make(map[string]bool, len(available))
	sorted := make([]string, 0, len(available))
	for _, a := range available {
		if a != "" && !have[a] {
			have[a] = true
			sorted = append(sorted, a)
		}
	}
	sort.Strings(sorted)
	seen := map[string]bool{}
	var out []string
	add := func(b string) {
		if !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	for _, pat := range p.Benchmarks {
		if prefix, wild := strings.CutSuffix(pat, "*"); wild {
			for _, a := range sorted {
				if strings.HasPrefix(a, prefix) {
					add(a)
				}
			}
		} else if have[pat] {
			add(pat)
		}
	}
	return out
}
