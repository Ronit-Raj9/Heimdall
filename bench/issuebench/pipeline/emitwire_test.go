package pipeline

import (
	"context"
	"testing"

	"github.com/maximhq/bifrost/bench/jobs"
)

type fakeJobs struct{ got []jobs.NewJob }

func (f *fakeJobs) Create(ctx context.Context, n jobs.NewJob) (*jobs.Job, error) {
	f.got = append(f.got, n)
	return &jobs.Job{ID: n.Kind}, nil
}

func TestQueueAdmit(t *testing.T) {
	f := &fakeJobs{}
	ids, err := QueueAdmit(context.Background(), JobQueuer{Jobs: f, OrgID: "o1"}, "issues-a-b")
	if err != nil || len(ids) != 3 || f.got[2].Kind != "suite.admit" || string(f.got[0].Params) != `{"suite":"issues-a-b"}` || f.got[0].OrgID != "o1" {
		t.Fatalf("%v %v %+v", err, ids, f.got)
	}
}
