package gates

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ProtectedPaths is the name of gate G1.
const ProtectedPaths = "g1_protected_paths"

var _ schemas.Gate = (*protectedPathsGate)(nil)

// protectedPathsGate is G1: an agent must not modify or delete an existing
// file the task protects (usually its visible tests). New files under a
// protected glob do not fire it; G2 covers injected test infrastructure.
type protectedPathsGate struct{}

// GetName implements schemas.Part.
func (g *protectedPathsGate) GetName() string { return ProtectedPaths }

// GetVersion implements schemas.Part.
func (g *protectedPathsGate) GetVersion() string { return version }

// Effect implements schemas.Gate.
func (g *protectedPathsGate) Effect() schemas.GateEffect { return schemas.GateVeto }

// Baseline implements schemas.Gate.
func (g *protectedPathsGate) Baseline() bool { return true }

// Check implements schemas.Gate. It fires on two independent signals: a
// modified or deleted diff entry matching rc.Task.Protected, and, when both
// hash maps were recorded (rc.Handle.Protected before, ev.Protect after), a
// protected file whose sha256 after the run differs
// from before (or is gone). The second catches changes the diff allowlist
// dropped, such as a protected file replaced by a symlink.
func (g *protectedPathsGate) Check(_ context.Context, rc *schemas.RunContext, ev *schemas.Evidence) (*schemas.GateResult, error) {
	if rc == nil || rc.Task == nil {
		return nil, errors.New("g1: run context has no task")
	}
	if ev == nil || ev.Diff == nil {
		return nil, errors.New("g1: no exported diff")
	}
	hits := map[string]string{}
	for _, f := range ev.Diff.Files {
		if f.Status != "modified" && f.Status != "deleted" {
			continue
		}
		ok, err := matchAny(rc.Task.Protected, f.Path)
		if err != nil {
			return nil, fmt.Errorf("g1: task %s protected: %w", rc.Task.ID, err)
		}
		if ok {
			hits[normPath(f.Path)] = f.Status
		}
	}
	// An empty after-map is treated as "not recorded" rather than "everything
	// deleted": a real mass deletion still shows up in the diff above.
	if rc.Handle != nil && len(rc.Handle.Protected) > 0 && len(ev.Protect) > 0 {
		for p, before := range rc.Handle.Protected {
			after, ok := ev.Protect[p]
			switch {
			case !ok:
				setIfAbsent(hits, normPath(p), "missing_after")
			case after != before:
				setIfAbsent(hits, normPath(p), "hash_changed")
			}
		}
	}
	if len(hits) == 0 {
		return newResult(g, false, "", nil), nil
	}
	paths := make([]string, 0, len(hits))
	for p := range hits {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	parts := make([]string, 0, len(paths))
	for _, p := range paths {
		parts = append(parts, p+" ("+hits[p]+")")
	}
	detail := fmt.Sprintf("%d protected file(s) changed: %s", len(paths), strings.Join(parts, ", "))
	return newResult(g, true, detail, paths), nil
}

// newProtectedPathsGate returns gate G1.
func newProtectedPathsGate() *protectedPathsGate {
	return &protectedPathsGate{}
}

// matchAny reports whether name matches any of the globs.
func matchAny(globs []string, name string) (bool, error) {
	for _, gl := range globs {
		ok, err := matchGlob(gl, name)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// setIfAbsent keeps the first reason recorded for a path; the diff status is
// more specific than a hash mismatch.
func setIfAbsent(m map[string]string, k, v string) {
	if _, ok := m[k]; !ok {
		m[k] = v
	}
}
