package engine

import (
	"errors"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Failure causes (schemas/failurecause.go): a short code for why a plan or
// a worker dispatch did not start any run.
const (
	CauseImageMissing  = schemas.CauseImageMissing
	CauseWorkerRefused = schemas.CauseWorkerRefused
	CauseNoWorker      = schemas.CauseNoWorker
)

// FailureCause reads the cause code of a dispatch or plan failure message
// (schemas.FailureCause). It is pure and never fails; an unknown message
// answers "".
func FailureCause(msg string) string { return schemas.FailureCause(msg) }

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
