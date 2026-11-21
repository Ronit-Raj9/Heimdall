package engine

// Run control (audit/RESEARCH_R1.md items 1, 2, 7 and 8): the scheduler side
// of the live controls in bench/runctl.
//
//   - Pause drains: the queue hands out no slot while the experiment is
//     paused, to local workers or remote ones, and in-flight runs finish.
//     Workers wait instead of returning, so a paused experiment stays
//     running and continues where it stopped on resume.
//   - Paused models hold only the slots whose arm runs one of them.
//   - A live concurrency limits the local workers from the next lease on.
//   - A live spend cap replaces the frozen one while it stays a cap.
//   - Cancel run stops one in-flight run with ErrRunCancelled (sealed
//     aborted_infra, never counted, no attempt used) and holds its slot out
//     until an operator requeues it.
//   - Requeue supersedes a sealed aborted_infra or infra_error run (it stays
//     on disk and never counts) and the slot runs again.
//   - The error policy may retry infra_error runs the same way.
//
// The scheduler polls the controls between leases (RunOptions.ControlPoll),
// so a change made by the API, the CLI or another process reaches it within
// one poll.

import (
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
)

// DefaultControlPoll is how often a running scheduler reads its controls.
const DefaultControlPoll = time.Second

// Run control event kinds for the live view.
const (
	EventPaused      = "paused"
	EventResumed     = "resumed"
	EventRunCancel   = "run_cancelled"
	EventSlotHeld    = "slot_held"
	EventRunRequeued = "run_requeued"
)

// ctlState is the scheduler's view of the controls, swapped whole.
type ctlState struct {
	st *runctl.State
}

// controls returns the last controls read (never nil).
func (r *schedRun) controls() *runctl.State {
	if v := r.ctl.Load(); v != nil && v.st != nil {
		return v.st
	}
	return &runctl.State{Settings: runctl.Settings{Mode: runctl.ModeRunning}}
}

// loadControls reads the experiment's controls; on an error the last good
// state is kept and the error logged once per poll.
func (r *schedRun) loadControls() *runctl.State {
	st, err := runctl.Read(r.s.reg.Store.Root(), r.m.ExpID)
	if err != nil {
		r.s.logger.Warn("run controls unreadable; keeping the last state", "experiment", r.m.ExpID, "error", err.Error())
		return r.controls()
	}
	r.ctl.Store(&ctlState{st: st})
	return st
}

// slotModel is the model the slot's arm runs ("" when unknown).
func (r *schedRun) slotModel(slot *schemas.Slot) string {
	if a, ok := r.m.Arms[slot.Arm]; ok {
		return a.Config.Model
	}
	if slot.Arm == CompatArm && r.m.Compatibility != nil {
		return r.m.Compatibility.Config.Model
	}
	return ""
}

// held reports a pending slot the controls keep back: the experiment is
// paused, or the slot's model is.
func (r *schedRun) held(slot *schemas.Slot) bool {
	st := r.controls()
	return st.Paused() || st.ModelPaused(r.slotModel(slot))
}

// applyGate pushes the controls to the queue: what is held and the local
// concurrency limit.
func (r *schedRun) applyGate(st *runctl.State) {
	limit := r.baseWorkers
	if st.Concurrency > 0 {
		limit = st.Concurrency
	}
	r.queue.setGate(r.held, limit)
}

// liveCap reads the live spend cap (0: none).
func (r *schedRun) liveCap() float64 {
	return math.Float64frombits(r.liveCapBits.Load())
}

// pollControls reads the controls every poll until ctx ends or stop closes,
// and applies each change: the queue gate, the live cap, cancels of
// in-flight runs and requeues of slots held out.
func (r *schedRun) pollControls(ctx context.Context, stop <-chan struct{}) {
	every := r.opts.ControlPoll
	if every <= 0 {
		every = DefaultControlPoll
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-stop:
			return
		case <-t.C:
		}
		r.syncControls()
	}
}

// syncControls applies the controls read now.
func (r *schedRun) syncControls() {
	prev := r.controls()
	st := r.loadControls()
	if st.Paused() != prev.Paused() {
		if st.Paused() {
			r.emit(Event{Kind: EventPaused, Message: fmt.Sprintf("paused by %s: in-flight runs finish, no new run starts", st.PausedBy)})
		} else {
			r.emit(Event{Kind: EventResumed, Message: "resumed: new runs start again"})
		}
	}
	if r.capFromStart && st.TotalUSD > 0 {
		r.liveCapBits.Store(math.Float64bits(st.TotalUSD))
	} else {
		r.liveCapBits.Store(0)
	}
	r.applyGate(st)
	r.cancelInFlight(st)
	r.releaseRequeued(st)
}

