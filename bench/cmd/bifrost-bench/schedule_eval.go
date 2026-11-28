package main

// Lane X12: scheduled re-evaluations. A schedule that names an evaluation
// (a saved evaluation id or a spec file) starts a new evaluation of that
// spec at each trigger through the same path as POST /evaluations. The
// evaluation runs on after the trigger ends; its gate is judged when it
// finishes. Its whole budget counts against the schedule's daily cap.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/projectfs"
	"github.com/maximhq/bifrost/bench/schedule"
)

// launchScheduledEvaluation is handlers.LaunchEvaluation; tests replace it.
var launchScheduledEvaluation = handlers.LaunchEvaluation

// runEvaluationSchedule starts one scheduled evaluation for the org of svc.
func runEvaluationSchedule(ctx context.Context, a *app, svc *handlers.Services, s schedule.Schedule) (*schedule.Result, error) {
	if a == nil || a.cfg == nil {
		return nil, errors.New("no organization app")
	}
	if svc == nil || svc.Launcher == nil {
		return nil, errors.New("evaluation schedules run from the server (serve), which launches their experiments; this process has no launcher")
	}
	root := a.cfg.Root
	spec, err := schedule.LoadEvaluationSpec(root, s.Evaluation)
	if err != nil {
		return nil, err
	}
	if s.NewModels != nil {
		return runNewModelSchedule(ctx, a, svc, s, spec)
	} // W4 feature 3 (schedule_newmodel.go)
	capUSD := schedule.EvaluationCap(s, spec)
	if err := s.Approved.CheckCap(capUSD); err != nil {
		return nil, err
	}
	spec.BudgetUSD = capUSD
	env := handlers.OrgEnv{Cfg: a.cfg, Mode: a.cfg.Server.Mode, Services: *svc}
	if env.Services.Projects == nil {
		if ps, err := projectfs.Open(root); err == nil {
			env.Services.Projects = ps
		}
	}
	ev, err := launchScheduledEvaluation(ctx, env, *spec, "schedule:"+s.Name, s.Name)
	if err != nil {
		return nil, fmt.Errorf("start evaluation: %w", err)
	}
	started := 0
	for _, c := range ev.Children {
		if c.ExperimentID != "" {
			started++
		}
	}
	res := &schedule.Result{EvaluationID: ev.ID, CostUSD: capUSD,
		Summary: fmt.Sprintf("evaluation %s started on %d of %d benchmarks, budget %.2f USD", ev.ID, started, len(ev.Children), capUSD)}
	if started == 0 { // nothing started, nothing spent: the reserved budget is freed (F2, PH5-13)
		res.CostUSD = 0
		var why []string
		for _, c := range ev.Children {
			if c.Skipped != "" {
				why = append(why, c.Benchmark+": "+c.Skipped)
			}
		}
		if len(why) == 0 {
			return res, fmt.Errorf("evaluation %s started no benchmark: see its page for why each was skipped", ev.ID)
		}
		return res, fmt.Errorf("evaluation %s started no benchmark. %s", ev.ID, strings.Join(why, " "))
	}
	return res, nil
}
