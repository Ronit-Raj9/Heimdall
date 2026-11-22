package engine

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X2 same-error stop. An experiment whose runs all fail the same way before
// the agent starts (a service that never becomes ready, an image that does
// not build) used to retry every slot to its attempt limit. After
// setupStopAfter such runs in a row the remote path stops the experiment's
// queued slots with the reason. The local scheduler is unchanged.

const (
	setupStopAfter = 3   // runs in a row with the same setup failure
	infraSigMax    = 200 // bytes of a failure signature
	stopReasonMax  = 300 // bytes of the failure text in the stop reason
)

// infraNoise matches what differs between two runs that failed the same way:
// run ids, hex ids and numbers.
var infraNoise = regexp.MustCompile(`r_[a-z0-9]{6,}|[0-9a-f]{8,}|\d+`)

// infraSignature returns the normalized first failure of a run that ended
// aborted_infra at the prepare or arm stage (before its agent started), and
// whether it did. Two runs that failed the same way get the same signature.
func infraSignature(run *schemas.Run) (sig string, beforeAgent bool) {
	if run == nil || run.Outcome != schemas.OutcomeAbortedInfra || len(run.Failures) == 0 {
		return "", false
	}
	f := run.Failures[0]
	if f.Stage != schemas.StagePrepare && f.Stage != schemas.StageArm {
		return "", false
	}
	sig = infraNoise.ReplaceAllString(f.Part+": "+f.Error, "#")
	if len(sig) > infraSigMax {
		sig = sig[:infraSigMax]
	}
	return sig, true
}

// reachedAgent reports whether a run got to its agent: any outcome but
// aborted_infra, or a first failure at the run stage or later.
func reachedAgent(run *schemas.Run) bool {
	if run == nil {
		return false
	}
	if run.Outcome != schemas.OutcomeAbortedInfra {
		return true
	}
	if len(run.Failures) == 0 {
		return false
	}
	return slices.Index(schemas.Stages, run.Failures[0].Stage) >= slices.Index(schemas.Stages, schemas.StageRun)
}

// stopReason is the first line of the run's first failure, at most
// stopReasonMax bytes.
func stopReason(run *schemas.Run) string {
	if run == nil || len(run.Failures) == 0 {
		return "unknown failure"
	}
	f := run.Failures[0]
	text, _, _ := strings.Cut(f.Error, "\n")
	if f.Part != "" {
		text = f.Part + ": " + text
	}
	if len(text) > stopReasonMax {
		text = text[:stopReasonMax]
	}
	return text
}

// remoteRunEnded applies the X2 rules to a run a remote worker completed and
// returns whether its slot goes back to the queue (again is the caller's
// answer so far). A slot that goes back after aborted_infra waits
// infraRetryDelay(attempt) before the next lease. The setupStopAfter-th run
// in a row that failed the same way before its agent started, with no run of
// the experiment having reached its agent, ends this slot and every queued
// one with the reason.
func (c *Coordinator) remoteRunEnded(l *remoteLease, run *schemas.Run, again bool) bool {
	r := l.r
	sig, before := infraSignature(run)
	r.mu.Lock()
	switch {
	case reachedAgent(run):
		r.agentStarted, r.setupFails, r.setupSig = true, 0, ""
	case before && sig == r.setupSig:
		r.setupFails++
	case before:
		r.setupSig, r.setupFails = sig, 1
	}
	n := r.setupFails
	stop := before && !r.agentStarted && n >= setupStopAfter
	if !stop && again {
		if r.notBefore == nil {
			r.notBefore = map[string]time.Time{}
		}
		r.notBefore[l.slot.SlotID] = c.now().Add(infraRetryDelay(l.attempt))
		r.retryNow = c.now
	}
	r.mu.Unlock()
	if !stop {
		return again
	}
	err := fmt.Errorf("stopped: %d runs in a row failed the same way before the agent started: %s. Fix the cause, then requeue the stopped runs on the comparison page", n, stopReason(run))
	c.logger.Warn("experiment stopped: the same setup failure repeated", "experiment", r.m.ExpID, "runs", n, "failure", stopReason(run))
	r.slotError(l.slot, err)
	for _, s := range r.queue.takeAll() {
		r.slotError(s, err)
	}
	return false
}
