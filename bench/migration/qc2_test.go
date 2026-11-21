package migration

import (
	"slices"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// slotted gives the fixture's manifest one counted slot per run, so the
// pair coverage check sees the frozen plan.
func slotted(in Input) Input {
	m := *in.Manifest
	m.Slots = nil
	for _, r := range in.Runs {
		m.Slots = append(m.Slots, r.Slot)
	}
	in.Manifest = &m
	return in
}

func setOutcome(r *schemas.SealedRun, pass bool) {
	r.Run.Outcome = schemas.OutcomeFail
	r.Metrics["verified_success"] = schemas.Observed(0, schemas.EvidenceOutcome)
	if pass {
		r.Run.Outcome = schemas.OutcomePass
		r.Metrics["verified_success"] = schemas.Observed(1, schemas.EvidenceOutcome)
	}
}

// Audit qc2-05-01: an A-to-C pair is checked on its own coverage. C passing
// on eight tasks with two required slots missing must not recommend migrate.
func TestPairCoverageWithholdsUnderCoveredCandidate(t *testing.T) {
	in := fixture()
	for i := range in.Runs {
		r := &in.Runs[i]
		if r.Slot.Condition != schemas.ConditionClean {
			continue
		}
		switch r.Slot.Arm {
		case "A":
			setOutcome(r, false)
		case "C":
			setOutcome(r, true)
		}
	}
	in = slotted(in)
	q := Query{From: "A", To: []string{"C"}}
	if a := pairTo(t, build(t, in, q), "C").Recommendation.Action; a != ActionMigrate {
		t.Fatalf("fully covered A to C = %s, want migrate", a)
	}
	in.Runs = slices.DeleteFunc(slices.Clone(in.Runs), func(r schemas.SealedRun) bool {
		return r.Slot.Arm == "C" && r.Slot.Condition == schemas.ConditionClean && (r.Slot.Task == "t09" || r.Slot.Task == "t10")
	})
	rec := pairTo(t, build(t, in, q), "C").Recommendation
	if rec.Action != ActionNotEnough || !strings.Contains(rec.Reason, "graded coverage") || !strings.Contains(rec.Reason, "arm C") {
		t.Fatalf("under-covered A to C recommendation %+v", rec)
	}
}

// Audit qc2-05-03: every section reads the public split, so held-out tasks
// enter neither the pair verdict nor the flipped list.
func TestMigrationReadsPublicSplitOnly(t *testing.T) {
	in := fixture()
	m := *in.Manifest
	m.HeldOutTasks = []string{"t04", "t05", "t06", "t07", "t08", "t09", "t10"}
	in.Manifest = &m
	rep := build(t, in, Query{From: "old-model"})
	if !slices.Contains(rep.Notes, HeldOutNote) {
		t.Fatalf("notes %v lack the held-out note", rep.Notes)
	}
	p := pairTo(t, rep, "B")
	if p.Overall.Tasks != 3 {
		t.Fatalf("overall tasks %d, want the 3 public tasks", p.Overall.Tasks)
	}
	for _, f := range p.Flipped {
		if slices.Contains(m.HeldOutTasks, f.Task) {
			t.Fatalf("flipped lists held-out task %s", f.Task)
		}
	}
}

// Audit qc2-05-04: an INVALID release verdict ranks no model in the
// embedded matrix either.
func TestInvalidReleaseVerdictRanksNoModel(t *testing.T) {
	in := fixture()
	in.Invalid = []string{"gate model_fidelity fired"}
	rep := build(t, in, Query{From: "old-model"})
	for _, s := range rep.Models {
		if s.Success.IsObserved() {
			t.Fatalf("model %s success %+v shown under an INVALID verdict", s.Arm, s.Success)
		}
	}
	if rep.Pareto != nil {
		for _, pt := range rep.Pareto.Points {
			if pt.Frontier {
				t.Fatalf("pareto point %s on the frontier under an INVALID verdict", pt.ID)
			}
		}
	}
}

// Audit qc2-05-05: two baseline arms of one model compared with one new arm
// keep their own cost explanation.
func TestMultipleBaselinesKeepTheirOwnCost(t *testing.T) {
	in := fixture()
	m := *in.Manifest
	m.Arms = map[string]schemas.ArmManifest{}
	for id, a := range in.Manifest.Arms {
		m.Arms[id] = a
	}
	b := m.Arms["B"]
	b.Config.Model = "old-model"
	m.Arms["B"] = b
	in.Manifest = &m
	rep := build(t, in, Query{From: "old-model", To: []string{"C"}})
	if len(rep.Pairs) != 2 {
		t.Fatalf("pairs %d, want A to C and B to C", len(rep.Pairs))
	}
	for _, p := range rep.Pairs {
		if p.Cost == nil || p.Cost.A.Arm != p.From.Arm || p.Cost.B.Arm != p.To.Arm {
			t.Fatalf("pair %s to %s carries the cost of another pair: %+v", p.From.Arm, p.To.Arm, p.Cost)
		}
	}
}

// Audit qc2-05-08: a release-verdict veto (uncalibrated judge cap)
// withholds a migration the statistics alone would give.
func TestReleaseVetoWithholdsRecommendation(t *testing.T) {
	in := fixture()
	in.Vetoes = []string{"judge calibration caps the release verdict: judge not calibrated"}
	rec := pairTo(t, build(t, in, Query{From: "old-model"}), "B").Recommendation
	if rec.Action != ActionNotEnough || !strings.Contains(rec.Reason, "judge calibration") {
		t.Fatalf("vetoed recommendation %+v", rec)
	}
}
