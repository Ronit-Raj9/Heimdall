package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// A busy host makes gVisor fail to start a sandbox with
// "fork/exec /usr/bin/gvisor-bin/gvisor-sentry-prewarmer: resource
// temporarily unavailable" (hundreds of times in two hours on the shared
// worker, round 4). Nothing ran yet, so the start is tried again a few
// times after a short wait instead of failing the run. Only this error is
// retried: any other start failure fails the run as before.
const busyStartRetries = 3

// busyStartWait is the wait before each retry (a variable for tests).
var busyStartWait = 5 * time.Second

// sandboxStartBusy reports whether err is gVisor's busy-host start failure.
func sandboxStartBusy(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "resource temporarily unavailable") && strings.Contains(s, "gvisor")
}

// retryBusyStart runs start, and again up to busyStartRetries times while it
// fails with sandboxStartBusy and reports that nothing ran (started false),
// saying each retry on the run's events.
func (p *Pipeline) retryBusyStart(ctx context.Context, st *runState, stage schemas.Stage, start func() (started bool, err error)) error {
	started, err := start()
	for i := 1; i <= busyStartRetries && !started && sandboxStartBusy(err); i++ {
		p.emit(st, Event{Kind: EventStage, Stage: stage,
			Message: fmt.Sprintf("the sandbox could not start because the machine is busy; trying again (%d of %d)", i, busyStartRetries)})
		select {
		case <-ctx.Done():
			return err
		case <-time.After(busyStartWait):
		}
		started, err = start()
	}
	return err
}
