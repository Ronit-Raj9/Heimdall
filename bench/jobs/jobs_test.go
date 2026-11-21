package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/db"
	"github.com/maximhq/bifrost/bench/db/dbtest"
)

func fastOptions(owner string) Options {
	return Options{Owner: owner, Workers: 3, LeaseFor: 300 * time.Millisecond, HeartbeatEvery: 30 * time.Millisecond,
		PollEvery: 20 * time.Millisecond, ProgressEvery: 20 * time.Millisecond, ShutdownGrace: 5 * time.Second}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func status(t *testing.T, r *Runner, org, id string) *Job {
	t.Helper()
	j, err := r.Get(context.Background(), org, id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func TestCreateValidateListAndOrgScope(t *testing.T) {
	d := dbtest.Open(t)
	r := New(d, fastOptions("a"))
	r.Register("echo", func(ctx context.Context, j *Job, log Logger, progress func(any)) (any, error) { return nil, nil },
		WithValidator(func(_ context.Context, org string, raw json.RawMessage) (any, string, error) {
			var p struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(raw, &p); err != nil || p.Name == "" {
				return nil, "", fmt.Errorf("name is required")
			}
			return p, "name:" + p.Name, nil
		}))
	r.Register("internal.thing", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil }, Internal())
	ctx := context.Background()

	if _, err := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "nope"}); !errors.Is(err, ErrUnknownKind) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "internal.thing"}); !errors.Is(err, ErrNotCreatable) {
		t.Fatalf("internal kind through Create: %v", err)
	}
	if _, err := r.Enqueue(ctx, NewJob{OrgID: "org_a", Kind: "internal.thing"}); err != nil {
		t.Fatalf("internal kind through Enqueue: %v", err)
	}
	if _, err := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "echo", Params: json.RawMessage(`{}`)}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("missing name: %v", err)
	}
	if _, err := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "echo", Params: json.RawMessage(`{"name":`)}); !errors.Is(err, ErrInvalidParams) {
		t.Fatalf("bad JSON: %v", err)
	}
	j, err := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "echo", Params: json.RawMessage(`{"name":"x","extra":1}`), CreatedBy: "usr_1"})
	if err != nil {
		t.Fatal(err)
	}
	if j.Status != StatusQueued || j.Target != "name:x" || string(j.Params) != `{"name":"x"}` || j.CreatedBy != "usr_1" || !strings.HasPrefix(j.ID, "job_") {
		t.Fatalf("created job: %+v (params %s)", j, j.Params)
	}
	if _, err := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "echo", Params: json.RawMessage(`{"name":"x"}`)}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second active job on one target: %v", err)
	}
	if _, err := r.Create(ctx, NewJob{OrgID: "org_b", Kind: "echo", Params: json.RawMessage(`{"name":"x"}`)}); err != nil {
		t.Fatalf("same target in another org: %v", err)
	}
	if _, err := r.Get(ctx, "org_b", j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("org_b reads org_a's job: %v", err)
	}
	if _, err := r.Cancel(ctx, "org_b", j.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("org_b cancels org_a's job: %v", err)
	}
	if _, err := r.Logs(ctx, "org_b", j.ID, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("org_b reads org_a's logs: %v", err)
	}
	page, err := r.List(ctx, "org_a", Filter{})
	if err != nil || len(page.Jobs) != 2 {
		t.Fatalf("org_a list: %v %+v", err, page)
	}
	page, err = r.List(ctx, "org_a", Filter{Kind: "echo"})
	if err != nil || len(page.Jobs) != 1 || page.Jobs[0].ID != j.ID {
		t.Fatalf("kind filter: %v %+v", err, page)
	}
	page, err = r.List(ctx, "org_a", Filter{Limit: 1})
	if err != nil || len(page.Jobs) != 1 || page.Next == "" {
		t.Fatalf("page 1: %v %+v", err, page)
	}
	page2, err := r.List(ctx, "org_a", Filter{Limit: 1, Before: page.Next})
	if err != nil || len(page2.Jobs) != 1 || page2.Next != "" || page2.Jobs[0].ID == page.Jobs[0].ID {
		t.Fatalf("page 2: %v %+v", err, page2)
	}
	// A queued job is cancelled at once and frees its target.
	c, err := r.Cancel(ctx, "org_a", j.ID)
	if err != nil || c.Status != StatusCancelled || !c.CancelRequested || c.FinishedAt == nil {
		t.Fatalf("cancel queued: %v %+v", err, c)
	}
	if _, err := r.Cancel(ctx, "org_a", j.ID); !errors.Is(err, ErrFinished) {
		t.Fatalf("cancel twice: %v", err)
	}
	if _, err := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "echo", Params: json.RawMessage(`{"name":"x"}`)}); err != nil {
		t.Fatalf("target free after cancel: %v", err)
	}
	if got := r.Kinds(); len(got) != 1 || got[0] != "echo" {
		t.Fatalf("creatable kinds %v", got)
	}
}

