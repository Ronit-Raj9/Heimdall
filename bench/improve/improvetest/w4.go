package improvetest

import (
	"context"

	"github.com/maximhq/bifrost/bench/improve"
)

// CheckImprover implements improve.Executor: the scripted improver always runs.
func (x *Exec) CheckImprover(context.Context, *improve.Spec) error { return nil }

// Runs implements improve.Executor: scripted experiments keep no run folders.
func (x *Exec) Runs(context.Context, string) ([]improve.RunRef, error) {
	return []improve.RunRef{}, nil
}
