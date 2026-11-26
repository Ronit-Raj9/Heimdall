package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/plaintext"
)

const w4b04RawPath = "/var/lib/heimdall/runs/orgs/org_01m7yyh2wft8r2x5tjyxthksbj/.index/results.jsonl"

// w4b04Jobs serves one failed job whose error and log name a server path.
type w4b04Jobs struct{ JobsAPI }

func (w4b04Jobs) Kinds() []string { return []string{"index.rebuild"} }

func (w4b04Jobs) job() jobs.Job {
	return jobs.Job{ID: "job_1", Kind: "index.rebuild", Status: jobs.StatusFailed, Error: "results index (jsonl) " + w4b04RawPath + ": permission denied"}
}

func (f w4b04Jobs) Get(context.Context, string, string) (*jobs.Job, error) {
	j := f.job()
	return &j, nil
}

func (f w4b04Jobs) List(context.Context, string, jobs.Filter) (*jobs.Page, error) {
	return &jobs.Page{Jobs: []jobs.Job{f.job()}}, nil
}

func (w4b04Jobs) Logs(_ context.Context, _, _ string, since int64, _ int) (*jobs.LogPage, error) {
	if since > 0 {
		return &jobs.LogPage{JobID: "job_1", Status: jobs.StatusFailed, Next: since}, nil
	}
	return &jobs.LogPage{JobID: "job_1", Status: jobs.StatusFailed, Lines: []jobs.LogLine{{Seq: 1, Line: "results index (jsonl) " + w4b04RawPath}}, Next: 1}, nil
}

// TestJobsServedTextCleaned checks that GET /jobs, GET /jobs/{id} and the
// job log (JSON and event stream) send no server path or organization id on a
// hosted server, while the stored job keeps its text (wave 4, F1.3).
func TestJobsServedTextCleaned(t *testing.T) {
	saved := plaintext.DefaultRoots
	t.Cleanup(func() { plaintext.DefaultRoots = saved })
	plaintext.DefaultRoots = func() []string { return []string{"/var/lib/heimdall/runs"} }
	api := w4b04Jobs{}
	h := NewJobsHandler(testMode, api, passThrough)
	defer h.Close()
	reg := func(rt *router.Router) { h.RegisterRoutes(rt) }
	for _, tc := range []struct{ name, uri, accept string }{
		{"list", "/api/bench/jobs", ""},
		{"get", "/api/bench/jobs/job_1", ""},
		{"logs json", "/api/bench/jobs/job_1/logs", ""},
		{"logs stream", "/api/bench/jobs/job_1/logs", "text/event-stream"},
	} {
		var header []string
		if tc.accept != "" {
			header = []string{"Accept", tc.accept}
		}
		resp := jobsRequest(t, reg, nil, "GET", tc.uri, "", header...)
		body := string(resp.Body())
		if !strings.Contains(body, "results index") {
			t.Errorf("%s: body lost the text: %s", tc.name, body)
		}
		if strings.Contains(body, "/var/") || strings.Contains(body, "org_01m7") {
			t.Errorf("%s: body names the server: %s", tc.name, body)
		}
	}
	if j := api.job(); !strings.Contains(j.Error, w4b04RawPath) {
		t.Fatalf("stored job changed: %q", j.Error)
	}
}
