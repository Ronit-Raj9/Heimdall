package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

const gvisorBusy = "docker start: OCI runtime start failed: fork/exec /usr/bin/gvisor-bin/gvisor-sentry-prewarmer: resource temporarily unavailable"

func busyState(events *[]Event) *runState {
	return &runState{
		in: SlotInput{Emit: func(ev Event) { *events = append(*events, ev) }},
		rc: &schemas.RunContext{Slot: &schemas.Slot{SlotID: "s_a"}, Run: &schemas.Run{RunID: "r_a"}},
	}
}

func TestRetryBusyStart(t *testing.T) {
	old := busyStartWait
	busyStartWait = time.Millisecond
	defer func() { busyStartWait = old }()
	p := &Pipeline{now: time.Now}

	// Busy twice, then up: started on the third try, two retries said.
	var events []Event
	n := 0
	err := p.retryBusyStart(context.Background(), busyState(&events), schemas.StageRun, func() (bool, error) {
		n++
		if n < 3 {
			return false, errors.New(gvisorBusy)
		}
		return true, nil
	})
	if err != nil || n != 3 || len(events) != 2 || !strings.Contains(events[1].Message, "trying again (2 of 3)") {
		t.Fatalf("err %v, tries %d, events %+v", err, n, events)
	}

	// Always busy: 1 try and 3 retries, then the error.
	events, n = nil, 0
	err = p.retryBusyStart(context.Background(), busyState(&events), schemas.StagePrepare, func() (bool, error) {
		n++
		return false, errors.New(gvisorBusy)
	})
	if err == nil || n != 1+busyStartRetries {
		t.Fatalf("err %v, tries %d", err, n)
	}

	// Any other error, or a busy error after the agent ran: no retry.
	for _, c := range []struct {
		started bool
		msg     string
	}{{false, "exec format error"}, {true, gvisorBusy}, {false, "resource temporarily unavailable"}} {
		n = 0
		_ = p.retryBusyStart(context.Background(), busyState(&events), schemas.StageRun, func() (bool, error) {
			n++
			return c.started, errors.New(c.msg)
		})
		if n != 1 {
			t.Errorf("%q started=%v: %d tries, want 1", c.msg, c.started, n)
		}
	}
}
