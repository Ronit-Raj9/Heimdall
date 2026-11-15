package leaderboard

import (
	"slices"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The held-out board (docs/held-out-and-canaries.md). A run is held out when
// its experiment's manifest lists its task in held_out_tasks: the split is
// read from the frozen plan, never from the task files today, so moving a
// task between splits later does not move old runs between boards. The
// public board, the default, never counts a held-out run; the held-out board
// counts nothing else.

// validSplit reports whether s is a board split; empty means public.
func validSplit(s string) bool {
	return s == "" || s == schemas.SplitPublic || s == schemas.SplitHeldOut
}

// inSplit reports whether the record belongs on the board of split.
func inSplit(r *record, split string) bool {
	held := r.exp != nil && r.exp.m != nil && slices.Contains(r.exp.m.HeldOutTasks, r.slot.Task)
	return held == (split == schemas.SplitHeldOut)
}

// boardSplit is what the board states: held_out on the held-out board, and
// nothing on the public one, so a public board encodes as before.
func boardSplit(split string) string {
	if split == schemas.SplitHeldOut {
		return schemas.SplitHeldOut
	}
	return ""
}
