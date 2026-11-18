package suite

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

func fp(x float64) *float64 { return &x }

func TestTierTasks(t *testing.T) {
	s := &schemas.Suite{ID: "s", TaskIDs: []string{"t01", "t02", "t03"},
		Tiers: map[string]schemas.TaskSelector{"pr": {IDs: []string{"t03", "t01"}}, "bad": {IDs: []string{"t09"}}}}
	got, err := TierTasks(s, "pr")
	if err != nil || strings.Join(got, ",") != "t01,t03" {
		t.Fatalf("pr %v %v", got, err)
	}
	if got, err := TierTasks(s, "nightly"); err != nil || len(got) != 3 {
		t.Fatalf("nightly %v %v", got, err)
	}
	if _, err := TierTasks(s, "headroom"); err == nil || !strings.Contains(err.Error(), "tiers suggest") {
		t.Fatalf("undefined tier: %v", err)
	}
	if _, err := TierTasks(s, "bad"); err == nil {
		t.Fatal("a tier naming an unknown task was accepted")
	}
}

func TestCompareTiers(t *testing.T) {
	a := &schemas.Manifest{ExpID: "a", Tier: &schemas.TierPlan{Name: "pr"}, Slots: []schemas.Slot{{Task: "t01", Counted: true}, {Task: "t02", Counted: true}}}
	b := &schemas.Manifest{ExpID: "b", Slots: []schemas.Slot{{Task: "t02", Counted: true}, {Task: "t03", Counted: true}}}
	if _, err := CompareTiers(a, b, false); err == nil || !strings.Contains(err.Error(), "different task tiers") {
		t.Fatalf("different tiers compared: %v", err)
	}
	common, err := CompareTiers(a, b, true)
	if err != nil || strings.Join(common, ",") != "t02" {
		t.Fatalf("restricted %v %v", common, err)
	}
	if c, err := CompareTiers(a, a, false); err != nil || c != nil {
		t.Fatalf("same tier %v %v", c, err)
	}
}

func TestSuggestTiers(t *testing.T) {
	cands := []TierCandidate{
		{Task: "t01", PassRate: fp(0.5), Discrimination: "discriminating", Spread: fp(0.6), WallS: fp(60)},
		{Task: "t02", PassRate: fp(1), Discrimination: "always_pass", WallS: fp(30)},
		{Task: "t03", PassRate: fp(0.2), Discrimination: "discriminating", Spread: fp(0.3), WallS: fp(900)},
		{Task: "t04", PassRate: fp(0.4), Discrimination: "discriminating", Spread: fp(0.5), WallS: fp(120), Flaky: true},
		{Task: "t05", PassRate: fp(0.3), Discrimination: "single_config", WallS: fp(100)},
		{Task: "t06", PassRate: fp(0.6), Discrimination: "discriminating"},
	}
	s := SuggestTiers("s", cands, SuggestOptions{MaxMinutes: 10, Workers: 2})
	pr := strings.Join(s.Tiers[TierPR].IDs, ",")
	// t01 fits (2 runs x 60 s), t03 would need 1800 s of a 1200 s budget,
	// t02 never discriminates, t04 is flaky, t06 has no wall time; t05 fits.
	if pr != "t01,t05" {
		t.Fatalf("pr %q, reasons %v", pr, s.Reasons)
	}
	if got := strings.Join(s.Tiers[TierHeadroom].IDs, ","); got != "t03,t05" {
		t.Fatalf("headroom %q", got)
	}
	if !s.Tiers[TierNightly].All {
		t.Fatal("nightly is not every task")
	}
	if s.PRMinutes == nil || *s.PRMinutes > 10 || len(s.Notes) == 0 {
		t.Fatalf("pr minutes %v notes %v", s.PRMinutes, s.Notes)
	}
}

func TestPresetsDecode(t *testing.T) {
	for _, name := range []string{"pr-smoke", "nightly", "headroom"} {
		var e schemas.Experiment
		path := "../experiments/presets/" + name + ".yaml"
		if err := DecodeYAMLFile(path, path, SchemaExperiment, &e); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if e.Tier == "" {
			t.Fatalf("%s has no tier", name)
		}
	}
}
