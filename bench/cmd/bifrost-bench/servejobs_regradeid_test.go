package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/db/dbtest"
	"github.com/maximhq/bifrost/bench/jobs"
)

// TestRegradeJobRefusesStoreInvalidID: the job create refuses a re-score name
// the store would refuse later, so the job never fails 15 s after it starts.
func TestRegradeJobRefusesStoreInvalidID(t *testing.T) {
	a, _ := jobsProject(t)
	hub := newJobsHub(dbtest.Open(t), a.logger, false, true, "")
	hub.add(newLauncher(a, nil))
	ctx := context.Background()
	_, err := hub.runner.Create(ctx, jobs.NewJob{OrgID: "local", Kind: kindExperimentRegate, Params: json.RawMessage(`{"exp_id":"e_demo","id":"zz-test-rescore"}`)})
	if !errors.Is(err, jobs.ErrInvalidParams) {
		t.Fatalf("create with zz-test-rescore: %v, want ErrInvalidParams", err)
	}
	want := "re-score name zz-test-rescore: use lower-case letters, digits and _ only, for example zz_test_rescore"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error %q does not say %q", err, want)
	}
	j, err := hub.runner.Create(ctx, jobs.NewJob{OrgID: "local", Kind: kindExperimentRegate, Params: json.RawMessage(`{"exp_id":"e_demo","id":"zz_test_rescore"}`)})
	if err != nil || j == nil {
		t.Fatalf("create with zz_test_rescore: %v", err)
	}
}
