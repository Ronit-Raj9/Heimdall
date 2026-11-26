package improve

import (
	"math/rand/v2"
	"slices"
)

func init() { DefaultProposer = keepSetProposer{} }

// Proposal kinds (ProposalSpec.Kind, CandidateRun.Kind).
const (
	KindMutation = "mutation"
	KindMerge    = "merge"
	KindRestart  = "restart"
	KindNull     = "null"
)

// keepSetProposer is the default proposer (A3.4): slot 1 mutates the
// incumbent; from round 2 one merge slot when two keep set members led
// disjoint practice tasks; one restart slot from version 0 in each restart
// round; the other slots mutate a keep set member drawn by the tasks it led.
type keepSetProposer struct{}

// proposalSeed is the improver seed of one slot: spec seed x 1000 + round x
// 16 + slot, so a slot's idea is fixed by the spec.
func proposalSeed(spec *Spec, round, slot int) int64 {
	return spec.Seed*1000 + int64(round*16+slot)
}

// Plan returns the round's slots, slot 1 first.
func (keepSetProposer) Plan(rec *Record, round int) []ProposalSpec {
	k := max(1, rec.Spec.Improver.Candidates)
	out := []ProposalSpec{{Slot: 1, Kind: KindMutation, Source: rec.Incumbent, Seed: proposalSeed(&rec.Spec, round, 1)}}
	members := keepMembers(rec)
	if len(out) < k && rec.Spec.Improver.Merge && round >= 2 {
		if a, b, ok := mergePair(members); ok {
			second := b.version
			out = append(out, ProposalSpec{Slot: len(out) + 1, Kind: KindMerge, Source: a.version, Second: &second, Seed: proposalSeed(&rec.Spec, round, len(out)+1)})
		}
	}
	if len(out) < k && slices.Contains(rec.Spec.Improver.RestartRounds, round) {
		out = append(out, ProposalSpec{Slot: len(out) + 1, Kind: KindRestart, Source: rec.Base, Seed: proposalSeed(&rec.Spec, round, len(out)+1)})
	}
	for len(out) < k {
		slot := len(out) + 1
		src := rec.Incumbent
		if len(members) > 0 {
			rng := rand.New(rand.NewPCG(uint64(rec.Spec.Seed), uint64(round*16+slot)))
			total := 0
			for _, m := range members {
				total += len(m.entry.Led) + 1
			}
			pick := rng.IntN(total)
			for _, m := range members {
				pick -= len(m.entry.Led) + 1
				if pick < 0 {
					src = m.version
					break
				}
			}
		}
		out = append(out, ProposalSpec{Slot: slot, Kind: KindMutation, Source: src, Seed: proposalSeed(&rec.Spec, round, slot)})
	}
	return out
}

type keepMember struct {
	entry   KeepEntry
	version Version
}

// keepMembers resolves the keep set's refs to versions; a ref that cannot be
// found is left out.
func keepMembers(rec *Record) []keepMember {
	out := []keepMember{}
	for _, e := range rec.Keep {
		if v, ok := findVersion(rec, e.Ref); ok {
			out = append(out, keepMember{entry: e, version: v})
		}
	}
	return out
}

// findVersion is the version with ref among version 0, the incumbent and the
// rounds' ideas.
func findVersion(rec *Record, ref string) (Version, bool) {
	if rec.Incumbent.Ref == ref {
		return rec.Incumbent, true
	}
	if rec.Base.Ref == ref {
		return rec.Base, true
	}
	for _, rd := range rec.Rounds {
		for _, c := range rd.Candidates {
			if c.Version != nil && c.Version.Ref == ref {
				return *c.Version, true
			}
		}
	}
	return Version{}, false
}

// mergePair is the first pair of keep set members whose led practice tasks
// are non-empty and disjoint; the member that led more tasks comes first.
func mergePair(ms []keepMember) (keepMember, keepMember, bool) {
	for i := range ms {
		for j := i + 1; j < len(ms); j++ {
			a, b := ms[i], ms[j]
			if len(a.entry.Led) == 0 || len(b.entry.Led) == 0 || a.version.ConfigHash == b.version.ConfigHash {
				continue
			}
			disjoint := true
			for _, t := range a.entry.Led {
				if slices.Contains(b.entry.Led, t) {
					disjoint = false
					break
				}
			}
			if !disjoint {
				continue
			}
			if len(b.entry.Led) > len(a.entry.Led) {
				a, b = b, a
			}
			return a, b, true
		}
	}
	return keepMember{}, keepMember{}, false
}

// updateKeep recomputes the keep set after a screen: the incumbent plus the
// best keep - 1 ideas by practice difference that were not dropped as worse
// and did not trip a check.
func updateKeep(rec *Record, rd *Round, dev map[string]map[string]float64, incumbent map[string]float64) {
	keep := []KeepEntry{{Ref: rec.Incumbent.Ref, Obj: rec.Incumbent.BundleObj}}
	cands := []*CandidateRun{}
	for i := range rd.Candidates {
		c := &rd.Candidates[i]
		if c.Version == nil || len(c.Tripwires) > 0 || c.Dropped == droppedWorse || c.Version.Ref == rec.Incumbent.Ref {
			continue
		}
		if _, ok := dev[c.ID]; !ok {
			continue
		}
		cands = append(cands, c)
	}
	slices.SortStableFunc(cands, func(a, b *CandidateRun) int {
		switch {
		case a.DevDiff > b.DevDiff:
			return -1
		case a.DevDiff < b.DevDiff:
			return 1
		}
		return a.Slot - b.Slot
	})
	for _, c := range cands {
		if len(keep) >= max(1, rec.Spec.Improver.Keep) {
			break
		}
		led := []string{}
		for t, v := range dev[c.ID] {
			if iv, ok := incumbent[t]; ok && v > iv {
				led = append(led, t)
			}
		}
		slices.Sort(led)
		keep = append(keep, KeepEntry{Ref: c.Version.Ref, Obj: c.Obj, Led: led, DevDiff: c.DevDiff})
	}
	rec.Keep = keep
}

// droppedWorse is the screen's reason for an idea that is clearly worse.
const droppedWorse = "worse than the current version on the practice tasks"
