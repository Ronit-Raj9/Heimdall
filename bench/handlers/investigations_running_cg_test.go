package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
)

// cgInvJobs holds jobs of two organizations and filters List like the
// runner: by org, kind and status.
type cgInvJobs struct {
	JobsAPI
	all  []jobs.Job
	orgs []string // the orgs List was asked for
}

func (f *cgInvJobs) List(_ context.Context, org string, flt jobs.Filter) (*jobs.Page, error) {
	f.orgs = append(f.orgs, org)
	var out []jobs.Job
	for _, j := range f.all {
		if j.OrgID == org && j.Kind == flt.Kind && (flt.Status == "" || j.Status == flt.Status) {
			out = append(out, j)
		}
	}
	return &jobs.Page{Jobs: out}, nil
}

// TestInvestigationsRunning: GET /investigations lists the organization's
// queued and running investigation jobs (fix wave M7-31), filtered by exp,
// and never a job of another organization.
func TestInvestigationsRunning(t *testing.T) {
	at := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	f := &cgInvJobs{all: []jobs.Job{
		{ID: "job_run", OrgID: "org_a", Kind: investigateJobKind, Status: jobs.StatusRunning, Params: json.RawMessage(`{"exp_id":"e1"}`), CreatedAt: at},
		{ID: "job_q", OrgID: "org_a", Kind: investigateJobKind, Status: jobs.StatusQueued, Params: json.RawMessage(`{"exp_id":"e2"}`), CreatedAt: at},
		{ID: "job_done", OrgID: "org_a", Kind: investigateJobKind, Status: jobs.StatusSucceeded, Params: json.RawMessage(`{"exp_id":"e1"}`), CreatedAt: at},
		{ID: "job_other_kind", OrgID: "org_a", Kind: "catalog.import", Status: jobs.StatusRunning, CreatedAt: at},
		{ID: "job_b", OrgID: "org_b", Kind: investigateJobKind, Status: jobs.StatusRunning, Params: json.RawMessage(`{"exp_id":"e1"}`), CreatedAt: at},
	}}
	h := &investigationsHandler{mode: schemas.Mode("live"), root: t.TempDir(), jobs: f, org: "org_a"}
	r := router.New()
	r.GET(APIPrefix+"/investigations", h.list)

	code, body := abCall(r, "GET", APIPrefix+"/investigations", nil)
	var list InvestigationList
	if code != 200 || json.Unmarshal(body, &list) != nil {
		t.Fatalf("list: %d %s", code, body)
	}
	if len(list.Running) != 2 || list.Running[0].ID != "job_run" || list.Running[0].Status != "running" || list.Running[0].ExpID != "e1" || list.Running[1].ID != "job_q" || list.Running[1].Status != "queued" {
		t.Fatalf("running %+v", list.Running)
	}
	for _, o := range f.orgs {
		if o != "org_a" {
			t.Fatalf("jobs read for org %q", o)
		}
	}
	code, body = abCall(r, "GET", APIPrefix+"/investigations?exp=e1", nil)
	list = InvestigationList{}
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list.Running) != 1 || list.Running[0].ID != "job_run" {
		t.Fatalf("exp list: %d %s", code, body)
	}

	// Organization B sees only its own job.
	hb := &investigationsHandler{mode: schemas.Mode("live"), root: t.TempDir(), jobs: f, org: "org_b"}
	rb := router.New()
	rb.GET(APIPrefix+"/investigations", hb.list)
	code, body = abCall(rb, "GET", APIPrefix+"/investigations", nil)
	list = InvestigationList{}
	if code != 200 || json.Unmarshal(body, &list) != nil || len(list.Running) != 1 || list.Running[0].ID != "job_b" {
		t.Fatalf("org b list: %d %s", code, body)
	}

	// No job runner, or no org: no running field at all.
	hn := &investigationsHandler{mode: schemas.Mode("live"), root: t.TempDir()}
	rn := router.New()
	rn.GET(APIPrefix+"/investigations", hn.list)
	if _, body := abCall(rn, "GET", APIPrefix+"/investigations", nil); string(body) == "" || strings.Contains(string(body), `"running"`) {
		t.Fatalf("no runner list: %s", body)
	}
}