// Two runners on one database race for many jobs: each job runs once.
func TestClaimExclusiveUnderConcurrency(t *testing.T) {
	d := dbtest.Open(t)
	var mu sync.Mutex
	runs := map[string]int{}
	h := func(ctx context.Context, j *Job, log Logger, progress func(any)) (any, error) {
		mu.Lock()
		runs[j.ID]++
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		return map[string]string{"by": j.LeaseOwner}, nil
	}
	ra, rb := New(d, fastOptions("a")), New(d, fastOptions("b"))
	ra.Register("work", h)
	rb.Register("work", h)
	ctx := context.Background()
	const n = 40
	var ids []string
	for i := range n {
		j, err := ra.Enqueue(ctx, NewJob{OrgID: "org_a", Kind: "work", Params: json.RawMessage(fmt.Sprintf(`{"i":%d}`, i))})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, j.ID)
	}
	if err := ra.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rb.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer ra.Stop(ctx)
	defer rb.Stop(ctx)
	waitFor(t, "every job to succeed", func() bool {
		page, err := ra.List(ctx, "org_a", Filter{Status: StatusSucceeded, Limit: 500})
		return err == nil && len(page.Jobs) == n
	})
	mu.Lock()
	defer mu.Unlock()
	for _, id := range ids {
		if runs[id] != 1 {
			t.Fatalf("job %s ran %d times", id, runs[id])
		}
		j := status(t, ra, "org_a", id)
		if j.Attempt != 1 || j.LeaseOwner != "" || j.Result == nil {
			t.Fatalf("finished job %+v", j)
		}
	}
}

// A runner that dies holding a job: its lease expires, another runner
// requeues and resumes it (Attempt 2), and past the maximum it fails.
func TestLeaseExpiryRequeueAndMaxAttempts(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	dead := New(d, fastOptions("dead"))
	dead.Register("resumable", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil }, WithMaxAttempts(2))
	j, err := dead.Enqueue(ctx, NewJob{OrgID: "org_a", Kind: "resumable"})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := dead.claim(ctx) // claimed, then the process "dies": no heartbeat, no handler
	if err != nil || claimed == nil || claimed.ID != j.ID || claimed.Attempt != 1 || claimed.LeaseOwner != "dead" {
		t.Fatalf("claim: %v %+v", err, claimed)
	}
	// The lease is still fresh: nothing to recover.
	if res, err := dead.Recover(ctx); err != nil || res.Requeued != 0 {
		t.Fatalf("recover fresh lease: %v %+v", err, res)
	}
	expire(t, d, j.ID)

	var resumed atomic.Bool
	alive := New(d, fastOptions("alive"))
	alive.Register("resumable", func(ctx context.Context, j *Job, log Logger, progress func(any)) (any, error) {
		resumed.Store(j.Resume())
		log.Printf("resumed at attempt %d", j.Attempt)
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}, WithMaxAttempts(2))
	if err := alive.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "requeued job to run again", func() bool {
		x := status(t, alive, "org_a", j.ID)
		return x.Status == StatusRunning && x.LeaseOwner == "alive"
	})
	x := status(t, alive, "org_a", j.ID)
	if x.Attempt != 2 {
		t.Fatalf("attempt after requeue %d", x.Attempt)
	}
	alive.Stop(ctx) // graceful: handler cancelled with ErrShutdown, job queued again
	waitFor(t, "requeue on shutdown", func() bool { return status(t, alive, "org_a", j.ID).Status == StatusQueued })
	if !resumed.Load() {
		t.Fatal("handler was not told it resumed")
	}
	// Attempt 3 > max 2: a claim then lease expiry fails the job.
	again, err := dead.claim(ctx)
	if err != nil || again == nil || again.Attempt != 3 {
		t.Fatalf("third claim: %v %+v", err, again)
	}
	expire(t, d, j.ID)
	res, err := dead.Recover(ctx)
	if err != nil || res.Failed != 1 {
		t.Fatalf("recover past max: %v %+v", err, res)
	}
	x = status(t, dead, "org_a", j.ID)
	if x.Status != StatusFailed || !strings.Contains(x.Error, "lease expired") || x.FinishedAt == nil {
		t.Fatalf("job past max attempts: %+v", x)
	}
	logs, err := dead.Logs(ctx, "org_a", j.ID, 0, 0)
	if err != nil || len(logs.Lines) == 0 || !strings.Contains(joinLines(logs), "lease expired on attempt 1: queued again") {
		t.Fatalf("recovery not logged: %v %+v", err, logs)
	}
}

