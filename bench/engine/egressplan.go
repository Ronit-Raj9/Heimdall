package engine

import (
	"fmt"

	"github.com/maximhq/bifrost/bench/egress"
	"github.com/maximhq/bifrost/bench/schemas"
)

// checkEgress refuses an experiment egress mode that is not offline, replay
// or live-allowlisted (record is for admission only, `egress record`), and
// warns when live-allowlisted runs will reach real hosts: their results are
// not repeatable and are labelled like external: live (docs/egress.md).
func (p *Planner) checkEgress(e *schemas.Experiment, suite *schemas.Suite, conds []condPlan) error {
	if !egress.RunMode(e.Egress) {
		return fmt.Errorf("experiment egress %q is not offline, replay or live-allowlisted", e.Egress)
	}
	if e.Egress == "" {
		return nil
	}
	any := false
	for _, sv := range suite.Services {
		any = any || sv.Role == schemas.ServiceRoleEgress
	}
	for _, id := range usedTasks(conds) {
		if t := suite.TaskByID(id); t != nil {
			for _, sv := range t.Services {
				any = any || sv.Role == schemas.ServiceRoleEgress
			}
		}
	}
	switch {
	case !any:
		p.logger.Warn("experiment sets egress, but no planned task has an egress service", "experiment", e.ID, "egress", e.Egress)
	case e.Egress == egress.ModeLive:
		p.logger.Warn("egress live-allowlisted: allowlisted hosts are reached live, results are not repeatable and are labelled", "experiment", e.ID)
	}
	return nil
}
