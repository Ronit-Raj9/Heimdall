package verdict

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// withSlots returns m with the slots of runs as its assigned slots.
func withSlots(m *schemas.Manifest, runs []schemas.SealedRun) *schemas.Manifest {
	for _, r := range runs {
		m.Slots = append(m.Slots, r.Slot)
	}
	return m
}

// without drops the runs for which drop is true.
func without(runs []schemas.SealedRun, drop func(r schemas.SealedRun) bool) []schemas.SealedRun {
	var out []schemas.SealedRun
	for _, r := range runs {
		if !drop(r) {
			out = append(out, r)
		}
	}
	return out
}

func checkNamed(checks []schemas.InstrumentCheck, name string) *schemas.InstrumentCheck {
	for i := range checks {
		if checks[i].Name == name {
			return &checks[i]
		}
	}
	return nil
}

func TestCompleteSlots(t *testing.T) {
	clean := schemas.ConditionClean
	all := concat(pattern(clean, ArmBaseline, "1"), pattern(clean, ArmCandidate, "1"))
	compat := sealedRun("t01", "compat", clean, 1, schemas.Observed(1, schemas.EvidenceOutcome))
	compat.Slot.Counted = false
	m := withSlots(testManifest(ArmBaseline, ArmCandidate), append(all, compat))
	tests := []struct {
		name    string
		runs    []schemas.SealedRun
		added   int
		missing string
	}{
		{"every slot has a run", all, 0, ""},
		{"a missing counted slot gets a placeholder", all[:1], 1, all[1].Slot.SlotID},
		{"a missing compatibility slot does not", all, 0, ""},
		{"nothing at all", nil, 2, all[0].Slot.SlotID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompleteSlots(m, tt.runs)
			if len(got) != len(tt.runs)+tt.added {
				t.Fatalf("%d runs, want %d", len(got), len(tt.runs)+tt.added)
			}
			if again := CompleteSlots(m, got); len(again) != len(got) {
				t.Fatalf("second call added %d runs", len(again)-len(got))
			}
			if tt.added == 0 {
				return
			}
			p := got[len(tt.runs)]
			if p.Slot.SlotID != tt.missing || p.Run.RunID != "" || p.Run.Outcome != schemas.OutcomeUnknown || metricValue(p, MetricVerifiedSuccess).IsObserved() {
				t.Fatalf("placeholder %+v", p)
			}
		})
	}
	if got := CompleteSlots(testManifest(ArmBaseline, ArmCandidate), all[:1]); len(got) != 1 {
		t.Fatalf("a manifest without slots added %d runs", len(got)-1)
	}
}

func TestDecideMissingSlotsCountAsUnknown(t *testing.T) {
	clean := schemas.ConditionClean
	full := concat(passes(clean, ArmBaseline, 3, 3, 3, 3, 3, 3, 3, 3, 3), passes(clean, ArmCandidate, 3, 3, 3, 3, 3, 3, 3, 3, 3))
	m := withSlots(testManifest(ArmBaseline, ArmCandidate), full)
	tests := []struct {
		name        string
		runs        []schemas.SealedRun
		invalid     string // a substring of some invalid reason, "" for none
		coverage    schemas.InstrumentStatus
		unknownB    int // unknown B runs on t02 in the primary effect
		tasksPaired int
	}{
		{"complete", full, "", schemas.InstrumentPass, 0, 8},
		{"one missing B run is an unknown, not unequal trials", without(full, func(r schemas.SealedRun) bool {
			return r.Slot.Task == "t02" && r.Slot.Arm == ArmCandidate && r.Slot.Trial == 3
		}), "", schemas.InstrumentWarn, 1, 8},
		{"a task with no run at all stays in and trips the coverage gate", without(full, func(r schemas.SealedRun) bool {
			return r.Slot.Task == "t02"
		}), "graded coverage", schemas.InstrumentWarn, 3, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := decideOrFail(t, m, tt.runs)
			reasons := strings.Join(a.Verdict.InvalidReasons, "\n")
			if (tt.invalid == "") != (len(a.Verdict.InvalidReasons) == 0) || !strings.Contains(reasons, tt.invalid) {
				t.Fatalf("invalid reasons %q, want %q", a.Verdict.InvalidReasons, tt.invalid)
			}
			if strings.Contains(reasons, "unequal trials") {
				t.Fatalf("a missing slot read as unequal trials: %q", reasons)
			}
			c := checkNamed(a.Verdict.Instruments, InstrumentSlotCoverage)
			if c == nil || c.Status != tt.coverage {
				t.Fatalf("slot coverage %+v, want %s", c, tt.coverage)
			}
			e := a.Verdict.Effect
			if pairedTasks(e) != tt.tasksPaired {
				t.Fatalf("%d tasks paired, want %d", pairedTasks(e), tt.tasksPaired)
			}
			for _, td := range e.LowerBound.PerTask {
				if td.Task == "t02" && td.UnknownB != tt.unknownB {
					t.Fatalf("t02 unknown B %d, want %d", td.UnknownB, tt.unknownB)
				}
			}
		})
	}
}

