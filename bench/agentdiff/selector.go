package agentdiff

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// minPrefix is the shortest config hash prefix a selector may use.
const minPrefix = 8

// armRecord is one experiment arm with its resolved config.
type armRecord struct {
	ref  ArmRef
	m    *schemas.Manifest
	arm  schemas.ArmManifest
	hash string
}

// resolved is one side of the diff after selection.
type resolved struct {
	selector string
	hash     string
	expID    string // set when picked by <exp_id>:<arm>
	arm      string
	rec      *armRecord // the arm whose resolved config the config diff reads
}

// Versions lists every config hash the manifests resolve, newest first, with
// the arms that ran it and their sealed run counts. It is what a picker
// offers; the counts are not seal-verified (Diff verifies what it uses).
func Versions(src Source) VersionList {
	arms := collectArms(src)
	byHash := map[string]*VersionInfo{}
	var order []string
	for _, a := range arms {
		v, ok := byHash[a.hash]
		if !ok {
			v = &VersionInfo{ConfigHash: a.hash, AgentID: a.arm.Config.ID, Label: a.ref.Label, Suites: []string{}, Arms: []ArmRef{}}
			byHash[a.hash] = v
			order = append(order, a.hash)
		}
		v.Arms = append(v.Arms, a.ref)
		if !slices.Contains(v.Suites, a.ref.Suite) {
			v.Suites = append(v.Suites, a.ref.Suite)
		}
		if a.ref.CreatedAt.After(v.LatestAt) {
			v.LatestAt, v.Label, v.AgentID = a.ref.CreatedAt, a.ref.Label, a.arm.Config.ID
		}
		for _, r := range src.Runs(a.ref.ExpID) {
			if r.Sealed && r.Slot.Arm == a.ref.Arm {
				v.SealedRuns++
			}
		}
	}
	out := VersionList{Versions: make([]VersionInfo, 0, len(order))}
	for _, h := range order {
		out.Versions = append(out.Versions, *byHash[h])
	}
	slices.SortStableFunc(out.Versions, func(x, y VersionInfo) int {
		return cmp.Or(y.LatestAt.Compare(x.LatestAt), cmp.Compare(x.ConfigHash, y.ConfigHash))
	})
	return out
}

// collectArms reads every manifest's arms, newest manifest first, ties by
// experiment then arm id. The compatibility arm is left out: its runs never
// enter a verdict.
func collectArms(src Source) []*armRecord {
	var out []*armRecord
	for _, id := range src.Experiments() {
		m, ok := src.Manifest(id)
		if !ok || m == nil {
			continue
		}
		suite := m.SuiteID + "@" + m.SuiteVersion
		for armID, a := range m.Arms {
			hash := a.ConfigHash
			if hash == "" {
				continue
			}
			label := a.Label
			if label == "" {
				label = a.Config.ID
			}
			out = append(out, &armRecord{
				ref: ArmRef{ExpID: id, Arm: armID, Label: label, Suite: suite, CreatedAt: m.CreatedAt},
				m:   m, arm: a, hash: hash,
			})
		}
	}
	slices.SortStableFunc(out, func(x, y *armRecord) int {
		return cmp.Or(y.ref.CreatedAt.Compare(x.ref.CreatedAt), cmp.Compare(x.ref.ExpID, y.ref.ExpID), cmp.Compare(x.ref.Arm, y.ref.Arm))
	})
	return out
}

// resolvePair picks the two versions of a query. With A and B empty and an
// agent id, B is that agent's newest config hash on the suite and A the one
// before it. Every failure wraps ErrBadQuery.
func resolvePair(arms []*armRecord, q Query) (resolved, resolved, error) {
	if q.A == "" && q.B == "" {
		if q.Agent == "" {
			return resolved{}, resolved{}, fmt.Errorf("%w: give two versions (a and b) or an agent id", ErrBadQuery)
		}
		return latestTwo(arms, q.Agent, q.Suite)
	}
	if q.A == "" || q.B == "" {
		return resolved{}, resolved{}, fmt.Errorf("%w: give both versions a and b", ErrBadQuery)
	}
	a, err := resolveOne(arms, q.A)
	if err != nil {
		return resolved{}, resolved{}, fmt.Errorf("version a: %w", err)
	}
	b, err := resolveOne(arms, q.B)
	if err != nil {
		return resolved{}, resolved{}, fmt.Errorf("version b: %w", err)
	}
	return a, b, nil
}

