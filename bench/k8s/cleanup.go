package k8s

import (
	"strings"
	"time"
)

// CleanupInput is what `k8s cleanup` knows when it decides which bench
// namespaces are leftovers.
type CleanupInput struct {
	Prefix string // this install's namespace prefix; other installs' namespaces are never selected
	// Install is this install's owner id (InstallID). A namespace labelled
	// with another install's id is never selected, and one with no owner
	// label (created before the label existed) only when this install's
	// store shows its run ended: its age alone proves nothing (QC2-10-02).
	Install string
	Live    map[string]bool // runs whose slot lease is fresh: kept
	Dead    map[string]bool // runs whose lease is stale or that are sealed: removed
	Grace   time.Duration   // a namespace with no lease information is removed only when older than this
	Now     time.Time
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
		owner := n.Metadata.Labels[LabelInstall]
		if in.Install != "" && owner != "" && owner != in.Install {
			continue
		}
		legacy := in.Install != "" && owner == ""
		v := CleanupVerdict{Namespace: n}
		run := n.Metadata.Labels[LabelRun]
		created, err := time.Parse(time.RFC3339, n.Metadata.CreationTimestamp)
		switch {
		case run != "" && in.Live[run]:
			v.Reason = "run is live"
		case run != "" && in.Dead[run]:
			v.Remove, v.Reason = true, "run is no longer live"
		case legacy:
			v.Reason = "no install owner label; removed only when this install's store shows its run ended"
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

// PoolAnchor is the anchor config map found in one pool namespace.
type PoolAnchor struct {
	Namespace string
	Anchor    ConfigMap
}

// PoolVerdict is one pool anchor and whether cleanup removes it, with why.
type PoolVerdict struct {
	PoolAnchor
	Remove bool
	Reason string
}

// ClassifyPoolAnchors decides, in namespace pool mode, which anchors are
// stale leases of this install. Only anchors carrying this install's owner
// label are considered (a pool may be shared with another install); the
// namespace itself is never a candidate. A run anchor is removed when its
// run is dead, kept when it is live, and otherwise removed only when older
// than the grace period; a check anchor likewise by age.
func ClassifyPoolAnchors(items []PoolAnchor, in CleanupInput) []PoolVerdict {
	var out []PoolVerdict
	for _, a := range items {
		l := a.Anchor.Metadata.Labels
		if l[LabelBench] != "1" || in.Install == "" || l[LabelInstall] != in.Install {
			continue
		}
		v := PoolVerdict{PoolAnchor: a}
		run := anchorRun(a.Anchor)
		created, err := time.Parse(time.RFC3339, a.Anchor.Metadata.CreationTimestamp)
		switch {
		case l[LabelRole] == RoleRun && in.Live[run]:
			v.Reason = "run is live"
		case l[LabelRole] == RoleRun && in.Dead[run]:
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
