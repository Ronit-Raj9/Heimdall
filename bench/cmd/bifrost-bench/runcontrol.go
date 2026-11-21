package main

// Run control (handlers/runcontrol.go, bench/runctl): the launcher writes an
// experiment's controls in its store folder; a running scheduler, here or in
// a `run` process on the same store, reads them within a second.

import (
	"context"
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/engine"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/runctl"
	"github.com/maximhq/bifrost/bench/schemas"
)

var _ handlers.RunOperator = (*launcher)(nil)

func (l *launcher) storeRoot() string { return l.a.store.Root() }

// Controls returns the experiment's controls and verified log.
func (l *launcher) Controls(_ context.Context, expID string) (*runctl.State, []runctl.Entry, error) {
	st, err := runctl.Read(l.storeRoot(), expID)
	if err != nil {
		return nil, nil, err
	}
	log, err := runctl.Log(l.storeRoot(), expID)
	if err == nil {
		err = runctl.Verify(l.storeRoot(), expID)
	}
	return st, log, err
}

// Pause drains the experiment.
func (l *launcher) Pause(ctx context.Context, expID, reason string) (*runctl.State, error) {
	return runctl.Pause(l.storeRoot(), expID, actorOf(ctx), reason)
}

// Unpause clears a pause.
func (l *launcher) Unpause(ctx context.Context, expID, reason string) (*runctl.State, bool, error) {
	cur, err := runctl.Read(l.storeRoot(), expID)
	if err != nil {
		return nil, false, err
	}
	if !cur.Paused() {
		return cur, false, nil
	}
	st, err := runctl.Resume(l.storeRoot(), expID, actorOf(ctx), reason)
	return st, err == nil, err
}

// PatchControls applies live settings.
func (l *launcher) PatchControls(ctx context.Context, expID string, p runctl.Patch) (*runctl.State, error) {
	return runctl.Apply(l.storeRoot(), expID, actorOf(ctx), runctl.ActionSet, "", p.Reason, func(s *runctl.State) error {
		p.ApplyTo(s)
		return nil
	})
}

// CancelRun holds the slot out and stops its in-flight run.
func (l *launcher) CancelRun(ctx context.Context, expID, slotID, runID, reason string) (*runctl.State, error) {
	return engine.CancelRun(l.storeRoot(), expID, actorOf(ctx), slotID, runID, reason)
}

// RequeueRun supersedes the slot's newest run (or lifts its cancel) and
// starts the experiment's run when none is active.
func (l *launcher) RequeueRun(ctx context.Context, expID, slotID, reason string) (*runctl.State, error) {
	recs, err := l.a.store.SlotRuns(ctx, expID, slotID)
	if err != nil && !errors.Is(err, schemas.ErrNotFound) {
		return nil, err
	}
	st, err := engine.Requeue(l.storeRoot(), expID, actorOf(ctx), slotID, reason, recs)
	if err != nil {
		return nil, err
	}
	_ = l.rebuild(ctx) // the index reads superseded runs
	if !l.Active(expID) {
		if err := l.Start(ctx, expID); err != nil && !errors.Is(err, schemas.ErrExists) {
			return st, fmt.Errorf("requeued; starting the experiment failed (resume it from its page): %w", err)
		}
	}
	return st, nil
}
