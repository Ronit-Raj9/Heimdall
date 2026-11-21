package engine

// Debug runs (lane X20b): a replay, fork or breakpoint run of one recorded
// run (schemas.DebugSpec). The planner marks every slot uncounted; the
// pipeline loads the frozen cassette onto the run's key slot, binds the
// replay and hold plan, and suspends the agent's wall clock while a
// breakpoint holds a request.

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// heldPoll is how often the pipeline reads a debug run's held time.
var heldPoll = time.Second

// debugSpec is the debug spec of rc's experiment, or nil.
func debugSpec(rc *schemas.RunContext) *schemas.DebugSpec {
	if rc == nil {
		return nil
	}
	if rc.Manifest != nil && rc.Manifest.Experiment.Debug != nil {
		return rc.Manifest.Experiment.Debug
	}
	if rc.Experiment != nil {
		return rc.Experiment.Debug
	}
	return nil
}

// runDebugOf is the run.json link of a debug experiment's run.
func runDebugOf(m *schemas.Manifest) *schemas.RunDebug {
	if m == nil || m.Experiment.Debug == nil {
		return nil
	}
	d := m.Experiment.Debug
	return &schemas.RunDebug{Mode: d.Mode, FromRun: d.FromRun, FromExp: d.FromExp, Call: d.Call}
}

// debugPlan is the fault plan of a debug run: mode replay with the fork
// point and edit for a replay or fork, the base mode for a step run, and
// the breakpoints for either.
func debugPlan(d *schemas.DebugSpec, runID, baseMode string) schemas.FaultPlan {
	plan := schemas.FaultPlan{Mode: baseMode, RunID: runID}
	if d.Mode == schemas.DebugReplay || d.Mode == schemas.DebugFork {
		plan.Mode = "replay"
		rp := &schemas.ReplayPlan{OnDivergence: d.Divergence()}
		if d.Mode == schemas.DebugFork {
			rp.UntilCall = d.Call
			if d.Edit != nil {
				rp.EditResponse, rp.EditMessage = d.Edit.Response, d.Edit.Message
			}
		}
		plan.Replay = rp
	}
	if !d.Breakpoints.Empty() {
		bp := *d.Breakpoints
		plan.Hold = &bp
	}
	return plan
}

// bindDebugPlan loads the cassette and binds the debug plan to the run's
// key slot. A proxy that cannot replay refuses the run: a replay that
// silently went live would spend money and prove nothing.
func (p *Pipeline) bindDebugPlan(ctx context.Context, st *runState, d *schemas.DebugSpec, baseMode string) error {
	slot := st.rc.Key.KeySlot
	if d.Cassette != nil && (d.Mode == schemas.DebugReplay || d.Mode == schemas.DebugFork) {
		rc, ok := p.reg.Fault.(schemas.ReplayController)
		if !ok {
			return &partError{part: faultPart, err: errors.New("this fault proxy cannot replay model calls; refusing the debug run")}
		}
		if err := rc.PutCassette(ctx, slot, d.Cassette); err != nil {
			return &partError{part: faultPart, err: fmt.Errorf("load recording on key slot %s: %w", slot, err)}
		}
	}
	plan := debugPlan(d, st.rc.Run.RunID, baseMode)
	if err := p.reg.Fault.SetPlan(ctx, slot, plan); err != nil {
		return &partError{part: faultPart, err: fmt.Errorf("bind debug plan to key slot %s: %w", slot, err)}
	}
	return nil
}

// debugClock returns ctx carrying a held clock for a run with breakpoints:
// a poller reads the run's held time from the proxy, and the environment
// extends the wall budget by it. stop ends the poller and records the held
// time in run.json. Without breakpoints ctx is returned as is.
func (p *Pipeline) debugClock(ctx context.Context, st *runState) (context.Context, func()) {
	d := debugSpec(st.rc)
	hc, ok := p.reg.Fault.(schemas.HoldController)
	if d == nil || d.Breakpoints.Empty() || !ok || st.rc.Run == nil {
		return ctx, func() {}
	}
	runID := st.rc.Run.RunID
	var held atomic.Int64
	read := func() {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if s, err := hc.Holds(rctx, runID); err == nil && s.HeldMS > held.Load() {
			held.Store(s.HeldMS)
		}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(heldPoll)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				read()
			}
		}
	}()
	clock := schemas.HeldClock(func() time.Duration { return time.Duration(held.Load()) * time.Millisecond })
	return schemas.WithHeldClock(ctx, clock), func() {
		close(done)
		read()
		if st.rc.Run.Debug != nil {
			st.rc.Run.Debug.HeldMS = held.Load()
		}
	}
}
