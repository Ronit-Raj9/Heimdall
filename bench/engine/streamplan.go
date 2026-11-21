package engine

import (
	"cmp"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// streamOrderStream is the PCG word of a seeded stream order. It differs
// from the other planner streams so a stream never moves the arm order.
const streamOrderStream = 0x73747265 // "stre"

// checkStream refuses a stream the engine cannot run: an invalid block, an
// adaptive trials design (optional slots would leave holes in a chain), or
// a team task (a team has no single memory volume to carry).
func checkStream(e *schemas.Experiment, suite *schemas.Suite, conds []condPlan) error {
	if e.Stream == nil {
		return nil
	}
	if err := e.Stream.Validate(e.Arms); err != nil {
		return err
	}
	if e.Trials != nil && e.Trials.Mode == schemas.TrialsAdaptive {
		return errors.New("stream cannot be combined with adaptive trials: an optional run would leave a hole in a chain")
	}
	for _, c := range conds {
		for _, id := range c.tasks {
			if t := suite.TaskByID(id); t != nil && len(t.Team) > 0 {
				return fmt.Errorf("stream cannot run task %s: a team task has no single memory to carry", id)
			}
		}
	}
	return nil
}

// streamKey names the chain of a slot: one per arm, condition and trial.
func streamKey(s *schemas.Slot) string {
	return s.Arm + "\x00" + s.Condition + "\x00" + strconv.Itoa(s.Trial)
}

// planStream turns the slots of a stream experiment into chains. Every
// counted slot of one arm, condition and trial gets its 1-based place in
// the chain (StreamIndex) and the slot that runs before it (StreamPrev):
// the condition's task order, or one seeded shuffle of it shared by every
// chain of the condition, so the arms see the tasks in the same order.
// With replay k, k more slots per chain run the chain's first k tasks again
// at the end (StreamReplay, not counted, so the verdict never sees them).
// Compatibility slots stay outside every chain. Without a stream the slots
// are returned unchanged.
func planStream(m *schemas.Manifest, conds []condPlan, slots []schemas.Slot) []schemas.Slot {
	st := m.Experiment.Stream
	if st == nil {
		return slots
	}
	rank := map[string]map[string]int{} // condition -> task -> place
	for _, c := range conds {
		order := slices.Clone(c.tasks)
		if st.Order == schemas.StreamOrderSeeded {
			r := rand.New(rand.NewPCG(uint64(m.Experiment.Seed), streamOrderStream))
			r.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })
		}
		rank[c.id] = map[string]int{}
		for i, t := range order {
			rank[c.id][t] = i
		}
	}
	chains := map[string][]int{} // chain -> slot indexes
	var keys []string
	for i := range slots {
		s := &slots[i]
		if !s.Counted || s.Arm == CompatArm {
			continue
		}
		k := streamKey(s)
		if _, ok := chains[k]; !ok {
			keys = append(keys, k)
		}
		chains[k] = append(chains[k], i)
	}
	var replays []schemas.Slot
	for _, k := range keys {
		idx := chains[k]
		slices.SortStableFunc(idx, func(a, b int) int {
			sa, sb := &slots[a], &slots[b]
			return cmp.Compare(rank[sa.Condition][sa.Task], rank[sb.Condition][sb.Task])
		})
		prev := ""
		for n, i := range idx {
			slots[i].StreamIndex, slots[i].StreamPrev = n+1, prev
			prev = slots[i].SlotID
		}
		for n := 0; n < st.Replay && n < len(idx); n++ {
			src := slots[idx[n]]
			rep := src
			rep.SlotID = utils.NewID("s", 10)
			rep.PairID = utils.NewID("p", 10)
			rep.Counted = false
			rep.StreamReplay = true
			rep.StreamIndex = len(idx) + n + 1
			rep.StreamPrev = prev
			rep.RunInputHash = ""
			prev = rep.SlotID
			replays = append(replays, rep)
		}
	}
	for _, r := range replays {
		r.OrderIndex = len(slots)
		slots = append(slots, r)
	}
	return slots
}
