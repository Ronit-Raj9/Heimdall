package improve

import (
	"context"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Runner creates, drives, pauses and stops improvement runs. A run resumes
// from its last saved stage, so a crash or a failed experiment is retried
// with another Run call.
type Runner struct {
	Root   string // <runs root>/.improvements
	Exec   Executor
	Now    func() time.Time
	Logger schemas.Logger

	// The parts of the loop (A1.9); nil uses the package default.
	Evidence  EvidenceBuilder
	Proposer  Proposer
	Screener  Screener
	Commit    CommitRule
	Claim     ClaimRule
	Estimator Estimator
	// Objects is the content-addressed store; a zero Root keeps no objects.
	Objects Objects
	// OnEvent is called when a run finishes or pauses; nil calls nothing.
	OnEvent func(kind string, rec *Record)

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