// A cancel requested while the runner was gone settles as cancelled.
func TestRecoverHonoursCancelRequest(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("dead"))
	r.Register("k", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	j, _ := r.Enqueue(ctx, NewJob{OrgID: "org_a", Kind: "k"})
	if c, err := r.claim(ctx); err != nil || c == nil {
		t.Fatalf("claim: %v", err)
	}
	if c, err := r.Cancel(ctx, "org_a", j.ID); err != nil || c.Status != StatusRunning || !c.CancelRequested {
		t.Fatalf("cancel running: %v %+v", err, c)
	}
	expire(t, d, j.ID)
	if res, err := r.Recover(ctx); err != nil || res.Cancelled != 1 {
		t.Fatalf("recover: %v %+v", err, res)
	}
	if x := status(t, r, "org_a", j.ID); x.Status != StatusCancelled {
		t.Fatalf("status %s", x.Status)
	}
}

func expire(t *testing.T, d *db.DB, id string) {
	t.Helper()
	if _, err := d.SQL().ExecContext(context.Background(), d.Rebind(`UPDATE jobs SET lease_until = ? WHERE id = ?`),
		time.Now().Add(-time.Minute).UnixMilli(), id); err != nil {
		t.Fatal(err)
	}
}

func joinLines(p *LogPage) string {
	var b strings.Builder
	for _, l := range p.Lines {
		b.WriteString(l.Line + "\n")
	}
	return b.String()
}