// resolveOne resolves one selector: <exp_id>:<arm>, a full config hash, or
// a unique prefix of at least minPrefix characters. A hash reads its config
// from the newest arm that resolved to it.
func resolveOne(arms []*armRecord, sel string) (resolved, error) {
	sel = strings.TrimPrefix(strings.TrimSpace(sel), "sha256:")
	if expID, armID, ok := strings.Cut(sel, ":"); ok {
		for _, a := range arms {
			if a.ref.ExpID == expID && a.ref.Arm == armID {
				return resolved{selector: sel, hash: a.hash, expID: expID, arm: armID, rec: a}, nil
			}
		}
		return resolved{}, fmt.Errorf("%w: no experiment %q with arm %q", ErrBadQuery, expID, armID)
	}
	if !isHex(sel) {
		return resolved{}, fmt.Errorf("%w: %q is neither a config hash nor <exp_id>:<arm>", ErrBadQuery, sel)
	}
	sel = strings.ToLower(sel)
	var hit *armRecord
	matches := map[string]bool{}
	for _, a := range arms {
		h := strings.ToLower(strings.TrimPrefix(a.hash, "sha256:"))
		if h == sel || (len(sel) >= minPrefix && strings.HasPrefix(h, sel)) {
			matches[a.hash] = true
			if hit == nil {
				hit = a
			}
		}
	}
	switch {
	case len(matches) == 0 && len(sel) < minPrefix:
		return resolved{}, fmt.Errorf("%w: hash prefix %q is shorter than %d characters", ErrBadQuery, sel, minPrefix)
	case len(matches) == 0:
		return resolved{}, fmt.Errorf("%w: no arm resolved to config hash %q", ErrBadQuery, sel)
	case len(matches) > 1:
		return resolved{}, fmt.Errorf("%w: hash prefix %q matches %d config hashes; give more characters", ErrBadQuery, sel, len(matches))
	}
	return resolved{selector: sel, hash: hit.hash, rec: hit}, nil
}

// latestTwo returns the agent's two newest config hashes on suite (on the
// suite of its newest arm when suite is empty), older first. Arms of one
// manifest tie on time; the later arm id counts as newer, since B is the
// candidate by convention.
func latestTwo(arms []*armRecord, agent, suite string) (resolved, resolved, error) {
	ordered := slices.Clone(arms)
	slices.SortStableFunc(ordered, func(x, y *armRecord) int {
		return cmp.Or(y.ref.CreatedAt.Compare(x.ref.CreatedAt), cmp.Compare(x.ref.ExpID, y.ref.ExpID), cmp.Compare(y.ref.Arm, x.ref.Arm))
	})
	var seen []*armRecord
	for _, a := range ordered {
		if a.arm.Config.ID != agent {
			continue
		}
		if suite == "" {
			suite = a.ref.Suite
		}
		if a.ref.Suite != suite || slices.ContainsFunc(seen, func(s *armRecord) bool { return s.hash == a.hash }) {
			continue
		}
		seen = append(seen, a)
	}
	if len(seen) < 2 {
		where := ""
		if suite != "" {
			where = " on " + suite
		}
		return resolved{}, resolved{}, fmt.Errorf("%w: agent %q has %d config hash(es)%s; two are needed", ErrBadQuery, agent, len(seen), where)
	}
	b := resolved{selector: seen[0].hash, hash: seen[0].hash, rec: seen[0]}
	a := resolved{selector: seen[1].hash, hash: seen[1].hash, rec: seen[1]}
	return a, b, nil
}

// pickSuite returns the query's suite, or the newest suite both versions ran:
// for a version picked by arm, its experiment's suite. No common suite wraps
// ErrBadQuery.
func pickSuite(arms []*armRecord, a, b resolved, suite string) (string, error) {
	if suite != "" {
		return suite, nil
	}
	suitesOf := func(r resolved) map[string]time.Time {
		out := map[string]time.Time{}
		for _, x := range arms {
			if x.hash != r.hash || (r.expID != "" && (x.ref.ExpID != r.expID || x.ref.Arm != r.arm)) {
				continue
			}
			if t, ok := out[x.ref.Suite]; !ok || x.ref.CreatedAt.After(t) {
				out[x.ref.Suite] = x.ref.CreatedAt
			}
		}
		return out
	}
	sa, sb := suitesOf(a), suitesOf(b)
	best, bestAt := "", time.Time{}
	for s, ta := range sa {
		tb, ok := sb[s]
		if !ok {
			continue
		}
		t := ta
		if tb.After(t) {
			t = tb
		}
		if best == "" || t.After(bestAt) || (t.Equal(bestAt) && s < best) {
			best, bestAt = s, t
		}
	}
	if best == "" {
		return "", fmt.Errorf("%w: versions a and b never ran the same suite version; nothing to compare", ErrBadQuery)
	}
	return best, nil
}

// isHex reports whether s is a non-empty run of hex digits.
func isHex(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
