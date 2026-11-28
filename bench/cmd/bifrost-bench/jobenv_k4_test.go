package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/traceinbox"
	"os"
	"path/filepath"
)

// K4: the job env is built the way serve builds an org: orgDeps, the org's
// launcher in the hub, then applyServiceHooks. Before K4 jobEnvFor ran the
// hooks over an empty Services, so every job that reads runs failed with
// "this server cannot read runs" or "this server has no run index".
func TestJobEnvHasTheOrgsRunIndex(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		a, _ := jobsProject(t)
		ctx := context.Background()
		deps, ix, err := orgDeps(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		hub := newJobsHub(dbtest.Open(t), a.logger, hosted, true, "")
		l := newLauncher(a, ix)
		hub.add(l)
		deps.Launcher = l
		applyServiceHooks(a, &deps, "local", hosted)
		env := hub.jobEnvFor(l)
		s := env.Services
		if s.Index == nil || s.Suites == nil || s.Agents == nil || s.Launcher == nil {
			t.Fatalf("hosted=%v: job services miss the request path's: index %v suites %v agents %v", hosted, s.Index != nil, s.Suites != nil, s.Agents != nil)
		}
		if _, ok := s.Index.Manifest("e_demo"); !ok {
			t.Fatalf("hosted=%v: the job index does not see the org's comparison", hosted)
		}
		// validity.scan reads every comparison.
		if _, err := runValidityScanJob(ctx, env, &jobs.Job{ID: "j1"}, discardLogger{}, nil); err != nil {
			t.Fatalf("hosted=%v: validity scan: %v", hosted, err)
		}
		// findings.investigate with the web body (webInvestigateBody shape)
		// reads the comparison's runs; e_demo has no failed run.
		body := json.RawMessage(strings.Replace(webInvestigateBody, "exp1", "e_demo", 1))
		if _, _, err := validateFindingsInvestigate(ctx, env, body); err != nil {
			t.Fatalf("hosted=%v: the web body is refused: %v", hosted, err)
		}
		_, err = runFindingsInvestigate(ctx, env, &jobs.Job{ID: "j2", Params: body}, discardLogger{}, nil)
		if err == nil || strings.Contains(err.Error(), "cannot read runs") {
			t.Fatalf("hosted=%v: investigate: %v", hosted, err)
		}
		if _, _, err := validateFindingsInvestigate(ctx, env, json.RawMessage(webInvestigateBody)); err == nil {
			t.Fatalf("hosted=%v: an unknown comparison is accepted", hosted)
		}
	}
}

// A launcher whose deps were never bound still reads its own runs.
func TestJobEnvFallsBackToTheLaunchersIndex(t *testing.T) {
	a, _ := jobsProject(t)
	_, ix, err := orgDeps(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	hub := newJobsHub(dbtest.Open(t), a.logger, true, true, "")
	l := newLauncher(a, ix)
	hub.add(l)
	if env := hub.jobEnvFor(l); env.Services.Index == nil {
		t.Fatal("no run index")
	}
}

// The Draft with a model body (traceDraftJob, components/inbox/proposalBar.tsx)
// created through the hub the way POST /jobs does.
func TestTraceDraftWebBodyCreatesAJob(t *testing.T) {
	for _, hosted := range []bool{false, true} {
		a, root := jobsProject(t)
		if err := os.MkdirAll(filepath.Join(root, traceinbox.Dir, traceinbox.ItemsDir, "inc-0123456789ab"), 0o755); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		deps, ix, err := orgDeps(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		hub := newJobsHub(dbtest.Open(t), a.logger, hosted, true, "")
		l := newLauncher(a, ix)
		hub.add(l)
		deps.Launcher = l
		applyServiceHooks(a, &deps, "local", hosted)
		body := `{"item":"inc-0123456789ab","suite":"demo","kind":"qa","provider":"openai","model":"gpt-5.4-mini","budget_usd":0.3,"yes":true}`
		j, err := hub.runner.Create(ctx, jobs.NewJob{OrgID: "local", Kind: jobTraceDraft, Params: json.RawMessage(body)})
		if err != nil || j == nil {
			t.Fatalf("hosted=%v: %v", hosted, err)
		}
	}
}