// Cancel mid-job: the handler's context ends with ErrCancelled, progress and
// logs written so far stay, and the job ends cancelled.
func TestCancelMidJob(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("a"))
	started := make(chan struct{})
	var cause atomic.Value
	r.Register("long", func(ctx context.Context, j *Job, log Logger, progress func(any)) (any, error) {
		progress(map[string]int{"done": 1})
		fmt.Fprintf(log, "step one\npartial")
		close(started)
		<-ctx.Done()
		cause.Store(context.Cause(ctx))
		log.Printf("stopping")
		return map[string]int{"done": 1}, context.Cause(ctx)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.Stop(ctx)
	j, err := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "long"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	waitFor(t, "progress written", func() bool { return status(t, r, "org_a", j.ID).Progress != nil })
	if _, err := r.Cancel(ctx, "org_a", j.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cancelled", func() bool { return status(t, r, "org_a", j.ID).Status == StatusCancelled })
	if c, _ := cause.Load().(error); !errors.Is(c, ErrCancelled) {
		t.Fatalf("handler cause %v", c)
	}
	x := status(t, r, "org_a", j.ID)
	if string(x.Progress) != `{"done":1}` || string(x.Result) != `{"done":1}` || x.FinishedAt == nil || x.LeaseOwner != "" {
		t.Fatalf("cancelled job %+v progress %s result %s", x, x.Progress, x.Result)
	}
	logs, _ := r.Logs(ctx, "org_a", j.ID, 0, 0)
	got := joinLines(logs)
	for _, want := range []string{"step one\n", "partial\n", "stopping\n", "cancelled: job cancelled\n"} {
		if !strings.Contains(got, want) {
			t.Fatalf("logs %q lack %q", got, want)
		}
	}
}

// Shutdown and restart: runner A stops mid-job and queues it again; runner B
// (the restarted server) resumes it to success.
func TestShutdownAndRestartResume(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	var attempts []int
	var mu sync.Mutex
	started := make(chan struct{}, 2)
	h := func(ctx context.Context, j *Job, log Logger, progress func(any)) (any, error) {
		mu.Lock()
		attempts = append(attempts, j.Attempt)
		mu.Unlock()
		started <- struct{}{}
		if !j.Resume() {
			<-ctx.Done() // the first run waits for the shutdown
			return nil, context.Cause(ctx)
		}
		return map[string]bool{"resumed": true}, nil
	}
	a := New(d, fastOptions("a"))
	a.Register("exp", h)
	if err := a.Start(ctx); err != nil {
		t.Fatal(err)
	}
	j, err := a.Create(ctx, NewJob{OrgID: "local", Kind: "exp"})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	a.Stop(ctx)
	if x := status(t, a, "local", j.ID); x.Status != StatusQueued || x.LeaseOwner != "" || x.Attempt != 1 {
		t.Fatalf("after shutdown %+v", x)
	}
	b := New(d, fastOptions("b"))
	b.Register("exp", h)
	if err := b.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.Stop(ctx)
	waitFor(t, "resumed job to succeed", func() bool { return status(t, b, "local", j.ID).Status == StatusSucceeded })
	mu.Lock()
	defer mu.Unlock()
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("attempts %v", attempts)
	}
	logs, _ := b.Logs(ctx, "local", j.ID, 0, 0)
	if got := joinLines(logs); !strings.Contains(got, "runner shutting down") || !strings.Contains(got, "resuming: attempt 2") {
		t.Fatalf("logs %q", got)
	}
}

func TestLogCapsKeepHeadAndTail(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	opts := fastOptions("a")
	opts.LogHead, opts.LogTail, opts.MaxLineBytes = 3, 4, 10
	r := New(d, opts)
	r.Register("chatty", func(ctx context.Context, j *Job, log Logger, progress func(any)) (any, error) {
		for i := 1; i <= 20; i++ {
			log.Printf("line %d", i)
		}
		log.Warnf("%s", strings.Repeat("x", 50))
		return nil, nil
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.Stop(ctx)
	j, _ := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "chatty"})
	waitFor(t, "done", func() bool { return status(t, r, "org_a", j.ID).Status == StatusSucceeded })
	page, err := r.Logs(ctx, "org_a", j.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	for _, l := range page.Lines {
		seqs = append(seqs, l.Seq)
	}
	if fmt.Sprint(seqs) != "[1 2 3 18 19 20 21]" || page.Dropped != 14 || page.Next != 21 {
		t.Fatalf("kept %v dropped %d next %d", seqs, page.Dropped, page.Next)
	}
	last := page.Lines[len(page.Lines)-1]
	if last.Level != LevelWarn || last.Line != "xxxxxxxxxx [line cut]" {
		t.Fatalf("last line %+v", last)
	}
	tail, _ := r.Logs(ctx, "org_a", j.ID, 19, 1)
	if len(tail.Lines) != 1 || tail.Lines[0].Seq != 20 || tail.Next != 20 {
		t.Fatalf("since 19 limit 1: %+v", tail)
	}
}

func TestHandlerPanicAndErrorFail(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("a"))
	r.Register("boom", func(context.Context, *Job, Logger, func(any)) (any, error) { panic("kaboom") })
	r.Register("err", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, errors.New("no luck") })
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer r.Stop(ctx)
	a, _ := r.Create(ctx, NewJob{OrgID: "o", Kind: "boom"})
	b, _ := r.Create(ctx, NewJob{OrgID: "o", Kind: "err"})
	waitFor(t, "both failed", func() bool {
		return status(t, r, "o", a.ID).Status == StatusFailed && status(t, r, "o", b.ID).Status == StatusFailed
	})
	if e := status(t, r, "o", a.ID).Error; !strings.Contains(e, "kaboom") {
		t.Fatalf("panic error %q", e)
	}
	if e := status(t, r, "o", b.ID).Error; e != "no luck" {
		t.Fatalf("error %q", e)
	}
}