func TestDecideEvidenceProblemsInvalidate(t *testing.T) {
	clean := schemas.ConditionClean
	runs := concat(passes(clean, ArmBaseline, 3, 3, 3, 3, 3, 3, 3, 3, 3), passes(clean, ArmCandidate, 3, 3, 3, 3, 3, 3, 3, 3, 3))
	m := withSlots(testManifest(ArmBaseline, ArmCandidate), runs)
	tampered := append([]schemas.SealedRun(nil), runs...)
	tampered[4].Problems = []string{"seal does not verify: verify run " + tampered[4].Run.RunID + ": file metrics.json changed: seal verification failed"}
	tampered[4].Metrics = map[string]schemas.Value{}
	doubled := append([]schemas.SealedRun(nil), runs...)
	doubled[7].Problems = []string{"slot " + doubled[7].Slot.SlotID + " has 2 counted runs (r_a, r_b); exactly one may count"}
	tests := []struct {
		name string
		runs []schemas.SealedRun
		want []string
	}{
		{"clean runs", runs, nil},
		{"a run whose seal does not verify", tampered, []string{tampered[4].Run.RunID, "metrics.json changed"}},
		{"a slot with two counted runs", doubled, []string{doubled[7].Slot.SlotID, "has 2 counted runs"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := decideOrFail(t, m, tt.runs)
			if tt.want == nil {
				if a.Verdict.Decision == schemas.DecisionInvalid {
					t.Fatalf("invalid: %q", a.Verdict.InvalidReasons)
				}
				return
			}
			if a.Verdict.Decision != schemas.DecisionInvalid || a.Verdict.Action != schemas.ActionFixHarness {
				t.Fatalf("decision %s action %s, want invalid fix_harness", a.Verdict.Decision, a.Verdict.Action)
			}
			for _, w := range tt.want {
				if !strings.Contains(a.Verdict.InvalidReasons[0], w) {
					t.Fatalf("first reason %q does not name %q", a.Verdict.InvalidReasons[0], w)
				}
			}
		})
	}
}

func TestInvalidate(t *testing.T) {
	a := &schemas.Analysis{Verdict: schemas.Verdict{Decision: schemas.DecisionImproved, Action: schemas.ActionShip}}
	Invalidate(a)
	if a.Verdict.Decision != schemas.DecisionImproved {
		t.Fatal("no reasons changed the decision")
	}
	Invalidate(a, "seals.log line 3 malformed")
	if a.Verdict.Decision != schemas.DecisionInvalid || a.Verdict.Action != schemas.ActionFixHarness || len(a.Verdict.InvalidReasons) != 1 {
		t.Fatalf("verdict %+v", a.Verdict)
	}
	Invalidate(nil, "x")
}

func TestSlotCoverageWithoutSlots(t *testing.T) {
	c := slotCoverage(testManifest(ArmBaseline, ArmCandidate), pattern(schemas.ConditionClean, ArmBaseline, "1"))
	if c.Status != schemas.InstrumentNotApplicable {
		t.Fatalf("status %s, want not_applicable", c.Status)
	}
}
