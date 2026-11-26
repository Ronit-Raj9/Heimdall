package main

// Wave 4 lane I35 (W4-48): the weekly issue sync schedule. A schedule named
// issue-sync-<id> with job issue-sync runs one sync of that repository; the
// sync keeps its own weekly spending cap (issuesync.Run).

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/issuesync"
	"github.com/maximhq/bifrost/bench/schedule"
)

// issueSyncRun is issuesync.Run; tests replace it.
var issueSyncRun = issuesync.Run

// runIssueSyncSchedule runs one weekly issue sync for the org of svc.
func runIssueSyncSchedule(ctx context.Context, a *app, svc *handlers.Services, s schedule.Schedule) (*schedule.Result, error) {
	if a == nil || a.cfg == nil {
		return nil, errors.New("no organization app")
	}
	id := strings.TrimPrefix(s.Name, schedule.IssueSyncPrefix)
	if id == s.Name || id == "" {
		return nil, fmt.Errorf("schedule %s: a weekly issue sync schedule is named %s<sync id>", s.Name, schedule.IssueSyncPrefix)
	}
	env := handlers.OrgEnv{Cfg: a.cfg, Mode: a.cfg.Server.Mode}
	if svc != nil {
		env.Services = *svc
	}
	ref, err := issueSyncRun(ctx, handlers.IssueSyncDeps(env), id)
	if err != nil {
		return nil, err
	}
	res := &schedule.Result{CostUSD: ref.SpentUSD,
		Summary: fmt.Sprintf("%s: %d new issues started, %d skipped", ref.Status, ref.Started, ref.Skipped)}
	if ref.Reason != "" {
		res.Summary += "; " + ref.Reason
	}
	if ref.Status == issuesync.RunFailed {
		return res, errors.New(ref.Reason)
	}
	return res, nil
}