func TestClipLineKeepsUTF8(t *testing.T) {
	long := strings.Repeat("a", 8191) + "é" + "tail"
	got := clipLine(long, 8192)
	if !utf8.ValidString(got) || got != strings.Repeat("a", 8191)+" [line cut]" {
		t.Fatalf("clip at a rune boundary: valid=%v len=%d", utf8.ValidString(got), len(got))
	}
	if got := clipLine("ok\xffbad", 100); !utf8.ValidString(got) || got != "ok�bad" {
		t.Fatalf("invalid bytes: %q", got)
	}
	if got := clipLine("short", 10); got != "short" {
		t.Fatalf("short: %q", got)
	}
}

func TestCancelTransitionsByState(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("a"))
	r.Register("echo", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	queued, _ := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "echo"})
	running, _ := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "echo"})
	done, _ := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "echo"})
	// Simulate a concurrent claim and a finished job without starting the runner.
	if _, err := d.SQL().ExecContext(ctx, d.Rebind(`UPDATE jobs SET status = ?, lease_owner = 'other', lease_until = ? WHERE id = ?`),
		string(StatusRunning), time.Now().Add(time.Hour).UnixMilli(), running.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().ExecContext(ctx, d.Rebind(`UPDATE jobs SET status = ? WHERE id = ?`), string(StatusSucceeded), done.ID); err != nil {
		t.Fatal(err)
	}
	if j, err := r.Cancel(ctx, "org_a", queued.ID); err != nil || j.Status != StatusCancelled || !j.CancelRequested {
		t.Fatalf("queued: %+v %v", j, err)
	}
	if j, err := r.Cancel(ctx, "org_a", running.ID); err != nil || j.Status != StatusRunning || !j.CancelRequested {
		t.Fatalf("running: %+v %v", j, err)
	}
	if _, err := r.Cancel(ctx, "org_a", done.ID); !errors.Is(err, ErrFinished) {
		t.Fatalf("finished: %v", err)
	}
	if _, err := r.Cancel(ctx, "org_b", queued.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other org: %v", err)
	}
}

