package engine

import (
	"context"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
)

// worldTick makes time pass before session index (0-based) of a task with
// a world block: the environment POSTs the tick hook of the task's services
// with the session's date and the events of the session that just ended,
// and every tick is appended to world.json. Session 0 and a task without a
// world change nothing. A tick that an event needed and that failed fails
// the run's run stage: the agent would otherwise be graded against a world
// that never changed.
func (p *Pipeline) worldTick(ctx context.Context, st *runState, task *schemas.Task, index int) error {
	if task == nil || task.World == nil || index == 0 {
		return nil
	}
	we, ok := st.env.(schemas.WorldEnvironment)
	if !ok {
		return wrapPart("environment", st.env, fmt.Errorf("task %s has a world clock, but environment %s cannot deliver world ticks", task.ID, st.env.GetName()))
	}
	date, err := task.World.SessionDate(index)
	if err != nil {
		return fmt.Errorf("world clock: %w", err)
	}
	ticks, err := we.WorldTick(ctx, st.rc, index+1, date, task.World.EventsAfter(index))
	st.world = append(st.world, ticks...)
	if werr := p.writeJSON(ctx, st, schemas.WorldFile, st.world); werr != nil && err == nil {
		err = werr
	}
	if err != nil {
		return wrapPart("environment", st.env, fmt.Errorf("world tick before session %d: %w", index+1, err))
	}
	return nil
}
