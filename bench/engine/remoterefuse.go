package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X3: a refused lease is not final. A worker refuses an experiment when its
// project does not reproduce the manifest yet (a benchmark added after its
// last project download) or when a check failed for a passing reason (a
// docker image inspect killed on a busy host). The coordinator offers the
// experiment to that worker again after refuseRetry, and the worker checks it
// again after the same time. After maxRefusals refusals by every worker that
// could take it, the queued slots fail with the reason, so the job ends with
// it instead of waiting forever.
const (
	refuseRetry = 60 * time.Second
	maxRefusals = 3
)

// leaseRefusal is one worker's refusals of one experiment: the last reason,
// when it came, and how many there were since the worker registered.
type leaseRefusal struct {
	reason string
	at     time.Time
	n      int
}

// refusedLocked reports whether r is kept from w for now: within refuseRetry
// of its last refusal, or for good after maxRefusals (until the worker
// registers again). The caller holds c.mu.
func (c *Coordinator) refusedLocked(w *remoteWorker, r *schedRun) bool {
	f := w.refused[r.m.ExpID]
	if f == nil {
		return false
	}
	return f.n >= maxRefusals || c.now().Sub(f.at) < refuseRetry
}

// noteRefusalLocked records a refusal of l's experiment by its worker and
// returns the job log line for it. The caller holds c.mu.
func (c *Coordinator) noteRefusalLocked(l *remoteLease, reason string) string {
	w := c.workers[l.worker]
	if w == nil {
		return ""
	}
	f := w.refused[l.r.m.ExpID]
	if f == nil {
		f = &leaseRefusal{}
		w.refused[l.r.m.ExpID] = f
	}
	f.reason, f.at = reason, c.now()
	f.n++
	plain := PlainRefusal(reason)
	if BuildFailurePermanent(reason) {
		// PH4-2: a build that failed for its own reason fails the same way on
		// every try and every worker; the run ends now with the build's lines.
		f.n = maxRefusals
		return fmt.Sprintf("worker %s could not build the agent image: %s. This fails the same way on every try, so the run stops.", l.worker, plain)
	}
	if PlanRefusalPermanent(reason) {
		// PB1-11: the worker's own plan of this setup fails a check (a gateway
		// feature its Bifrost does not support); it fails the same way on
		// every try, so the run ends now with the reason.
		f.n = maxRefusals
		return fmt.Sprintf("worker %s cannot run this setup: %s. This fails the same way on every try, so the run stops.", l.worker, plain)
	}
	if buildFailure(reason) {
		return fmt.Sprintf("worker %s could not take this run: %s. Retrying the image build (%d of %d) in 1 minute.", l.worker, plain, f.n+1, maxRefusals)
	}
	if f.n >= maxRefusals {
		return fmt.Sprintf("worker %s could not take this run: %s. It refused it %d times, so Heimdall stops offering it to this worker.", l.worker, plain, f.n)
	}
	return fmt.Sprintf("worker %s could not take this run: %s. Heimdall offers it again in 1 minute.", l.worker, plain)
}

// everyWorkerRefusedLocked reports whether no worker is left for r: every
// registered worker that is not lost and serves r's pool refused it
// maxRefusals times, and no lease of r is out. The caller holds c.mu.
func (c *Coordinator) everyWorkerRefusedLocked(r *schedRun) bool {
	for _, l := range c.leases {
		if l.r == r {
			return false
		}
	}
	any := false
	for _, id := range c.order {
		w := c.workers[id]
		if w == nil || w.info.State == schemas.WorkerLost || !c.openTo(w, r) {
			continue
		}
		f := w.refused[r.m.ExpID]
		if f == nil || f.n < maxRefusals {
			return false
		}
		any = true
	}
	return any
}

// failRefused ends every queued slot of r with the last refusal reason.
func (r *schedRun) failRefused(reason string) {
	err := fmt.Errorf("every worker refused this run %d times: %s", maxRefusals, PlainRefusal(reason))
	if BuildFailurePermanent(reason) {
		err = fmt.Errorf("the agent image cannot be built, and trying again would fail the same way: %s", PlainRefusal(reason))
	} else if PlanRefusalPermanent(reason) {
		err = fmt.Errorf("the worker cannot run this setup, and trying again would fail the same way: %s", PlainRefusal(reason))
	}
	for _, slot := range r.queue.takeAll() {
		r.slotError(slot, err)
	}
}

