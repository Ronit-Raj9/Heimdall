package main

import (
	"context"

	"github.com/maximhq/bifrost/bench/investigator"
	"github.com/maximhq/bifrost/bench/jobs"
)

// modelParams are the model fields every model-backed job of wave 4 takes.
type modelParams struct {
	Provider  string  `json:"provider"`
	Model     string  `json:"model"`
	KeyID     string  `json:"key_id,omitempty"`
	BudgetUSD float64 `json:"budget_usd"`
	Yes       bool    `json:"yes"` // the person confirmed the cost
}

// openCaller opens a budgeted model caller for one job. Lane W4-42 sets it
// in the init() of jobs_investigator.go; nil means this server makes no
// model calls, and the job fails with errNoModelCalls. done revokes the key
// and records the spend; call it once, also on failure.
var openCaller func(ctx context.Context, env jobEnv, j *jobs.Job, p modelParams) (c investigator.Caller, done func(), err error)
