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

const w4b04OrgID = "org_01m7yyh2wft8r2x5tjyxthksbj"

// w4b04OrgJobs serves a failed job whose error and log line name the org id
// outside any path, a gateway slot built from it, and a server path outside
// the known roots.
type w4b04OrgJobs struct{ JobsAPI }

const w4b04OrgLine = "slot org_org01m7yyh2wft8r2x5tjyxthksbj_openai_20 refused benchmark hd-swe for " + w4b04OrgID + ": open /srv/heimdall/cache/hd-swe.lock: permission denied"

func (w4b04OrgJobs) Kinds() []string { return []string{"suite.check"} }

func (w4b04OrgJobs) job() jobs.Job {
	return jobs.Job{ID: "job_1", OrgID: w4b04OrgID, Kind: "suite.check", Status: jobs.StatusFailed, Error: w4b04OrgLine}
}

func (f w4b04OrgJobs) Get(context.Context, string, string) (*jobs.Job, error) {
	j := f.job()
	return &j, nil
}

func (f w4b04OrgJobs) List(context.Context, string, jobs.Filter) (*jobs.Page, error) {
	return &jobs.Page{Jobs: []jobs.Job{f.job()}}, nil
}

func (w4b04OrgJobs) Logs(_ context.Context, _, _ string, since int64, _ int) (*jobs.LogPage, error) {
	if since > 0 {
		return &jobs.LogPage{JobID: "job_1", Status: jobs.StatusFailed, Next: since}, nil
	}
	return &jobs.LogPage{JobID: "job_1", Status: jobs.StatusFailed, Lines: []jobs.LogLine{{Seq: 1, Line: w4b04OrgLine}}, Next: 1}, nil
}

// TestJobTextNamesNoOrgID: on a hosted server the job error and log lines
// (JSON and event stream) carry no org id, slot name or server path, keep
// the benchmark name, and the job's own org_id field (the caller's own org,
// which no page prints) is unchanged. Locally the text is served as stored
// (wave 4, F1.3, group B review).
func TestJobTextNamesNoOrgID(t *testing.T) {
	saved := plaintext.DefaultRoots
	t.Cleanup(func() { plaintext.DefaultRoots = saved })
	h := NewJobsHandler(testMode, w4b04OrgJobs{}, passThrough)
	defer h.Close()
	reg := func(rt *router.Router) { h.RegisterRoutes(rt) }
	texts := func(uri, accept string) (string, string) {
		var header []string
		if accept != "" {
			header = []string{"Accept", accept}
		}
		body := string(jobsRequest(t, reg, nil, "GET", uri, "", header...).Body())
		if accept != "" {
			return body, ""
		}
		var j struct {
			OrgID string `json:"org_id"`
			Error string `json:"error"`
			Lines []struct {
				Line string `json:"line"`
			} `json:"lines"`
		}
		if err := json.Unmarshal([]byte(body), &j); err != nil {
			t.Fatalf("%s: %v: %s", uri, err, body)
		}
		out := j.Error
		for _, l := range j.Lines {
			out += "\n" + l.Line
		}
		return out, j.OrgID
	}

	plaintext.DefaultRoots = func() []string { return []string{"/var/lib/heimdall/runs"} }
	for _, tc := range []struct{ uri, accept string }{
		{"/api/bench/jobs/job_1", ""},
		{"/api/bench/jobs/job_1/logs", ""},
		{"/api/bench/jobs/job_1/logs", "text/event-stream"},
	} {
		text, org := texts(tc.uri, tc.accept)
		if !strings.Contains(text, "hd-swe") || !strings.Contains(text, "permission denied") {
			t.Errorf("%s %s: lost what failed: %q", tc.uri, tc.accept, text)
		}
		for _, gone := range []string{"org_01m7", "org_org01m7", "/srv/"} {
			if strings.Contains(text, gone) {
				t.Errorf("%s %s: served %q: %q", tc.uri, tc.accept, gone, text)
			}
		}
		if tc.uri == "/api/bench/jobs/job_1" && org != w4b04OrgID {
			t.Errorf("org_id field = %q, want the job's own org", org)
		}
	}

	plaintext.DefaultRoots = nil
	if text, _ := texts("/api/bench/jobs/job_1", ""); text != w4b04OrgLine {
		t.Errorf("local mode changed the job error: %q", text)
	}
}
