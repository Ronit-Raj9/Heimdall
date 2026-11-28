package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/auth"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// captureJobs records whether Create saw the POST /jobs marker.
type captureJobs struct {
	JobsAPI
	fromAPI bool
}

func (c *captureJobs) Create(ctx context.Context, n jobs.NewJob) (*jobs.Job, error) {
	c.fromAPI = FromJobsAPI(ctx)
	return &jobs.Job{ID: "job_1", Kind: n.Kind}, nil
}

// TestJobsRetryMarksAPI: the Jobs page's Retry body posted to POST /jobs
// reaches the kind's validator marked, so experiment.run refuses a run
// without a cap there (cmd expRunCapProblem), never on the server's own
// queueing (N2).
func TestJobsRetryMarksAPI(t *testing.T) {
	api := &captureJobs{}
	h := NewJobsHandler(testMode, api, passThrough)
	defer h.Close()
	member := &auth.Principal{Kind: auth.KindUser, UserID: "usr_m", OrgID: "org_a", Role: auth.RoleMember}
	resp := jobsRequest(t, func(rt *router.Router) { h.RegisterRoutes(rt) }, member, "POST", "/api/bench/jobs", `{"kind":"experiment.run","params":{"exp_id":"e_1"}}`)
	if resp.StatusCode() != 202 || !api.fromAPI {
		t.Fatalf("%d %s marked=%v", resp.StatusCode(), resp.Body(), api.fromAPI)
	}
	if FromJobsAPI(context.Background()) {
		t.Fatal("a plain context is marked")
	}
}

// TestExperimentCapProblem: resume of a comparison with no cap, frozen or
// live, or with its cap used up, is refused with a plain sentence.
func TestExperimentCapProblem(t *testing.T) {
	m := &schemas.Manifest{}
	obs := func(v float64) schemas.Value { return schemas.Observed(v, schemas.EvidenceGateway) }
	if got := experimentCapProblem(m, 0, obs(0)); !strings.Contains(got, "no spend cap") {
		t.Fatalf("no cap: %q", got)
	}
	if got := experimentCapProblem(m, 0.5, obs(0.1)); got != "" {
		t.Fatalf("live cap left: %q", got)
	}
	m.Experiment.Budget.TotalUSD = 0.05
	if got := experimentCapProblem(m, 0, obs(0.05)); !strings.Contains(got, "used up") {
		t.Fatalf("used up: %q", got)
	}
	if got := experimentCapProblem(m, 0, obs(0.01)); got != "" {
		t.Fatalf("cap left: %q", got)
	}
}
