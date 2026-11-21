package verdict

import (
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// InstrumentSlotCoverage is the instrument check that every counted slot the
// manifest assigned has a sealed run.
const InstrumentSlotCoverage = "slot_coverage"

// maxListedSlots caps the slots a slot_coverage detail names.
const maxListedSlots = 10

// CompleteSlots returns runs plus one placeholder for every counted slot of
// the manifest that has no run among them (DESIGN.md 9.3 item 1: no slot is
// dropped). A placeholder carries the slot, outcome unknown and no metrics, so
// every metric reads as unknown: it counts in the per-task cells (inside the
// missingness bounds), lowers graded coverage, keeps trials per arm equal and
// shows in the slot_coverage check. Runs are never changed or reordered and a
// manifest without slots adds nothing. Calling it twice adds nothing more.
// The verdict calls it itself; the release policy is handed the same
// completed list so a policy rule and the verdict see the same slots.
func CompleteSlots(m *schemas.Manifest, runs []schemas.SealedRun) []schemas.SealedRun {
	if m == nil || len(m.Slots) == 0 {
		return runs
	}
	have := make(map[string]bool, len(runs))
	for _, r := range runs {
		// An uncounted attempt (a superseded retry handed over for the
		// harness checks) never fills its slot.
		if r.Slot.Counted {
			have[r.Slot.SlotID] = true
		}
	}
	var missing []schemas.SealedRun
	notRequired := NotRequiredSlots(m, runs) // W7 adaptive trials: optional slots the frozen rule never released (sequential.go)
	for _, sl := range m.Slots {
		if !sl.Counted || have[sl.SlotID] || notRequired[sl.SlotID] {
			continue
		}
		have[sl.SlotID] = true
		if sl.ExpID == "" {
			sl.ExpID = m.ExpID
		}
		missing = append(missing, schemas.SealedRun{
			Slot:    sl,
			Run:     schemas.Run{SlotID: sl.SlotID, PairID: sl.PairID, ExpID: sl.ExpID, Outcome: schemas.OutcomeUnknown, Y: schemas.Unknown("no sealed run", schemas.EvidenceOutcome)},
			Metrics: map[string]schemas.Value{},
		})
	}
	if len(missing) == 0 {
		return runs
	}
	out := make([]schemas.SealedRun, 0, len(runs)+len(missing))
	return append(append(out, runs...), missing...)
}

// DecisionRuns is the population the verdict analyses: runs without the
// counted runs the frozen adaptive rule did not release (AdaptiveRuns), then
// every missing required slot added as an unknown (CompleteSlots). The
// release policy is handed exactly this list, so a policy rule never reads a
// run the verdict left out (audit qc2-02-06). Uncounted attempts in runs are
// kept for callers that filter on Slot.Counted.
func DecisionRuns(m *schemas.Manifest, runs []schemas.SealedRun) []schemas.SealedRun {
	return CompleteSlots(m, AdaptiveRuns(m, runs))
}

// Invalidate makes an analysis INVALID for reasons found outside the policy,
// such as a seals.log chain that does not verify, with the action the
// release table gives INVALID. It is a no-op without reasons.
func Invalidate(a *schemas.Analysis, reasons ...string) {
	if a == nil || len(reasons) == 0 {
		return
	}
	a.Verdict.InvalidReasons = append(a.Verdict.InvalidReasons, reasons...)
	a.Verdict.Decision = schemas.DecisionInvalid
	a.Verdict.Action = schemas.ActionFixHarness
}

// evidenceReasons returns one invalid reason per evidence problem the store
// attached to a counted run: its seal does not verify, or its slot has more
// than one counted run. Either means the run folder is not the evidence that
// was sealed, so no verdict can rest on it.
func evidenceReasons(runs []schemas.SealedRun) []string {
	var out []string
	for _, r := range runs {
		if !r.Slot.Counted {
			continue
		}
		for _, p := range r.Problems {
			out = append(out, fmt.Sprintf("evidence: run %s: %s", runLabel(r), p))
		}
	}
	return out
}

// slotCoverage counts, per arm, the counted manifest slots that have a sealed
// run. A missing slot is not a gate of its own: it enters the effect as an
// unknown, so graded coverage and the missingness bounds already weigh it,
// and the check only warns and names the slots. Not applicable when the
// manifest lists no slots.
func slotCoverage(m *schemas.Manifest, runs []schemas.SealedRun) schemas.InstrumentCheck {
	c := schemas.InstrumentCheck{
		Name:      InstrumentSlotCoverage,
		Threshold: "every counted slot has exactly one counted sealed run; a missing one counts as unknown",
		PerArm:    map[string]schemas.Ratio{},
	}
	if m == nil || len(m.Slots) == 0 {
		c.Status = schemas.InstrumentNotApplicable
		c.Detail = "the manifest lists no slots"
		return c
	}
	var missing []string
	total := 0
	for _, r := range runs {
		if !r.Slot.Counted {
			continue
		}
		total++
		ratio := c.PerArm[r.Slot.Arm]
		ratio.Den++
		if r.Run.RunID == "" {
			ratio.Unknown++
			missing = append(missing, runLabel(r))
		} else {
			ratio.Num++
		}
		c.PerArm[r.Slot.Arm] = ratio
	}
	if len(missing) == 0 {
		c.Status = schemas.InstrumentPass
		c.Detail = fmt.Sprintf("all %d counted slots have a sealed run", total)
		return c
	}
	listed := missing
	if len(listed) > maxListedSlots {
		listed = append(append([]string(nil), listed[:maxListedSlots]...), fmt.Sprintf("and %d more", len(missing)-maxListedSlots))
	}
	c.Status = schemas.InstrumentWarn
	c.Detail = fmt.Sprintf("%d of %d counted slots have no sealed run and count as unknown: %s",
		len(missing), total, strings.Join(listed, "; "))
	return c
}
