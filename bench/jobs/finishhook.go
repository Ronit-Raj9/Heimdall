package jobs

import (
	"sync"
	"time"
)

// FinishHook is told about a job that reached a final status on this runner
// (succeeded, cancelled or failed). It runs on its own goroutine, after the
// final status was written; it must not block for long or modify j.
type FinishHook func(j Job, status Status, result []byte, msg string)

var (
	finishHooksMu sync.Mutex
	finishHooks   []FinishHook
)

// OnFinish adds a hook every Runner calls when a job it ran ends
// (web parity L19: experiment-finished webhooks). Call it from init().
func OnFinish(f FinishHook) {
	finishHooksMu.Lock()
	defer finishHooksMu.Unlock()
	finishHooks = append(finishHooks, f)
}

func runFinishHooks(j *Job, status Status, result []byte, msg string, at time.Time) {
	finishHooksMu.Lock()
	hs := append([]FinishHook(nil), finishHooks...)
	finishHooksMu.Unlock()
	if len(hs) == 0 {
		return
	}
	c := *j
	c.Status, c.Error, c.FinishedAt = status, msg, &at
	for _, h := range hs {
		go func() {
			defer func() { _ = recover() }()
			h(c, status, result, msg)
		}()
	}
}