// PlainRefusal says a worker's refusal reason in plain words where a person
// can act on it, and keeps the raw reason after "Details: " (fix wave 2,
// N151): a benchmark missing from the worker's project copy, a benchmark
// changed after the run was planned, or a worker on another Heimdall
// version. Any other reason is returned as is.
func PlainRefusal(reason string) string {
	var plain string
	switch {
	case strings.HasPrefix(reason, "load suite") && strings.Contains(reason, "not found"):
		plain = "The worker's copy of your project does not have this benchmark yet."
	case strings.Contains(reason, "manifest hashes to"), strings.Contains(reason, "suite lock hash"):
		plain = "The benchmark changed after this run was planned: start it again."
	case strings.Contains(reason, "part versions differ"):
		plain = "The worker runs a different Heimdall version than the server."
	case strings.Contains(reason, "Lchown") || strings.Contains(reason, "subordinate"):
		plain = "The task image has files owned by a user id the worker's Docker cannot map. The server's subordinate id range must be made larger."
	case strings.Contains(reason, "No matching distribution found") || strings.Contains(reason, "requires a different Python"):
		plain = "A pinned Python package has no version for the Python in this image."
	case strings.Contains(reason, "python3: not found") || strings.Contains(reason, "python3-venv"):
		plain = "The agent's Dockerfile needs Python in the task image, and this task image has none."
	default:
		return reason
	}
	return plain + " Details: " + reason
}

// refusalFresh reports whether a worker's cached refusal may still be
// reused; after refuseRetry the experiment is checked again.
func (w *Worker) refusalFresh(e *workerExp) bool {
	return w.now().Sub(e.refusedAt) < refuseRetry
}

// buildFailure reports whether a refusal reason is a failed docker build of
// an image the run needs.
func buildFailure(reason string) bool {
	return strings.Contains(reason, "docker build") || strings.Contains(reason, "build agent image")
}

// transientBuildMarks are build errors that may pass on the next try: the
// network, a registry, a busy host.
var transientBuildMarks = []string{
	"resource temporarily unavailable", "i/o timeout", "TLS handshake timeout", "connection reset by peer",
	"connection refused", "temporary failure in name resolution", "Temporary failure in name resolution",
	"502 Bad Gateway", "503 Service Unavailable", "504 Gateway Timeout", "429 Too Many Requests",
	"toomanyrequests", "context deadline exceeded", "unexpected EOF", "signal: killed",
}

// BuildFailurePermanent reports whether a refusal reason is an image build
// that failed for its own reason (a pip install with no matching version, a
// missing file, a Dockerfile error, a file owner the host cannot map): it
// fails the same way again, so retrying only keeps the run waiting (PH4-2).
// A build that failed on the network or a busy host is not permanent.
func BuildFailurePermanent(reason string) bool {
	if !buildFailure(reason) {
		return false
	}
	for _, m := range transientBuildMarks {
		if strings.Contains(reason, m) {
			return false
		}
	}
	return true
}

// planRefusalPrefix starts a worker refusal whose own plan of the
// experiment failed (remoteworker.go check).
const planRefusalPrefix = "plan the experiment locally: "

// PlanRefusalPermanent reports whether a refusal is the worker's plan of the
// experiment failing a check, after the worker refreshed its project: the
// same manifest fails the same check on the next try (PB1-11, a prompt cache
// arm on a provider with no prompt_cache block). A benchmark missing from the
// project copy or a passing error (network, busy host) is not permanent.
func PlanRefusalPermanent(reason string) bool {
	if !strings.HasPrefix(reason, planRefusalPrefix) || strings.Contains(reason, "not found") {
		return false
	}
	for _, m := range transientBuildMarks {
		if strings.Contains(reason, m) {
			return false
		}
	}
	return true
}

// RefusalPermanent reports whether a refusal ends the run at once instead
// of being offered again (PH4-2, PB1-11).
func RefusalPermanent(reason string) bool {
	return BuildFailurePermanent(reason) || PlanRefusalPermanent(reason)
}
