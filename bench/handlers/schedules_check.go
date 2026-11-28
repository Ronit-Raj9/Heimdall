package handlers

// Lane F2: a repeat of an evaluation is checked when it is saved with the
// same readiness and plan checks its firing runs, and a schedule whose
// firings keep failing pauses itself.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
)

// scheduleCheckTimeout bounds the save-time plan check of one schedule.
const scheduleCheckTimeout = 60 * time.Second

// CheckEvaluationLaunch plans spec for every benchmark the way a launch
// does (benchmark found, agent fits, ready tasks, plan) without writing or
// starting anything. It returns nil when at least one benchmark would start,
// else an error naming why each would be skipped. With no launcher or
// project store here there is nothing to plan against, and it returns nil.
func CheckEvaluationLaunch(ctx context.Context, env OrgEnv, spec schemas.EvaluationSpec) error {
	s := env.Services
	if s.Launcher == nil || s.Projects == nil {
		return nil
	}
	h := &evaluationsHandler{env: env, now: time.Now, planOnly: true}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), scheduleCheckTimeout) // the request keeps its values; the check its own deadline
	defer cancel()
	if err := h.normalize(work, &spec); err != nil {
		return err
	}
	ev := h.newEvaluation(spec, "schedule-check", "")
	agents := h.agentConfigs(work)
	budget := childBudget(spec)
	var why []string
	for _, b := range spec.Benchmarks {
		c := h.launchChild(work, work, nil, ev, b, agents, budget, ev.CreatedBy)
		if c.Skipped == "" {
			return nil
		}
		why = append(why, b+": "+c.Skipped)
	}
	if len(why) == 0 {
		return errors.New("the evaluation names no benchmark")
	}
	return fmt.Errorf("it could not start now: %s", strings.Join(why, " "))
}

// checkEvaluationSchedules runs CheckEvaluationLaunch for every evaluation
// schedule of f that is new or changed against old (a pause or a rename
// alone is not checked again; a paused schedule is not checked).
func checkEvaluationSchedules(ctx context.Context, env OrgEnv, old, f *schedule.File) error {
	for _, s := range f.Schedules {
		if s.Evaluation == "" || s.Paused || s.NewModels != nil {
			continue
		}
		if old != nil {
			if prev, ok := old.Find(s.Name); ok && prev.Evaluation == s.Evaluation && prev.MaxUSDPerDay == s.MaxUSDPerDay && !prev.Paused {
				continue
			}
		}
		spec, err := schedule.LoadEvaluationSpec(env.Cfg.Root, s.Evaluation)
		if err != nil {
			return fmt.Errorf("schedule %s: %w", s.Name, err)
		}
		spec.BudgetUSD = schedule.EvaluationCap(s, spec)
		if err := CheckEvaluationLaunch(ctx, env, *spec); err != nil {
			return fmt.Errorf("schedule %s repeats an evaluation that %w", s.Name, err)
		}
	}
	return nil
}

// PauseWebSchedule pauses one web schedule by itself with reason, after
// failed firings in a row. A schedule not in the web file (a canary) is
// left alone.
func PauseWebSchedule(root, name, reason string) error {
	f, err := LoadWebSchedules(root)
	if err != nil {
		return err
	}
	for i := range f.Schedules {
		if f.Schedules[i].Name == name {
			f.Schedules[i].Paused, f.Schedules[i].PausedReason = true, reason
			return schedule.Save(WebSchedulesPath(root), f)
		}
	}
	return nil
}
