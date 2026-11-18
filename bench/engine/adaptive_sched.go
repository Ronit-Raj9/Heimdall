package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/verdict"
)

// W7 adaptive trials: the scheduler's part (docs/adaptive-trials.md). The
// queue starts with the slots that always run. Each time it drains, the
// frozen rule is replayed over the runs so far and the optional slots it now
// releases are queued, until it releases none. A slot never released is
// "not required", not missing. The verdict replays the same rule itself, so
// nothing here can change what is analysed; a wrong release only costs runs.

// EventAdaptiveRelease reports optional slots the rule released.
const EventAdaptiveRelease = "adaptive_release"

// adaptiveState is one Run call's view of the adaptive design.
type adaptiveState struct {
	queued map[string]bool // slots handed to the queue in this Run call
	last   *schemas.AdaptiveReport
}

// initialSlots are the slots the queue starts with: every slot of a fixed
// design, and only the non-optional ones of an adaptive design.
func initialSlots(m *schemas.Manifest) []schemas.Slot {
	if m.Adaptive == nil {
		return m.Slots
	}
	out := make([]schemas.Slot, 0, len(m.Slots))
	for _, sl := range m.Slots {
		if !sl.Optional {
			out = append(out, sl)
		}
	}
	return out
}

// releaseOptional replays the rule after the queue drained and queues the
// optional slots it newly releases. It reports whether it queued any. A
// fixed design, a cancelled run and a stopped spend cap release nothing.
func (r *schedRun) releaseOptional(ctx context.Context) bool {
	if r.m.Adaptive == nil || ctx.Err() != nil {
		return false
	}
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return false
	}
	if r.w7 == nil {
		r.w7 = &adaptiveState{queued: map[string]bool{}}
		for _, sl := range initialSlots(r.m) {
			r.w7.queued[sl.SlotID] = true
		}
	}
	runs, err := r.adaptiveRuns(ctx)
	if err != nil {
		r.s.logger.Error("adaptive trials: runs could not be read; no optional slot released", "experiment", r.m.ExpID, "error", err.Error())
		r.emit(Event{Kind: EventError, Message: "adaptive trials: " + err.Error()})
		return false
	}
	done := map[string]bool{}
	for _, run := range runs {
		done[run.Slot.SlotID] = true
	}
	rep := verdict.ReplayAdaptiveSettled(r.m, runs, func(id string) bool { return r.w7.queued[id] || done[id] })
	r.w7.last = rep
	var fresh []*schemas.Slot
	for i := range r.m.Slots {
		sl := &r.m.Slots[i]
		if sl.Optional && !r.w7.queued[sl.SlotID] && rep.SlotStates[sl.SlotID] == schemas.SlotReleased {
			r.w7.queued[sl.SlotID] = true
			cp := *sl
			fresh = append(fresh, &cp)
		}
	}
	if len(fresh) == 0 {
		return false
	}
	r.queue.push(fresh)
	r.emit(Event{Kind: EventAdaptiveRelease, Message: fmt.Sprintf("%s rule released %d optional slot(s); %s", rep.Rule, len(fresh), stopText(rep))})
	return true
}

// adaptiveSummary fills the summary's adaptive counts from the last replay.
func (r *schedRun) adaptiveSummary(sum *Summary) {
	if r.w7 == nil || r.w7.last == nil {
		return
	}
	sum.NotRequired = r.w7.last.RunsSaved
	sum.StopReason = r.w7.last.StopReason
}

func stopText(rep *schemas.AdaptiveReport) string {
	if rep.Rule == schemas.AdaptiveSequential {
		return fmt.Sprintf("look %d of %d: %s", rep.StopLook, rep.Looks, rep.StopReason)
	}
	return fmt.Sprintf("%d of %d planned slots required", rep.RequiredSlots, rep.MaxSlots)
}

// adaptiveRuns reads every slot's counted run with its metrics, from the run
// folders. The seals are not checked here: the rule only decides what to run
// next, and the verdict replays it over the verified runs.
func (r *schedRun) adaptiveRuns(ctx context.Context) ([]schemas.SealedRun, error) {
	var out []schemas.SealedRun
	for _, sl := range r.m.Slots {
		if !sl.Counted {
			continue
		}
		recs, err := slotRuns(ctx, r.s.reg.Store, r.m.ExpID, sl.SlotID)
		if err != nil {
			return nil, err
		}
		for _, rec := range recs {
			if !rec.Sealed || rec.Run.Outcome == schemas.OutcomeAbortedInfra {
				continue
			}
			metrics := map[string]schemas.Value{}
			if b, err := os.ReadFile(filepath.Join(rec.Dir, "metrics.json")); err == nil {
				if err := json.Unmarshal(b, &metrics); err != nil {
					return nil, fmt.Errorf("parse metrics of run %s: %w", rec.Run.RunID, err)
				}
			}
			out = append(out, schemas.SealedRun{Slot: sl, Run: rec.Run, Metrics: metrics})
			break
		}
	}
	return out, nil
}

// push queues slots in OrderIndex order among the pending ones.
func (q *slotQueue) push(slots []*schemas.Slot) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = append(q.pending, slots...)
	sort.SliceStable(q.pending, func(i, j int) bool { return q.pending[i].OrderIndex < q.pending[j].OrderIndex })
	q.notifyLocked()
}
