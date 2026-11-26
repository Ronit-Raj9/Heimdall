package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/plaintext"
)

// w4b04ProgressJobs serves a running comparison job whose progress carries
// the last live event and the last error, both naming a gateway slot and a
// server path.
type w4b04ProgressJobs struct{ JobsAPI }

const w4b04ProgressText = "slot org_org01m7yyh2wft8r2x5tjyxthksbj_openai_20: open /var/lib/heimdall/runs/orgs/org_01m7yyh2wft8r2x5tjyxthksbj/experiments/e1.yaml: no such file or directory"

func (w4b04ProgressJobs) Kinds() []string { return []string{"experiment.run"} }

func (w4b04ProgressJobs) job() jobs.Job {
	progress, _ := json.Marshal(map[string]any{
		"exp_id": "e1", "errors": 1, "last_error": w4b04ProgressText,
		"last_event": map[string]any{"kind": "error", "slot_id": "A-t1-0", "message": w4b04ProgressText},
	})
	return jobs.Job{ID: "job_1", Kind: "experiment.run", Status: jobs.StatusRunning, Progress: progress}
}

func (f w4b04ProgressJobs) Get(context.Context, string, string) (*jobs.Job, error) {
	j := f.job()
	return &j, nil
}

func (f w4b04ProgressJobs) List(context.Context, string, jobs.Filter) (*jobs.Page, error) {
	return &jobs.Page{Jobs: []jobs.Job{f.job()}}, nil
}

// TestJobProgressServedPlain: on a hosted server a job's progress (the last
// error and the last live event a comparison page shows) names no slot,
// org id or server path; the counts and the experiment id stay; locally the
// progress is served as stored (wave 4, F1.3, group B review).
func TestJobProgressServedPlain(t *testing.T) {
	saved := plaintext.DefaultRoots
	t.Cleanup(func() { plaintext.DefaultRoots = saved })
	h := NewJobsHandler(testMode, w4b04ProgressJobs{}, passThrough)
	defer h.Close()
	reg := func(rt *router.Router) { h.RegisterRoutes(rt) }

	plaintext.DefaultRoots = func() []string { return []string{"/var/lib/heimdall/runs"} }
	for _, uri := range []string{"/api/bench/jobs", "/api/bench/jobs/job_1"} {
		body := string(jobsRequest(t, reg, nil, "GET", uri, "").Body())
		for _, gone := range []string{"org_01m7", "org_org01m7", "/var/"} {
			if strings.Contains(body, gone) {
				t.Errorf("%s served %q: %s", uri, gone, body)
			}
		}
		for _, keep := range []string{`"exp_id":"e1"`, `"errors":1`, "experiments/e1.yaml does not exist", "provider slot openai 20"} {
			if !strings.Contains(body, keep) {
				t.Errorf("%s lost %q: %s", uri, keep, body)
			}
		}
	}

	plaintext.DefaultRoots = nil
	if body := string(jobsRequest(t, reg, nil, "GET", "/api/bench/jobs/job_1", "").Body()); !strings.Contains(body, "/var/lib/heimdall/runs/orgs/") {
		t.Errorf("local mode changed the progress: %s", body)
	}
}
