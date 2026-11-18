package engine

import (
	"context"

	"github.com/maximhq/bifrost/bench/schemas"
)

// W5 drift canary (bench/canary, docs/drift-canary.md).

// PlanCanary plans a drift canary run: an experiment whose arms are the
// watched targets, so one arm is enough. Everything else is planned exactly
// as Plan plans an experiment: a locked suite (a smoke canary only warns),
// reachable upstreams, the same arm resolution and hashing. The canary
// compares the run with its pinned baseline itself; no release verdict
// reads a canary experiment.
func (p *Planner) PlanCanary(ctx context.Context, exp *schemas.Experiment) (*schemas.Manifest, error) {
	return p.plan(ctx, exp, planMode{minArms: 1})
}