func TestRecoverLeavesUnregisteredKinds(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	online := New(d, fastOptions("a"))
	online.Register("long", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil }, WithMaxAttempts(20))
	j, err := online.Enqueue(ctx, NewJob{OrgID: "org_a", Kind: "long"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.SQL().ExecContext(ctx, d.Rebind(`UPDATE jobs SET status = ?, attempt = 3, lease_owner = 'gone', lease_until = 1 WHERE id = ?`),
		string(StatusRunning), j.ID); err != nil {
		t.Fatal(err)
	}
	offline := New(d, fastOptions("b")) // has no "long" kind, like an -offline server
	offline.Register("other", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	if res, err := offline.Recover(ctx); err != nil || res != (RecoverResult{}) {
		t.Fatalf("offline recover: %+v %v", res, err)
	}
	if got := status(t, online, "org_a", j.ID); got.Status != StatusRunning {
		t.Fatalf("offline runner settled a kind it does not run: %+v", got)
	}
	if res, err := online.Recover(ctx); err != nil || res.Requeued != 1 {
		t.Fatalf("online recover: %+v %v", res, err)
	}
}

func TestKindLimitLeavesWorkersForOtherWork(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	opts := fastOptions("a")
	opts.Workers = 3
	r := New(d, opts)
	block := make(chan struct{})
	var started atomic.Int32
	r.Register("wait", func(ctx context.Context, j *Job, log Logger, progress func(any)) (any, error) {
		started.Add(1)
		select {
		case <-block:
		case <-ctx.Done():
		}
		return nil, nil
	}, WithLimit(2, 1))
	r.Register("quick", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	var waits []*Job
	for _, org := range []string{"org_a", "org_a", "org_b", "org_c"} {
		j, err := r.Create(ctx, NewJob{OrgID: org, Kind: "wait"})
		if err != nil {
			t.Fatal(err)
		}
		waits = append(waits, j)
	}
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { close(block); r.Stop(ctx) }()
	waitFor(t, "two waits", func() bool { return started.Load() == 2 })
	if st := status(t, r, "org_a", waits[1].ID).Status; st != StatusQueued {
		t.Fatalf("second org_a job past the per-org cap: %s", st)
	}
	q, _ := r.Create(ctx, NewJob{OrgID: "org_d", Kind: "quick"})
	waitFor(t, "quick job", func() bool { return status(t, r, "org_d", q.ID).Status == StatusSucceeded })
	time.Sleep(100 * time.Millisecond)
	if n := started.Load(); n != 2 {
		t.Fatalf("kind cap exceeded: %d running", n)
	}
}

func TestSecondStopWaitsForHandBack(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("a"))
	var started atomic.Bool
	r.Register("slow", func(ctx context.Context, j *Job, log Logger, progress func(any)) (any, error) {
		started.Store(true)
		<-ctx.Done()
		time.Sleep(200 * time.Millisecond) // graceful hand-back takes a while
		return nil, context.Cause(ctx)
	})
	j, _ := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "slow"})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "start", started.Load)
	go r.Stop(ctx) // e.g. a context.AfterFunc
	time.Sleep(20 * time.Millisecond)
	r.Stop(ctx) // the owner's synchronous stop before closing the database
	if st := status(t, r, "org_a", j.ID).Status; st != StatusQueued {
		t.Fatalf("second Stop returned before the hand-back: %s", st)
	}
}

// TestRecoverHonoursCancelAfterScan is audit qc2-18-01: a cancel accepted
// between Recover's scan and its requeue settles the job as cancelled, and
// a queued row carrying a cancel request is never claimed.
func TestRecoverHonoursCancelAfterScan(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("dead"))
	r.Register("k", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	j, _ := r.Enqueue(ctx, NewJob{OrgID: "org_a", Kind: "k"})
	if c, err := r.claim(ctx); err != nil || c == nil {
		t.Fatalf("claim: %v", err)
	}
	expire(t, d, j.ID)
	afterRecoverScan = func() {
		if _, err := r.Cancel(ctx, "org_a", j.ID); err != nil {
			t.Errorf("cancel: %v", err)
		}
	}
	t.Cleanup(func() { afterRecoverScan = nil })
	res, err := r.Recover(ctx)
	afterRecoverScan = nil
	if err != nil || res.Cancelled != 1 || res.Requeued != 0 {
		t.Fatalf("recover: %v %+v", err, res)
	}
	if x := status(t, r, "org_a", j.ID); x.Status != StatusCancelled {
		t.Fatalf("status %s", x.Status)
	}
	stray, _ := r.Enqueue(ctx, NewJob{OrgID: "org_a", Kind: "k"})
	if _, err := d.SQL().ExecContext(ctx, d.Rebind(`UPDATE jobs SET cancel_requested = 1 WHERE id = ?`), stray.ID); err != nil {
		t.Fatal(err)
	}
	if c, err := r.claim(ctx); err != nil || c != nil {
		t.Fatalf("claimed a cancel-requested job: %v %+v", err, c)
	}
}

