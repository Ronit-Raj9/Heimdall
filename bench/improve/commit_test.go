package improve

import (
	"math"
	"strings"
	"testing"
)

func gateSpec(alpha float64, looks int) *Spec {
	return &Spec{Rounds: looks, Commit: CommitSpec{Alpha: alpha, AuditLooks: looks}}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-4 }

func TestFallbackLevelsAfterCommits(t *testing.T) {
	rule := fallbackSignFlip{}
	s := gateSpec(0.10, 3)
	var looks []LookRecord
	want := []float64{0.0333, 0.0667, 0.1}
	for k, w := range want {
		l := rule.Level(s, looks)
		if !near(l, w) {
			t.Fatalf("look %d: level %v, want %v", k+1, l, w)
		}
		looks = append(looks, LookRecord{Round: k + 1, Level: l, Committed: true})
	}
	if l := rule.Level(s, looks); l != 0 {
		t.Fatalf("after every look: level %v, want 0", l)
	}
}

func TestFallbackLevelWithoutCommitDoesNotCarry(t *testing.T) {
	rule := fallbackSignFlip{}
	s := gateSpec(0.10, 3)
	looks := []LookRecord{{Round: 1, Level: 0.1 / 3, Committed: false}}
	if l := rule.Level(s, looks); !near(l, 0.0333) {
		t.Fatalf("level %v, want 0.0333", l)
	}
	// A round 1 commit then a round 2 rejection: look 3 gets only its own share.
	looks = []LookRecord{{Round: 1, Level: 0.1 / 3, Committed: true}, {Round: 2, Level: 0.2 / 3}}
	if l := rule.Level(s, looks); !near(l, 0.0333) {
		t.Fatalf("level %v, want 0.0333", l)
	}
}

func TestSkippedLookSpendsNothing(t *testing.T) {
	rule := fallbackSignFlip{}
	d := rule.Decide([]float64{1, 1, 1, 1, 1, 1, 1}, 3, 10, 0.05)
	if !d.Skipped || d.Commit {
		t.Fatalf("missing 3 of 10 should skip: %+v", d)
	}
	if d.Why != "the gate test did not run: 3 of 10 gate tasks had no result on one of the versions" {
		t.Fatalf("why %q", d.Why)
	}
	// Exactly 20% missing still runs.
	d = rule.Decide([]float64{1, 1, 1, 1, 1, 1, 1, 1}, 2, 10, 0.05)
	if d.Skipped || !d.Commit {
		t.Fatalf("missing 2 of 10 should run and commit: %+v", d)
	}
	// The runner records looks only for decisions that did not skip, so the next level is unchanged.
	if l := rule.Level(gateSpec(0.10, 3), nil); !near(l, 0.0333) {
		t.Fatalf("level %v", l)
	}
}

func TestCommitRefusesWhenTooFewDiscordant(t *testing.T) {
	rule := fallbackSignFlip{}
	// Four positive tasks: the smallest p is 1/16 = 0.0625, above 0.05.
	d := rule.Decide([]float64{1, 0, 1, 0, 1, 1}, 0, 6, 0.05)
	if d.Commit || d.Skipped || d.Why != "the two versions differed on too few gate tasks for any test to pass" {
		t.Fatalf("%+v", d)
	}
	if d.N != 6 || d.NNonzero != 4 {
		t.Fatalf("counts %+v", d)
	}
	d = rule.Decide(nil, 0, 0, 0.05)
	if d.Commit || d.Skipped || d.Why != "the two versions differed on too few gate tasks for any test to pass" {
		t.Fatalf("empty: %+v", d)
	}
}

func TestCommitDecides(t *testing.T) {
	rule := fallbackSignFlip{}
	d := rule.Decide([]float64{0.5, 1, 1, 0.5, 1, 1, 0.5, 1}, 0, 8, 0.0333)
	if !d.Commit || d.P > 0.0333 || !near(d.Mean, 0.8125) {
		t.Fatalf("%+v", d)
	}
	if !strings.HasPrefix(d.Why, "Kept: better on 8 gate tasks by +81 points (p 0.0039, needed 0.033).") {
		t.Fatalf("why %q", d.Why)
	}
	d = rule.Decide([]float64{0.5, -1, 1, 0.5, -1, 1, 0.5, -0.5}, 0, 8, 0.0333)
	if d.Commit || d.Skipped {
		t.Fatalf("%+v", d)
	}
	if !strings.HasPrefix(d.Why, "Not kept: +12 points on 8 gate tasks (p ") {
		t.Fatalf("why %q", d.Why)
	}
}

func TestFormatPoints(t *testing.T) {
	for in, want := range map[float64]string{0.81: "+81", -0.123: "-12", 0.05: "+5.0", -0.034: "-3.4", 0: "0.0", 0.1: "+10.0"} {
		if got := formatPoints(in); got != want {
			t.Errorf("%v: %q, want %q", in, got, want)
		}
	}
	for in, want := range map[float64]string{0.0039: "0.0039", 0.0333: "0.033", 0.5: "0.50", 1: "1.0", 0.12345: "0.12"} {
		if got := formatP(in); got != want {
			t.Errorf("p %v: %q, want %q", in, got, want)
		}
	}
}
