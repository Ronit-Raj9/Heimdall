package engine

import (
	"errors"
	"fmt"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// checkExternal refuses a plan whose external APIs (Suite.External,
// Task.External, docs/external-apis.md) cannot run as declared: an
// experiment `external:` that is not live, record or replay; an API that
// does not validate or whose id is also a service id; and an API whose mode
// cannot be resolved (utils.ExternalMode): replay without its cassette, no
// cassette and no mode, or live or record without the experiment opting in.
// A plan whose runs will call real hosts is allowed but warned about: its
// results are not repeatable, and reports and the leaderboard say so.
func (p *Planner) checkExternal(e *schemas.Experiment, suite *schemas.Suite, conds []condPlan) error {
	var errs []error
	switch e.External {
	case "", schemas.ExternalLive, schemas.ExternalRecord, schemas.ExternalReplay:
	default:
		return fmt.Errorf("experiment external %q is not live, record or replay", e.External)
	}
	anyExternal := false
	for _, id := range usedTasks(conds) {
		t := suite.TaskByID(id)
		if t == nil {
			continue
		}
		refs := utils.ExternalAPIs(suite, t)
		if len(refs) == 0 {
			continue
		}
		anyExternal = true
		if err := utils.CheckExternalAPIs(refs); err != nil {
			errs = append(errs, fmt.Errorf("task %s: %w", id, err))
			continue
		}
		services := map[string]bool{}
		for _, sv := range utils.TaskServices(suite, t) {
			services[sv.ID] = true
		}
		for _, r := range refs {
			if services[r.API.ID] {
				errs = append(errs, fmt.Errorf("task %s: external api id %q is also a service id", id, r.API.ID))
				continue
			}
			_, have, err := utils.ExternalCassette(r)
			if err == nil {
				_, err = utils.ExternalMode(r.API, e.External, have)
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("task %s: %w", id, err))
			}
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	switch {
	case !anyExternal && e.External != "":
		p.logger.Warn("experiment sets external, but no planned task has an external api", "experiment", e.ID, "external", e.External)
	case anyExternal && (e.External == schemas.ExternalLive || e.External == schemas.ExternalRecord):
		p.logger.Warn("external apis run against the real hosts: results are not repeatable and are labelled", "experiment", e.ID, "external", e.External)
	}
	return nil
}
