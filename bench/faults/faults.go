// Package faults maps evaluation faults to experiment conditions. This is a
// minimal version for lane A02; lane A07 owns the package and replaces it.
package faults

import (
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Evaluation faults.
const (
	Clean                 = "clean"
	ModelDown             = "model-down"
	BrokenTool            = "broken-tool"
	MisleadingInstruction = "misleading-instruction"
)

// Skip is a fault that has no condition on a benchmark, with the reason.
type Skip struct {
	Fault  string `json:"fault"`
	Reason string `json:"reason"`
}

// Conditions returns the experiment condition ids for faults on suite s:
// "clean" always first, then one per fault the suite supports. Faults
// without a condition come back as skips.
func Conditions(faults []string, s *schemas.Suite) ([]string, []Skip) {
	out := []string{schemas.ConditionClean}
	seen := map[string]bool{schemas.ConditionClean: true}
	var skips []Skip
	for _, f := range faults {
		f = strings.TrimSpace(f)
		if f == "" || f == Clean || seen["fault:"+f] {
			continue
		}
		seen["fault:"+f] = true
		switch f {
		case MisleadingInstruction:
			id := misleadingProbe(s)
			if id == "" {
				skips = append(skips, Skip{Fault: f, Reason: "this benchmark has no misleading-instruction probe"})
				continue
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		case ModelDown, BrokenTool:
			skips = append(skips, Skip{Fault: f, Reason: "this fault is not available on this server yet"})
		default:
			skips = append(skips, Skip{Fault: f, Reason: "unknown fault"})
		}
	}
	return out, skips
}

func misleadingProbe(s *schemas.Suite) string {
	if s == nil {
		return ""
	}
	ids := append([]string(nil), s.ProbeIDs...)
	for _, p := range s.Probes {
		if p != nil {
			ids = append(ids, p.ID)
		}
	}
	for _, id := range ids {
		l := strings.ToLower(id)
		if strings.Contains(l, "misleading") || strings.Contains(l, "stale") {
			return id
		}
	}
	return ""
}
