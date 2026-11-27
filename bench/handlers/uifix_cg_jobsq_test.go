package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/jobs"
)

type cgQJobs struct {
	JobsAPI
	got jobs.Filter
	org string
}

func (f *cgQJobs) Kinds() []string { return []string{"echo"} }

func (f *cgQJobs) List(_ context.Context, org string, flt jobs.Filter) (*jobs.Page, error) {
	f.got, f.org = flt, org
	return &jobs.Page{Jobs: []jobs.Job{}}, nil
}

// TestJobsListQ: GET /jobs passes q to the job runner for the caller's
// organization (the store matches it, TestListQ); a q over 200 bytes is 400.
func TestJobsListQ(t *testing.T) {
	f := &cgQJobs{}
	h := NewJobsHandler(testMode, f, passThrough)
	defer h.Close()
	r := router.New()
	h.RegisterRoutes(r)
	if code, body := abCall(r, "GET", APIPrefix+"/jobs?q=suite", nil); code != 200 || f.got.Q != "suite" || f.org == "" {
		t.Fatalf("q: %d %s %+v", code, body, f.got)
	}
	if code, _ := abCall(r, "GET", APIPrefix+"/jobs?q="+strings.Repeat("x", 201), nil); code != 400 {
		t.Fatalf("long q = %d", code)
	}
}
