package pipeline

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/jobs"
)

type fakeJobs struct {
	mu   sync.Mutex
	got  []jobs.NewJob
	busy int // Creates of a targeted kind (lock, admit) refused with ErrConflict
}

func (f *fakeJobs) Create(ctx context.Context, n jobs.NewJob) (*jobs.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if n.Kind != "suite.lint" && f.busy > 0 {
		f.busy--
		return nil, fmt.Errorf("%w: %s suite:x", jobs.ErrConflict, n.Kind)
	}
	f.got = append(f.got, n)
	return &jobs.Job{ID: n.Kind}, nil
}

// suite.admit shares suite:<id> with suite.lock, so it always met the
// queued lock and the accept answered 502 (first real hosted run). A busy
// target is queued in the background, in order, once it is free.
func TestQueueAdmitWaitsForBusyTarget(t *testing.T) {
	old := AdmitRetry
	AdmitRetry = time.Millisecond
	defer func() { AdmitRetry = old }()
	f := &fakeJobs{busy: 3}
	ids, err := QueueAdmit(context.Background(), JobQueuer{Jobs: f, OrgID: "o1"}, "issues-a-b")
	if err != nil || len(ids) != 1 {
		t.Fatalf("%v %v", err, ids)
	}
	for i := 0; i < 1000; i++ {
		f.mu.Lock()
		n := len(f.got)
		f.mu.Unlock()
		if n == 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.got) != 3 || f.got[1].Kind != "suite.lock" || f.got[2].Kind != "suite.admit" {
		t.Fatalf("queued %+v", f.got)
	}
}

func TestQueueAdmit(t *testing.T) {
	f := &fakeJobs{}
	ids, err := QueueAdmit(context.Background(), JobQueuer{Jobs: f, OrgID: "o1"}, "issues-a-b")
	if err != nil || len(ids) != 3 || f.got[2].Kind != "suite.admit" || string(f.got[0].Params) != `{"suite":"issues-a-b"}` || f.got[0].OrgID != "o1" {
		t.Fatalf("%v %v %+v", err, ids, f.got)
	}
}
