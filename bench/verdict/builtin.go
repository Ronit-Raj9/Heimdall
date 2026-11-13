// Package verdict turns sealed runs into the release verdict, the diagnostics
// table and the always-on meters (DESIGN.md section 9). The decision rests on
// one primary endpoint, an exact task-level sign-flip test, and both
// missingness bounds; everything else it reports is descriptive. Audit applies
// the same rule to published per-task pass counts.
package verdict

import "github.com/maximhq/bifrost/bench/schemas"

// Builtin returns the verdict policies that ship with the bench.
func Builtin() []schemas.VerdictPolicy {
	return []schemas.VerdictPolicy{newPairedSignFlipPolicy()}
}
