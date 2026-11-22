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

// A score-mode stream with memory carried runs a visible "(no memory)"
// control of its one arm for forward transfer; the board leaves it out.
func TestBuildDropsStreamControlArm(t *testing.T) {
	f := newFake()
	m := f.exp("e_stream", "s@1", 0, map[string]schemas.ArmManifest{
		"A":       arm("codex on m", "h_a", "command"),
		"control": arm("codex on m (no memory)", "h_ctl", "command"),
	})
	m.Experiment.Stream = &schemas.StreamSpec{Memory: schemas.StreamMemoryCarry, FreshArm: "control"}
	for _, task := range []string{"t1", "t2"} {
		f.pass("e_stream", task, "A", "clean", 1, 0.1)
		f.pass("e_stream", task, "control", "clean", 1, 0.1)
	}
	lb := buildOrFail(t, f, Query{Now: t0})
	if got := entryHashes(lb); !slices.Equal(got, []string{"h_a"}) {
		t.Fatalf("entries %v, want only h_a", got)
	}
	// Without memory carried the fresh arm is an ordinary arm.
	m.Experiment.Stream.Memory = schemas.StreamMemoryFresh
	lb = buildOrFail(t, f, Query{Now: t0})
	if got := entryHashes(lb); len(got) != 2 {
		t.Fatalf("entries %v, want both", got)
	}
}

// An evaluation with the Bifrost cache option runs a "(Bifrost cache on)"
// copy of its one arm (lane C08). It is a variant, not a separate setup:
// the board leaves it out.
func TestBuildDropsCacheVariantArm(t *testing.T) {
	f := newFake()
	m := f.exp("e_cache", "s@1", 0, map[string]schemas.ArmManifest{
		"A":                        arm("codex on m", "h_a", "command"),
		schemas.EvaluationCacheArm: arm("codex on m"+schemas.EvaluationCacheSuffix, "h_cache", "command"),
	})
	for _, task := range []string{"t1", "t2"} {
		f.pass("e_cache", task, "A", "clean", 1, 0.1)
		f.pass("e_cache", task, schemas.EvaluationCacheArm, "clean", 1, 0.1)
	}
	lb := buildOrFail(t, f, Query{Now: t0})
	if got := entryHashes(lb); !slices.Equal(got, []string{"h_a"}) {
		t.Fatalf("entries %v, want only h_a", got)
	}
	// An arm named cache without the cache label is an ordinary arm.
	m.Arms[schemas.EvaluationCacheArm] = arm("my cache agent", "h_cache", "command")
	lb = buildOrFail(t, f, Query{Now: t0})
	if got := entryHashes(lb); len(got) != 2 {
		t.Fatalf("entries %v, want both", got)
	}
}
