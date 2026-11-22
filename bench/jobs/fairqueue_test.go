package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/db/dbtest"
)

// claimOrder claims every queued job one at a time and returns their ids in
// claim order. Claimed jobs stay running (nothing releases them), so the
// per-org running counts build up as a real runner's would.
func claimOrder(t *testing.T, r *Runner, n int) []string {
	t.Helper()
	var out []string
	for range n {
		r.clearIdle()
		j, err := r.claim(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if j == nil {
			break
		}
		out = append(out, j.OrgID+"/"+nParam(j))
	}
	return out
}

// nParam is the "n" param of a test job.
func nParam(j *Job) string {
	var p struct {
		N string `json:"n"`
	}
	_ = j.DecodeParams(&p)
	return p.N
}

func enqueueN(t *testing.T, r *Runner, org, kind string, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := r.Enqueue(context.Background(), NewJob{OrgID: org, Kind: kind, Params: []byte(`{"n":"` + n + `"}`)}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // distinct created_at
	}
}

func TestClaimIsRoundRobinAcrossOrgs(t *testing.T) {
	d := dbtest.Open(t)
	r := New(d, fastOptions("a"))
	r.Register("exp", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	// org_big queues five experiments before org_late queues two.
	enqueueN(t, r, "org_big", "exp", "b1", "b2", "b3", "b4", "b5")
	enqueueN(t, r, "org_late", "exp", "l1", "l2")
	got := claimOrder(t, r, 7)
	want := []string{"org_big/b1", "org_late/l1", "org_big/b2", "org_late/l2", "org_big/b3", "org_big/b4", "org_big/b5"}
	if len(got) != len(want) {
		t.Fatalf("claimed %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("claim order %v, want %v", got, want)
		}
	}
}

func TestPriorityOrdersWithinEqualLoad(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Open(t)
	r := New(d, fastOptions("a"))
	r.Register("exp", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	enqueueN(t, r, "org_a", "exp", "a1", "a2", "a3")
	page, err := r.List(ctx, "org_a", Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var a3 string
	for _, j := range page.Jobs {
		if nParam(&j) == "a3" {
			a3 = j.ID
		}
	}
	j, err := r.SetPriority(ctx, "org_a", a3, 99)
	if err != nil || j.Priority != MaxPriority {
		t.Fatalf("set priority: %+v %v", j, err)
	}
	got := claimOrder(t, r, 3)
	if len(got) != 3 || got[0] != "org_a/a3" || got[1] != "org_a/a1" {
		t.Fatalf("claim order %v: the bumped job goes first, then age", got)
	}
	if _, err := r.SetPriority(ctx, "org_a", a3, 1); !errors.Is(err, ErrNotQueued) {
		t.Fatalf("running job: %v", err)
	}
	if _, err := r.SetPriority(ctx, "org_a", "job_missing", 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing job: %v", err)
	}
	if _, err := r.SetPriority(ctx, "org_other", a3, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org's job: %v", err)
	}
	// Priority does not beat the round robin: a less loaded org goes first.
	enqueueN(t, r, "org_b", "exp", "b1")
	enqueueN(t, r, "org_a", "exp", "a4")
	page, _ = r.List(ctx, "org_a", Filter{})
	for _, j := range page.Jobs {
		if nParam(&j) == "a4" {
			if _, err := r.SetPriority(ctx, "org_a", j.ID, MaxPriority); err != nil {
				t.Fatal(err)
			}
		}
	}
	if got := claimOrder(t, r, 1); len(got) != 1 || got[0] != "org_b/b1" {
		t.Fatalf("claim %v, want org_b/b1 (org_a already runs 3)", got)
	}
}

func TestIdleClaimIsSkippedUntilPoked(t *testing.T) {
	ctx := context.Background()
	d := dbtest.Open(t)
	opts := fastOptions("a")
	opts.PollEvery = time.Hour
	r := New(d, opts)
	r.Register("exp", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	if j, err := r.claim(ctx); err != nil || j != nil {
		t.Fatalf("empty queue: %+v %v", j, err)
	}
	if !r.queueIdle() {
		t.Fatal("an empty claim should mark the queue idle")
	}
	// A job inserted behind the runner's back waits for the next poll.
	if err := r.insert(ctx, &Job{ID: "job_side", OrgID: "org_a", Kind: "exp", Params: []byte(`{}`), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if j, _ := r.claim(ctx); j != nil {
		t.Fatal("claimed while idle")
	}
	// Enqueue pokes: the next claim queries again.
	enqueueN(t, r, "org_a", "exp", "x")
	if j, err := r.claim(ctx); err != nil || j == nil {
		t.Fatalf("after enqueue: %+v %v", j, err)
	}
}
