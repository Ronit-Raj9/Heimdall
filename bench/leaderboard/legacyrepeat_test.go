package leaderboard

import (
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A legacy score-mode experiment held a hidden "(repeat)" copy of its one
// arm. The board leaves the copy out, so only the asked-for setup shows.
func TestBuildDropsLegacyRepeatArm(t *testing.T) {
	f := newFake()
	m := f.exp("e_score", "s@1", 0, map[string]schemas.ArmManifest{
		"A": arm("codex on m", "h_a", "command"),
		"B": arm("codex on m (repeat)", "h_rep", "command"),
	})
	m.Experiment.Arms = map[string]schemas.ArmSpec{
		"A": {Label: "codex on m"},
		"B": {Label: "codex on m (repeat)", Set: map[string]any{"env.HEIMDALL_QUICKRUN_ARM": "B"}},
	}
	for _, task := range []string{"t1", "t2"} {
		f.pass("e_score", task, "A", "clean", 1, 0.1)
		f.pass("e_score", task, "B", "clean", 1, 0.1)
	}
	lb := buildOrFail(t, f, Query{Now: t0})
	if got := entryHashes(lb); !slices.Equal(got, []string{"h_a"}) {
		t.Fatalf("entries %v, want only h_a", got)
	}
	// A real arm B whose label merely ends in "(repeat)" stays.
	m.Experiment.Arms["B"] = schemas.ArmSpec{Label: "codex on m (repeat)"}
	lb = buildOrFail(t, f, Query{Now: t0})
	if got := entryHashes(lb); len(got) != 2 {
		t.Fatalf("entries %v, want both", got)
	}
}
