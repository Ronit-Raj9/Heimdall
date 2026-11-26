package main

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/issuesync"
	"github.com/maximhq/bifrost/bench/lib"
	"github.com/maximhq/bifrost/bench/schedule"
)

func TestRunIssueSyncScheduleDispatch(t *testing.T) {
	got := ""
	old := issueSyncRun
	issueSyncRun = func(ctx context.Context, d issuesync.Deps, id string) (issuesync.RunRef, error) {
		got = id
		return issuesync.RunRef{Status: issuesync.RunNothingNew}, nil
	}
	defer func() { issueSyncRun = old }()
	a := &app{cfg: &lib.Config{Root: t.TempDir()}}
	s := schedule.Schedule{Name: schedule.IssueSyncPrefix + "sync-0000aaaa", Job: schedule.JobIssueSync, Cron: "0 3 * * 1"}
	res, err := (scheduleRunner{a: a}).Run(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sync-0000aaaa" || res == nil || res.CostUSD != 0 {
		t.Fatalf("id %q res %+v", got, res)
	}
}
