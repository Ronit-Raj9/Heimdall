package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// ErrRunCancelled is the cause of a scheduler context its owner cancelled on
// purpose: the experiment was cancelled, or the server running it is
// stopping and will resume it. A run stopped this way is sealed as
// aborted_infra whether or not its agent started, as on a lost lease
// (pipeline.go): it never counts, and a resume runs its slot again.
// (Jobs area, bench/jobs, docs/platform-design.md)
var ErrRunCancelled = errors.New("experiment run cancelled")

// CancelContext returns the context to run a scheduler under on behalf of
// parent (a job's context): it ends when parent ends, with ErrRunCancelled
// wrapping parent's cause, so the scheduler leases nothing more and its
// in-flight runs seal as aborted_infra. stop releases it.
func CancelContext(parent context.Context) (ctx context.Context, stop func()) {
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	unwatch := context.AfterFunc(parent, func() {
		cancel(fmt.Errorf("%w: %w", ErrRunCancelled, context.Cause(parent)))
	})
	return ctx, func() {
		unwatch()
		cancel(nil)
	}
}

// OwnerStopped reports a run sealed as aborted_infra because its owner
// stopped it on purpose (ErrRunCancelled): such a run never uses one of the
// slot's attempts, so cancelling and resuming, or restarting the server, can
// not exhaust a slot. A run interrupted by a crash or a lost lease still
// counts.
func OwnerStopped(run *schemas.Run) bool {
	if run == nil || run.Outcome != schemas.OutcomeAbortedInfra {
		return false
	}
	for _, f := range run.Failures {
		if f.Stage == schemas.StageLeased && f.Part == "engine" && strings.HasPrefix(f.Error, ErrRunCancelled.Error()) {
			return true
		}
	}
	return false
}

// countOwnerStopped counts the sealed owner-stopped runs of a slot.
func countOwnerStopped(recs []schemas.RunRecord) int {
	n := 0
	for _, rec := range recs {
		if rec.Sealed && OwnerStopped(&rec.Run) {
			n++
		}
	}
	return n
}

// stoppedRuns counts the slot's sealed owner-stopped runs; 0 when they can
// not be read (the attempt budget then stays the plain maximum).
func (r *schedRun) stoppedRuns(ctx context.Context, slot *schemas.Slot) int {
	recs, err := slotRuns(ctx, r.s.reg.Store, r.m.ExpID, slot.SlotID)
	if err != nil {
		return 0
	}
	return countOwnerStopped(recs)
}