// trackCancel registers the cancel of an in-flight local slot; the returned
// func removes it.
func (r *schedRun) trackCancel(slotID string, cancel context.CancelCauseFunc) func() {
	r.cancelMu.Lock()
	if r.cancels == nil {
		r.cancels = map[string]context.CancelCauseFunc{}
	}
	r.cancels[slotID] = cancel
	r.cancelMu.Unlock()
	return func() {
		r.cancelMu.Lock()
		delete(r.cancels, slotID)
		r.cancelMu.Unlock()
	}
}

// cancelInFlight stops every in-flight slot an operator cancelled: a local
// run's context ends with ErrRunCancelled (sealed aborted_infra, owner
// stopped, no attempt used); a remote lease is revoked.
func (r *schedRun) cancelInFlight(st *runctl.State) {
	if len(st.CancelledSlots) == 0 {
		return
	}
	r.cancelMu.Lock()
	var stop []func()
	for slotID, cancel := range r.cancels {
		h, ok := st.CancelledSlots[slotID]
		if !ok {
			continue
		}
		cause := fmt.Errorf("%w: an operator (%s) cancelled this run: %s", ErrRunCancelled, h.By, reasonOrNone(h.Reason))
		c := cancel
		stop = append(stop, func() { c(cause) })
		delete(r.cancels, slotID)
		r.emit(Event{Kind: EventRunCancel, SlotID: slotID, RunID: h.RunID, Message: cause.Error()})
	}
	r.cancelMu.Unlock()
	for _, f := range stop {
		f()
	}
	if r.opts.Remote != nil {
		r.opts.Remote.revokeCancelled(r, st)
	}
}

func reasonOrNone(s string) string {
	if s == "" {
		return "no reason given"
	}
	return s
}

// holdOut takes a slot an operator cancelled out of this run: it counts as
// pending and waits for a requeue (releaseRequeued) or the next resume.
func (r *schedRun) holdOut(slot *schemas.Slot) {
	r.mu.Lock()
	if r.heldOut == nil {
		r.heldOut = map[string]*schemas.Slot{}
	}
	first := r.heldOut[slot.SlotID] == nil
	r.heldOut[slot.SlotID] = slot
	r.sum.Pending++
	r.mu.Unlock()
	r.markUnsettled(slot.SlotID)
	if first {
		r.emit(Event{Kind: EventSlotHeld, SlotID: slot.SlotID, Message: "cancelled by an operator: the run waits for a requeue"})
	}
}

// releaseRequeued queues again every slot held out whose cancel was lifted
// (requeue) while this run's workers are still taking slots.
func (r *schedRun) releaseRequeued(st *runctl.State) {
	r.mu.Lock()
	var again []*schemas.Slot
	for id, slot := range r.heldOut {
		if !st.SlotCancelled(id) {
			again = append(again, slot)
			delete(r.heldOut, id)
			r.sum.Pending--
		}
	}
	r.mu.Unlock()
	if len(again) == 0 {
		return
	}
	for _, s := range again {
		r.emit(Event{Kind: EventRunRequeued, SlotID: s.SlotID, Message: "requeued by an operator"})
	}
	r.queue.push(again)
}

// operatorCancelled reports a slot the controls hold out right now.
func (r *schedRun) operatorCancelled(slot *schemas.Slot) bool {
	return r.controls().SlotCancelled(slot.SlotID)
}

// maxAborted is the slot's attempt budget for aborted_infra runs under the
// frozen error policy.
func (r *schedRun) maxAborted() int {
	return r.m.Experiment.ErrorPolicy.AbortedInfraAttempts()
}

// usedAttempts counts the runs that used one of the slot's attempts: every
// run but those its owner stopped and those an operator or the error policy
// superseded.
func usedAttempts(recs []schemas.RunRecord) int {
	n := 0
	for _, rec := range recs {
		if rec.Superseded || (rec.Sealed && OwnerStopped(&rec.Run)) {
			continue
		}
		n++
	}
	return n
}

// attemptsRemain reports whether the slot may start another run.
func (r *schedRun) attemptsRemain(ctx context.Context, slot *schemas.Slot) (bool, error) {
	recs, err := slotRuns(ctx, r.s.reg.Store, r.m.ExpID, slot.SlotID)
	if err != nil {
		return false, err
	}
	return usedAttempts(recs) < r.maxAborted(), nil
}

// policyRetry applies the error policy to an infra_error run just sealed:
// while the slot has had fewer infra_error runs than the policy allows, the
// run is superseded (kept, never counted) and true says run the slot again.
func (r *schedRun) policyRetry(ctx context.Context, slot *schemas.Slot, run *schemas.Run) bool {
	limit := r.m.Experiment.ErrorPolicy.InfraErrorAttempts()
	if limit <= 1 || run == nil || run.Outcome != schemas.OutcomeInfraError {
		return false
	}
	recs, err := slotRuns(ctx, r.s.reg.Store, r.m.ExpID, slot.SlotID)
	if err != nil {
		return false
	}
	n := 0
	for _, rec := range recs {
		if rec.Sealed && rec.Run.Outcome == schemas.OutcomeInfraError {
			n++
		}
	}
	if n >= limit {
		return false
	}
	_, err = Supersede(r.s.reg.Store.Root(), r.m.ExpID, "error_policy", run.RunID, slot.SlotID, string(run.Outcome),
		fmt.Sprintf("error policy: infra_error is retried up to %d runs (run %d of %d)", limit, n, limit))
	if err != nil {
		r.s.logger.Warn("error policy retry not recorded; the run counts", "run", run.RunID, "error", err.Error())
		return false
	}
	r.emit(Event{Kind: EventRunRequeued, SlotID: slot.SlotID, RunID: run.RunID, Message: "infra_error: the error policy runs the slot again; this run is kept and not counted"})
	return true
}

