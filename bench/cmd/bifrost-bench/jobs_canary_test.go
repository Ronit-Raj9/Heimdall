package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
)

type fakeJobs struct {
	handlers.JobsAPI
	created []jobs.NewJob
}

func (f *fakeJobs) Create(_ context.Context, n jobs.NewJob) (*jobs.Job, error) {
	f.created = append(f.created, n)
	return &jobs.Job{ID: "j1"}, nil
}

func TestScheduleTickerQueuesDueSchedules(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "policies"), 0o755)
	if err := os.WriteFile(handlers.WebSchedulesPath(root), []byte("schedules:\n  - {name: hourly, experiment: experiments/n.yaml, cron: \"0 * * * *\", timezone: UTC}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fj := &fakeJobs{}
	start := time.Date(2026, 11, 21, 9, 59, 0, 0, time.UTC)
	tk := &scheduleTicker{root: root, org: "local", jobs: fj, last: start}
	if got := tk.tick(context.Background(), start.Add(30*time.Second)); len(got) != 0 {
		t.Fatalf("fired early: %v", got)
	}
	if got := tk.tick(context.Background(), start.Add(90*time.Second)); len(got) != 1 || got[0] != "hourly" {
		t.Fatalf("fired %v", got)
	}
	if len(fj.created) != 1 || fj.created[0].Kind != kindScheduleFire || fj.created[0].CreatedBy != "system" {
		t.Fatalf("created %+v", fj.created)
	}
	if got := tk.tick(context.Background(), start.Add(120*time.Second)); len(got) != 0 {
		t.Fatalf("fired twice: %v", got)
	}
}

func TestRegisterOrMergeJobKindJoinsModes(t *testing.T) {
	jobKindsMu.Lock()
	saved := jobKinds
	jobKinds = nil
	jobKindsMu.Unlock()
	defer func() { jobKindsMu.Lock(); jobKinds = saved; jobKindsMu.Unlock() }()
	ran := ""
	registerJobKind(jobKindSpec{Kind: "x.run", Hosted: true, Run: func(context.Context, jobEnv, *jobs.Job, jobs.Logger, func(any)) (any, error) {
		ran = "hosted"
		return nil, nil
	}})
	registerOrMergeJobKind(jobKindSpec{Kind: "x.run", Local: true, Run: func(context.Context, jobEnv, *jobs.Job, jobs.Logger, func(any)) (any, error) {
		ran = "local"
		return nil, nil
	}})
	if len(jobKinds) != 1 || !jobKinds[0].Local || !jobKinds[0].Hosted {
		t.Fatalf("kinds %+v", jobKinds)
	}
	_, _ = jobKinds[0].Run(context.Background(), jobEnv{Hosted: false}, nil, nil, nil)
	if ran != "local" {
		t.Fatal(ran)
	}
	_, _ = jobKinds[0].Run(context.Background(), jobEnv{Hosted: true}, nil, nil, nil)
	if ran != "hosted" {
		t.Fatal(ran)
	}
}
