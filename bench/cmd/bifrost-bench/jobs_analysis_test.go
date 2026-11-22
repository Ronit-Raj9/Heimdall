package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/jobs"
)

// lineLog is a jobs.Logger that keeps the lines.
type lineLog struct{ lines []string }

func (l *lineLog) Printf(f string, a ...any) { l.lines = append(l.lines, fmt.Sprintf(f, a...)) }
func (l *lineLog) Warnf(f string, a ...any)  { l.Printf(f, a...) }
func (l *lineLog) Errorf(f string, a ...any) { l.Printf(f, a...) }
func (l *lineLog) Write(p []byte) (int, error) {
	l.lines = append(l.lines, string(p))
	return len(p), nil
}

func TestAnalysisJobKindsValidate(t *testing.T) {
	a, _ := jobsProject(t)
	hub := newJobsHub(dbtest.Open(t), a.logger, false, true, "")
	l := newLauncher(a, nil)
	hub.add(l)
	ctx := context.Background()
	for _, tc := range []struct {
		kind, params string
		want         error
		target       string
	}{
		{kindCheckRun, `{"exp_id":"e_demo"}`, nil, "analysis:e_demo"},
		{kindCheckRun, `{"exp_id":"e_demo","grade_source":"reviewed"}`, nil, "analysis:e_demo"},
		{kindCheckRun, `{"exp_id":"e_demo","run_id":"r_1"}`, jobs.ErrInvalidParams, ""},
		{kindCheckRun, `{"exp_id":"e_nope"}`, jobs.ErrNotFound, ""},
		{kindCheckRun, `{}`, jobs.ErrInvalidParams, ""},
		{kindCodeScanRun, `{"exp_id":"e_demo","run_id":"r_1"}`, nil, ""},
		{kindCodeScanRun, `{"exp_id":"e_demo","run_id":"../r"}`, jobs.ErrInvalidParams, ""},
		{kindCodeScanRun, `{"exp_id":"e_demo","grade_source":"x"}`, jobs.ErrInvalidParams, ""},
		{kindAwarenessRun, `{"exp_id":"e_demo"}`, nil, ""},
		{kindAwarenessRun, `{"exp_id":"../e_demo"}`, jobs.ErrInvalidParams, ""},
		{kindBlameRun, `{"exp_id":"e_demo"}`, nil, "analysis:e_demo"},
		{kindBlameRun, `{"exp_id":"e_demo","bogus":1}`, jobs.ErrInvalidParams, ""},
		{kindClustersRebuild, `{}`, nil, "index"},
		{kindClustersRebuild, `{"exp_id":"e_demo"}`, jobs.ErrInvalidParams, ""},
	} {
		j, err := hub.runner.Create(ctx, jobs.NewJob{OrgID: "local", Kind: tc.kind, Params: json.RawMessage(tc.params)})
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Fatalf("%s %s: %v, want %v", tc.kind, tc.params, err, tc.want)
		}
		if j == nil {
			continue
		}
		if j.Target != tc.target {
			t.Fatalf("%s target %q, want %q", tc.kind, j.Target, tc.target)
		}
		if j.Target != "" {
			if _, err := hub.runner.Cancel(ctx, "local", j.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Every analysis kind reads run folders only, so hosted serves them too.
	hosted := newJobsHub(dbtest.Open(t), a.logger, true, true, "")
	for _, k := range []string{kindCheckRun, kindCodeScanRun, kindAwarenessRun, kindBlameRun, kindClustersRebuild} {
		if !slices.Contains(hosted.runner.Kinds(), k) || !slices.Contains(hub.runner.Kinds(), k) {
			t.Fatalf("kind %s missing: local %v hosted %v", k, hub.runner.Kinds(), hosted.runner.Kinds())
		}
	}
}

func TestAnalysisJobsRunOnAnExperimentWithoutRuns(t *testing.T) {
	a, _ := jobsProject(t)
	l := newLauncher(a, nil)
	ctx := context.Background()
	log := &lineLog{}
	p := analysisParams{ExpID: "e_demo"}
	cs, err := codeScanRunJob(ctx, l, p, log)
	if err != nil {
		t.Fatal(err)
	}
	if r := cs.(codeScanRunResult); r.Runs != 0 || r.Vetoed == nil {
		t.Fatalf("codescan %+v", r)
	}
	if _, err := codeScanRunJob(ctx, l, analysisParams{ExpID: "e_demo", RunID: "r_missing"}, log); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("codescan of a missing run: %v", err)
	}
	b, err := blameRunJob(ctx, l, p, log)
	if err != nil {
		t.Fatal(err)
	}
	if r := b.(blameRunResult); r.Runs != 0 {
		t.Fatalf("blame %+v", r)
	}
	if _, err := clustersRebuildJob(ctx, l, log); err != nil {
		t.Fatal(err)
	}
	if _, err := awarenessRunJob(ctx, l, p, log); err != nil {
		t.Fatal(err)
	}
	if len(log.lines) == 0 {
		t.Fatal("no log lines")
	}
}
