package engine

import (
	"errors"
	"strings"
)

// Failure causes: a short code for why a plan or a worker dispatch did not
// start any run, so a page can say the cause in plain words and keep the
// raw message behind a disclosure. The raw message stays the source of
// truth; the code is a reading of it and "" means not recognised.
const (
	CauseImageMissing  = "image_missing"  // a task or base image is not on the shared worker's registry
	CauseWorkerRefused = "worker_refused" // a runner took the lease and refused it
	CauseNoWorker      = "no_worker"      // no runner was online or none claimed the work
)

// FailureCause reads the cause code of a dispatch or plan failure message.
// It is pure and never fails; an unknown message answers "".
func FailureCause(msg string) string {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "image registry") && (strings.Contains(m, "is not in") || strings.Contains(m, "not in the shared")),
		strings.Contains(m, "image") && (strings.Contains(m, "not found") || strings.Contains(m, "manifest unknown") || strings.Contains(m, "is missing")):
		return CauseImageMissing
	case strings.Contains(m, ErrNoRunner.Error()), strings.Contains(m, "no runner claimed"), strings.Contains(m, "no worker"):
		return CauseNoWorker
	case strings.Contains(m, "lease refused"), strings.Contains(m, "refused:"), strings.Contains(m, "policy refused"), strings.Contains(m, "worker refused"):
		return CauseWorkerRefused
	}
	return ""
}

// FailureCauseOf is FailureCause of an error; ErrNoRunner anywhere in the
// chain is CauseNoWorker.
func FailureCauseOf(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrNoRunner) {
		return CauseNoWorker
	}
	return FailureCause(err.Error())
}