// Requeueable reports whether a sealed run may be superseded: only
// aborted_infra and infra_error, never a graded run.
func Requeueable(o schemas.Outcome) bool {
	return o == schemas.OutcomeAbortedInfra || o == schemas.OutcomeInfraError
}

// ErrNotRequeueable is a requeue of a run whose outcome is graded or that is
// not the slot's newest.
var ErrNotRequeueable = fmt.Errorf("%w: run can not be requeued", runctl.ErrConflict)

// Supersede marks a sealed run superseded in the experiment's controls and
// lifts any cancel of its slot.
func Supersede(root, expID, actor, runID, slotID, outcome, reason string) (*runctl.State, error) {
	return runctl.Apply(root, expID, actor, runctl.ActionSupersede, runID, reason, func(s *runctl.State) error {
		if s.Superseded == nil {
			s.Superseded = map[string]runctl.Supersede{}
		}
		s.Superseded[runID] = runctl.Supersede{SlotID: slotID, Outcome: outcome, By: actor, Reason: reason, At: time.Now().UTC()}
		delete(s.CancelledSlots, slotID)
		return nil
	})
}

// CancelRun records an operator's cancel of the slot's in-flight or next
// run: a running scheduler stops it within one poll and holds the slot out.
func CancelRun(root, expID, actor, slotID, runID, reason string) (*runctl.State, error) {
	return runctl.Apply(root, expID, actor, runctl.ActionCancelRun, slotID, reason, func(s *runctl.State) error {
		if s.CancelledSlots == nil {
			s.CancelledSlots = map[string]runctl.SlotHold{}
		}
		if _, ok := s.CancelledSlots[slotID]; ok {
			return fmt.Errorf("%w: the run of slot %s is already cancelled", runctl.ErrConflict, slotID)
		}
		s.CancelledSlots[slotID] = runctl.SlotHold{RunID: runID, By: actor, Reason: reason, At: time.Now().UTC()}
		return nil
	})
}

// Requeue runs a slot again. recs are the slot's runs. A slot held out by a
// cancel is released; otherwise the newest sealed run must be aborted_infra
// or infra_error, and it is superseded: kept on disk, never counted, no
// attempt used. A slot with an unsealed (in-flight) run, or whose newest run
// is graded, is refused.
func Requeue(root, expID, actor, slotID, reason string, recs []schemas.RunRecord) (*runctl.State, error) {
	var newest *schemas.RunRecord
	for i := range recs {
		if recs[i].Superseded {
			continue
		}
		if !recs[i].Sealed {
			return nil, fmt.Errorf("%w: slot %s has a run in flight (%s); cancel it first", ErrNotRequeueable, slotID, recs[i].Run.RunID)
		}
		newest = &recs[i]
	}
	return runctl.Apply(root, expID, actor, runctl.ActionRequeue, slotID, reason, func(s *runctl.State) error {
		cancelled := s.SlotCancelled(slotID)
		delete(s.CancelledSlots, slotID)
		if newest == nil {
			if cancelled {
				return nil
			}
			return fmt.Errorf("%w: slot %s has no finished run to requeue", ErrNotRequeueable, slotID)
		}
		o := newest.Run.Outcome
		if !Requeueable(o) {
			if cancelled {
				return nil // the cancel came after a graded run: lifting it is all there is to do
			}
			return fmt.Errorf("%w: run %s is %s; only aborted_infra and infra_error runs are requeued, a graded run is never replaced", ErrNotRequeueable, newest.Run.RunID, o)
		}
		if o == schemas.OutcomeAbortedInfra && OwnerStopped(&newest.Run) {
			// Already uses no attempt: the slot runs again as it is.
			return nil
		}
		if s.Superseded == nil {
			s.Superseded = map[string]runctl.Supersede{}
		}
		s.Superseded[newest.Run.RunID] = runctl.Supersede{SlotID: slotID, Outcome: string(o), By: actor, Reason: reason, At: time.Now().UTC()}
		return nil
	})
}

// ExperimentModels lists the distinct models the manifest's arms run, sorted.
func ExperimentModels(m *schemas.Manifest) []string {
	var out []string
	for _, a := range m.Arms {
		if a.Config.Model != "" && !slices.Contains(out, a.Config.Model) {
			out = append(out, a.Config.Model)
		}
	}
	slices.Sort(out)
	return out
}
