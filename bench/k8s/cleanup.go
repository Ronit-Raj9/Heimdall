package k8s

import (
	"strings"
	"time"
)

// CleanupInput is what `k8s cleanup` knows when it decides which bench
// namespaces are leftovers.
type CleanupInput struct {
	Prefix string          // this install's namespace prefix; other installs' namespaces are never selected
	Live   map[string]bool // runs whose slot lease is fresh: kept
	Dead   map[string]bool // runs whose lease is stale or that are sealed: removed
	Grace  time.Duration   // a namespace with no lease information is removed only when older than this
	Now    time.Time
}

// CleanupVerdict is one namespace and whether cleanup removes it, with why.
type CleanupVerdict struct {
	Namespace Namespace
	Remove    bool
	Reason    string
}

// ClassifyCleanup decides, for each bench namespace, whether it is a
// leftover of this install. Only namespaces named <prefix>-... are
// considered. A run namespace is removed when its run is dead, kept when it
// is live, and otherwise removed only when its creation time is readable
// and older than the grace period; a check namespace likewise by age. A
// namespace whose age cannot be read is kept.
func ClassifyCleanup(items []Namespace, in CleanupInput) []CleanupVerdict {
	prefix := strings.Trim(nsBad.ReplaceAllString(strings.ToLower(in.Prefix), "-"), "-") + "-"
	var out []CleanupVerdict
	for _, n := range items {
		if !strings.HasPrefix(n.Metadata.Name, prefix) || n.Metadata.Labels[LabelRole] == RoleGW {
			continue
		}
		v := CleanupVerdict{Namespace: n}
		run := n.Metadata.Labels[LabelRun]
		created, err := time.Parse(time.RFC3339, n.Metadata.CreationTimestamp)
		switch {
		case run != "" && in.Live[run]:
			v.Reason = "run is live"
		case run != "" && in.Dead[run]:
			v.Remove, v.Reason = true, "run is no longer live"
		case err != nil:
			v.Reason = "age unknown"
		case in.Now.Sub(created) > in.Grace:
			v.Remove, v.Reason = true, "older than "+in.Grace.String()
		default:
			v.Reason = "younger than " + in.Grace.String() + " with no lease information"
		}
		out = append(out, v)
	}
	return out
}
