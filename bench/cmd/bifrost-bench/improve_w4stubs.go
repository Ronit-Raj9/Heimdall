package main

import (
	"context"
	"errors"

	"github.com/maximhq/bifrost/bench/improve"
)

// Placeholders that keep improveExecutor an improve.Executor until the
// executor work lands; that work deletes this file.

// CheckImprover implements improve.Executor; it refuses nothing yet.
func (x improveExecutor) CheckImprover(context.Context, *improve.Spec) error { return nil }

// Runs implements improve.Executor; run listing is not wired yet.
func (x improveExecutor) Runs(context.Context, string) ([]improve.RunRef, error) {
	return nil, errors.New("not wired yet")
}
