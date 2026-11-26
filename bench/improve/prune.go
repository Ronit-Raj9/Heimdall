package improve

import (
	"errors"
	"maps"
	"slices"
	"strings"
)

// Pruning of A3.3, wired into the runner: the playbook's counters are
// updated after each screen, and the next round's prune edits are added to
// slot 1's edit set. Prune edits are never applied alone.

// trackOpened updates rec.SkillsOpened from this round's evidence: a skill
// of the incumbent that a practice run of the incumbent opened gets this
// round, a skill seen for the first time gets this round too (it has not
// had a chance to be opened), and skills no longer in the incumbent are
// forgotten. Nothing is judged without evidence of the incumbent's runs.
func trackOpened(rec *Record, rd *Round, doc *EvidenceDoc) {
	if doc == nil {
		return
	}
	ran := false
	opened := map[string]bool{}
	for _, it := range doc.Items {
		if it.Version != rd.Parent.Number {
			continue
		}
		ran = true
		for _, p := range it.Opened {
			opened[p] = true
		}
	}
	if !ran {
		return
	}
	if rec.SkillsOpened == nil {
		rec.SkillsOpened = map[string]int{}
	}
	for p := range rec.SkillsOpened {
		if _, ok := rd.Parent.Bundle[p]; !ok {
			delete(rec.SkillsOpened, p)
		}
	}
	for p := range rd.Parent.Bundle {
		if !strings.HasSuffix(p, "/SKILL.md") {
			continue
		}
		if _, seen := rec.SkillsOpened[p]; opened[p] || !seen {
			rec.SkillsOpened[p] = rd.N
		}
	}
}

// prune adds the round's prune edits (PruneHints on the playbook the
// improver's edits produced) to slot 1's applied edits. A hint on an item or
// skill the improver's own edits touched is left out. The hints carry no
// run refs, so they are applied in a pass of their own; when none applies,
// the improver's result stands as it is.
func (r *Runner) prune(rec *Record, rd *Round, nb BundleObject, npb PlaybookObject, applied []AppliedEdit) (BundleObject, PlaybookObject, []AppliedEdit) {
	if DefaultEditApplier == nil {
		return nb, npb, applied
	}
	opened := map[string]int{}
	for p, n := range rec.SkillsOpened {
		if _, ok := nb.Files[p]; ok {
			opened[p] = n
		}
	}
	touched := map[string]bool{}
	for _, a := range applied {
		if a.Result == "refused" {
			continue
		}
		touched[a.ID], touched[a.Path] = true, true
		if id, ok := strings.CutPrefix(a.Result, "merged_into:"); ok {
			touched[id] = true
		}
	}
	delete(touched, "")
	hints := slices.DeleteFunc(PruneHints(npb, opened, rd.N), func(e Edit) bool { return touched[e.ID] || touched[e.Path] })
	if len(hints) == 0 {
		return nb, npb, applied
	}
	pb2, ppb, pa, err := DefaultEditApplier(BundleObject{Files: maps.Clone(nb.Files), Raw: slices.Clone(nb.Raw)}, npb, EditsFile{Edits: hints},
		rec.Spec.Editable, DefaultLimits, KindNull, rd.N)
	if err != nil {
		if !errors.Is(err, ErrNothingApplied) && r.Logger != nil {
			r.Logger.Warn("improvement: prune edits not applied", "improvement", rec.ID, "round", rd.N, "error", err.Error())
		}
		return nb, npb, applied
	}
	return pb2, ppb, append(applied, pa...)
}

// updateCounters adds the screen's flips to the playbook counters of each
// screened idea that carries edits (A3.3): a practice task the idea passed
// and the incumbent failed is fail_to_pass, the other way pass_to_fail. The
// idea's version then points at the updated playbook, so a kept version
// carries its counters into the next round's prune hints.
func (r *Runner) updateCounters(ids []string, byID map[string]*CandidateRun, dev map[string]map[string]float64, inc map[string]float64) error {
	if r.Objects.Root == "" {
		return nil
	}
	for _, id := range ids {
		c := byID[id]
		if c == nil || c.Version == nil || c.Version.PlaybookObj == "" || len(c.Edits) == 0 {
			continue
		}
		flips := map[string]string{}
		for t, after := range dev[id] {
			before, ok := inc[t]
			switch {
			case !ok:
			case before < 0.5 && after >= 0.5:
				flips[t] = "fail_to_pass"
			case before >= 0.5 && after < 0.5:
				flips[t] = "pass_to_fail"
			}
		}
		if len(flips) == 0 {
			continue
		}
		var pb PlaybookObject
		if err := r.Objects.Get(c.Version.PlaybookObj, ObjPlaybook, &pb); err != nil {
			return err
		}
		h, err := r.Objects.Put(ObjPlaybook, UpdateCounters(pb, c.Edits, flips))
		if err != nil {
			return err
		}
		c.Version.PlaybookObj = h
	}
	return nil
}