// TestStaleAttemptCannotFinishReclaim is audit qc2-18-02: once a job is
// requeued and reclaimed by the same runner, the old attempt can neither
// renew, write progress for, nor finalize the new claim.
func TestStaleAttemptCannotFinishReclaim(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("same"))
	r.Register("k", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	j, _ := r.Enqueue(ctx, NewJob{OrgID: "org_a", Kind: "k"})
	first, err := r.claim(ctx)
	if err != nil || first == nil {
		t.Fatalf("claim: %v", err)
	}
	expire(t, d, j.ID)
	if res, err := r.Recover(ctx); err != nil || res.Requeued != 1 {
		t.Fatalf("recover: %v %+v", err, res)
	}
	second, err := r.claim(ctx)
	if err != nil || second == nil || second.Attempt != 2 {
		t.Fatalf("reclaim: %v %+v", err, second)
	}
	if claimKey(first) == claimKey(second) {
		t.Fatal("two claims share one cancellation key")
	}
	r.finish(first, StatusSucceeded, nil, "")
	if x := status(t, r, "org_a", j.ID); x.Status != StatusRunning || x.Attempt != 2 {
		t.Fatalf("stale attempt finalized the reclaim: %+v", x)
	}
	r.finish(second, StatusSucceeded, nil, "")
	if x := status(t, r, "org_a", j.ID); x.Status != StatusSucceeded {
		t.Fatalf("current attempt not recorded: %+v", x)
	}
}

// TestLogSeqAllocatedByDatabase is audit qc2-18-05: two writers for one job
// that started from the same highest seq (recovery and the resumed handler)
// both keep their line.
func TestLogSeqAllocatedByDatabase(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("a"))
	r.Register("k", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	j, _ := r.Enqueue(ctx, NewJob{OrgID: "org_a", Kind: "k"})
	a, b := r.newLogger(j), r.newLogger(j)
	a.Printf("resuming")
	r.systemLog(ctx, j.ID, "warn", "lease expired")
	b.Printf("second writer")
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				r.newLogger(j).Printf("line %d", n)
			}
		}()
	}
	wg.Wait()
	logs, err := r.Logs(ctx, "org_a", j.ID, 0, 0)
	if err != nil || len(logs.Lines) != 43 {
		t.Fatalf("logs: %v, %d lines", err, len(logs.Lines))
	}
}

// TestCancelConflictIsErrConflict is audit qc2-18-06: a row that moves
// between Cancel's two updates answers the retryable ErrConflict (HTTP 409),
// not a plain error (HTTP 500).
func TestCancelConflictIsErrConflict(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("a"))
	r.Register("k", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil })
	j, _ := r.Create(ctx, NewJob{OrgID: "org_a", Kind: "k"})
	if _, err := d.SQL().ExecContext(ctx, d.Rebind(`UPDATE jobs SET status = ? WHERE id = ?`), string(StatusRunning), j.ID); err != nil {
		t.Fatal(err)
	}
	cancelBetweenUpdates = func(tx *sql.Tx) {
		if _, err := tx.ExecContext(ctx, d.Rebind(`UPDATE jobs SET status = ? WHERE id = ?`), string(StatusQueued), j.ID); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { cancelBetweenUpdates = nil })
	if _, err := r.Cancel(ctx, "org_a", j.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancel conflict = %v, want ErrConflict", err)
	}
}

// TestClaimGateSkipsOrgsWithoutCapacity is audit qc2-18-03: a gated kind is
// claimed only for organizations its gate lists, so jobs of organizations
// without capacity stay queued and an eligible one is claimed past them.
func TestClaimGateSkipsOrgsWithoutCapacity(t *testing.T) {
	d := dbtest.Open(t)
	ctx := context.Background()
	r := New(d, fastOptions("a"))
	var mu sync.Mutex
	eligible := []string{}
	r.Register("exp", func(context.Context, *Job, Logger, func(any)) (any, error) { return nil, nil },
		WithLimit(1, 1), WithClaimGate(func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), eligible...) }))
	waiting, _ := r.Enqueue(ctx, NewJob{OrgID: "org_wait", Kind: "exp"})
	ready, _ := r.Enqueue(ctx, NewJob{OrgID: "org_ready", Kind: "exp"})
	if c, err := r.claim(ctx); err != nil || c != nil {
		t.Fatalf("claimed with no eligible org: %v %+v", err, c)
	}
	mu.Lock()
	eligible = []string{"org_ready"}
	mu.Unlock()
	c, err := r.claim(ctx)
	if err != nil || c == nil || c.ID != ready.ID {
		t.Fatalf("eligible org not claimed past the waiting one: %v %+v", err, c)
	}
	if x := status(t, r, "org_wait", waiting.ID); x.Status != StatusQueued {
		t.Fatalf("waiting org's job %s", x.Status)
	}
}
