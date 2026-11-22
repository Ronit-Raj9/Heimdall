package tracebulk

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/traceinbox"
)

// Clusters groups inbox items into failure modes. Items fail the same way
// when they share the first failing tool and its error line (the inbox
// signature's tool and error parts), or, when no tool failed, the flags
// that raised them (the same failing evaluator, the same low score). The
// request part of the signature is ignored on purpose: one failure mode
// spans many different user requests. Clusters are ordered by occurrences,
// most first; items in a cluster too.
func Clusters(items []traceinbox.Item) []Cluster {
	type group struct {
		key   string
		items []traceinbox.Item
	}
	groups := map[string]*group{}
	var keys []string
	for _, it := range items {
		k := it.SignatureParts.Tool + "\x1f" + it.SignatureParts.Error
		g, ok := groups[k]
		if !ok {
			g = &group{key: k}
			groups[k] = g
			keys = append(keys, k)
		}
		g.items = append(g.items, it)
	}
	out := make([]Cluster, 0, len(keys))
	for _, k := range keys {
		g := groups[k]
		sort.SliceStable(g.items, func(i, j int) bool {
			a, b := g.items[i], g.items[j]
			if a.Occurrences != b.Occurrences {
				return a.Occurrences > b.Occurrences
			}
			if !a.LastSeen.Equal(b.LastSeen) {
				return a.LastSeen.After(b.LastSeen)
			}
			return a.ID < b.ID
		})
		top := g.items[0]
		sum := sha256.Sum256([]byte(k))
		c := Cluster{ID: "c_" + hex.EncodeToString(sum[:5]), Tool: top.SignatureParts.Tool, Error: top.SignatureParts.Error,
			Sample: top.Title, Items: []string{}}
		c.Evaluator = evaluatorOf(c.Error)
		c.Label = label(c.Tool, c.Error)
		if c.Tool == "" && c.Evaluator == "" {
			// A failed evaluator also marks the session errored, so its
			// error line reads "error: maxim evaluator <name> failed" in
			// lower case; the item's reasons keep the name as Maxim has it.
			if m := evalLine.FindStringSubmatch(c.Error); m != nil {
				c.Evaluator = m[1]
				for _, r := range top.Flagged {
					if m2 := evalReason.FindStringSubmatch(r); m2 != nil && strings.EqualFold(m2[1], m[1]) {
						c.Evaluator = m2[1]
					}
				}
				c.Label = "Maxim evaluator " + c.Evaluator + " failed"
			}
		}
		kinds := map[string]int{}
		for _, it := range g.items {
			c.Items = append(c.Items, it.ID)
			c.Occurrences += max(it.Occurrences, 1)
			kinds[it.SuggestedKind]++
			for _, r := range it.Flagged {
				if len(c.Reasons) < maxClusterReasons && !slices.Contains(c.Reasons, r) {
					c.Reasons = append(c.Reasons, r)
				}
			}
			if it.Link != "" && len(c.Links) < maxClusterLinks {
				c.Links = append(c.Links, it.Link)
			}
		}
		best := 0
		for kind, n := range kinds {
			if n > best || (n == best && kind < c.Kind) {
				c.Kind, best = kind, n
			}
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Occurrences != out[j].Occurrences {
			return out[i].Occurrences > out[j].Occurrences
		}
		return out[i].Label < out[j].Label
	})
	return out
}

var (
	evalLine   = regexp.MustCompile(`^(?:error: )?maxim evaluator (.+?) failed$`)
	evalReason = regexp.MustCompile(`^Maxim evaluator (.+?) failed`)
)

// flagKinds splits an error part "flag:a,b" into its kinds.
func flagKinds(errPart string) []string {
	rest, ok := strings.CutPrefix(errPart, "flag:")
	if !ok {
		return nil
	}
	return strings.Split(rest, ",")
}

// evaluatorOf is the first failing or low-scoring evaluator a flag error
// part names.
func evaluatorOf(errPart string) string {
	for _, k := range flagKinds(errPart) {
		for _, p := range []string{traceinbox.FlagEvaluator + ":", traceinbox.FlagScore + ":"} {
			if name, ok := strings.CutPrefix(k, p); ok {
				return name
			}
		}
	}
	return ""
}

// label names a failure mode in plain words.
func label(tool, errPart string) string {
	kinds := flagKinds(errPart)
	switch {
	case tool != "" && errPart != "" && kinds == nil:
		return tool + " fails: " + oneLine(errPart, 120)
	case tool != "":
		return tool + " returns an error"
	case kinds == nil && errPart != "":
		return "Errors with: " + oneLine(errPart, 120)
	}
	var parts []string
	for _, k := range kinds {
		switch {
		case strings.HasPrefix(k, traceinbox.FlagEvaluator+":"):
			parts = append(parts, "Maxim evaluator "+strings.TrimPrefix(k, traceinbox.FlagEvaluator+":")+" failed")
		case strings.HasPrefix(k, traceinbox.FlagScore+":"):
			parts = append(parts, "low "+strings.TrimPrefix(k, traceinbox.FlagScore+":")+" score")
		case k == traceinbox.FlagError:
			parts = append(parts, "trace errored")
		default:
			parts = append(parts, k)
		}
	}
	if len(parts) == 0 {
		return "Failed traces"
	}
	s := strings.Join(parts, ", ")
	return strings.ToUpper(s[:1]) + s[1:]
}
