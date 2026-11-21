// Package packs maps an evaluation pack ("coding", "support", ...) to the
// benchmarks it holds. This is a minimal version for lane A02; lane A03 owns
// the package and replaces it.
package packs

import (
	"path"
	"sort"
)

// table lists each pack's benchmark ids; a trailing "*" matches by prefix.
var table = map[string][]string{
	"coding":   {"hd-harbor-tb2", "hd-swe-verified", "hd-swe-rebench", "hd-swe-ib-lcb"},
	"terminal": {"hd-harbor-tb2", "hd-harbor-skills"},
	"support":  {"tau2", "mcpmark", "agentdojo-*"},
	"memory":   {"longmemeval", "locomo"},
	"research": {"gaia", "browsecomp-plus-*"},
	"voice":    {"voicebench"},
	"safety":   {"agentdojo-*", "hd-swe-ib-swe"},
}

// AllCompatible is the pack of every available benchmark; the caller drops
// the ones the agent cannot run.
const AllCompatible = "all-compatible"

// Resolve returns the benchmarks of pack id that are in available, sorted.
// An unknown pack gives nil.
func Resolve(id string, available []string) []string {
	if id == AllCompatible {
		out := append([]string(nil), available...)
		sort.Strings(out)
		return out
	}
	pats, ok := table[id]
	if !ok {
		return nil
	}
	var out []string
	for _, a := range available {
		for _, p := range pats {
			if m, _ := path.Match(p, a); m {
				out = append(out, a)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
