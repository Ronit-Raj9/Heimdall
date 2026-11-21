package engine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Runner jobs (gap G05): a hosted CLI job leased to the org's runner over
// the worker routes.

// fakeJobs is a JobExecutor: it logs, writes the collected file and returns
// a result, or blocks until cancelled.
type fakeJobs struct {
	ran       atomic.Int32
	block     bool
	cancelled chan struct{}
	once      sync.Once
}

func (f *fakeJobs) RunJob(ctx context.Context, job *schemas.JobLease, log func(string)) (*JobRun, error) {
	f.ran.Add(1)
	dir, err := os.MkdirTemp("", "fakejob-*")
	if err != nil {
		return nil, err
	}
	run := &JobRun{Dir: dir, Cleanup: func() { os.RemoveAll(dir) }}
	log("running " + strings.Join(job.Argv, " "))
	if f.block {
		<-ctx.Done()
		f.once.Do(func() { close(f.cancelled) })
		return run, ctx.Err()
	}
	if err := os.MkdirAll(filepath.Join(dir, "uploads", "visual"), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "uploads", "visual", "c.png"), []byte("png"), 0o600); err != nil {
		return nil, err
	}
	log("done")
	run.Result = json.RawMessage(`{"ok":true}`)
	run.Files = []string{"uploads/visual/c.png"}
	return run, nil
}

func fastJobTiming(t *testing.T) {
	t.Helper()
	hb, stale, claim, watch := JobHeartbeatEvery, JobStaleAfter, JobClaimWait, jobWatchEvery
	JobHeartbeatEvery, JobStaleAfter, JobClaimWait, jobWatchEvery = 20*time.Millisecond, 5*time.Second, 5*time.Second, 10*time.Millisecond
	t.Cleanup(func() { JobHeartbeatEvery, JobStaleAfter, JobClaimWait, jobWatchEvery = hb, stale, claim, watch })
}

// startRunner runs w until the test ends.
func startRunner(t *testing.T, w *Worker, jobs JobExecutor) {
	t.Helper()
	w.opts.Jobs = jobs
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = w.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
}

func TestRunnerJobEndToEnd(t *testing.T) {
	fastJobTiming(t)
	hub := NewRemoteHub(RemoteOptions{})
	a := newHostedOrg(t, hub, "org_a")
	fj := &fakeJobs{}
	startRunner(t, hostedRunner(t, a.url, tokADefault, "a-1", ""), fj)

	var mu sync.Mutex
	var lines []string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := a.c.RunJob(ctx, schemas.JobLease{JobID: "job_1", JobKind: "visual.capture", Argv: []string{"visual", "capture"},
		Collect: []string{"uploads/visual"}}, func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	if out.Worker != "a-1" || out.Error != "" || out.Archive == "" {
		t.Fatalf("outcome %+v", out)
	}
	res, files, err := ReadJobResult(out.Archive, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if string(res) != `{"ok":true}` || len(files) != 1 || files[0].Path != "uploads/visual/c.png" || string(files[0].Data) != "png" {
		t.Fatalf("result %s files %+v", res, files)
	}
	mu.Lock()
	got := strings.Join(lines, "|")
	mu.Unlock()
	if !strings.Contains(got, "running visual capture") || !strings.Contains(got, "done") {
		t.Fatalf("log lines %q", got)
	}
	for _, w := range a.c.Workers() {
		if len(w.Running) != 0 {
			t.Fatalf("worker still running %v", w.Running)
		}
	}
}

func TestRunnerJobCancel(t *testing.T) {
	fastJobTiming(t)
	hub := NewRemoteHub(RemoteOptions{})
	a := newHostedOrg(t, hub, "org_a")
	fj := &fakeJobs{block: true, cancelled: make(chan struct{})}
	startRunner(t, hostedRunner(t, a.url, tokADefault, "a-1", ""), fj)

	ctx, cancel := context.WithCancel(context.Background())
	errc := make(chan error, 1)
	go func() {
		_, err := a.c.RunJob(ctx, schemas.JobLease{JobID: "job_2", JobKind: "gpu.check", Argv: []string{"gpu", "check"}}, nil)
		errc <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for fj.ran.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if fj.ran.Load() == 0 {
		t.Fatal("runner never claimed the job")
	}
	cancel()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("RunJob after cancel: %v", err)
	}
	select {
	case <-fj.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not reach the runner")
	}
}

func TestRunnerJobNoRunner(t *testing.T) {
	fastJobTiming(t)
	hub := NewRemoteHub(RemoteOptions{})
	a := newHostedOrg(t, hub, "org_a")
	start := time.Now()
	if _, err := a.c.RunJob(context.Background(), schemas.JobLease{JobID: "j", Argv: []string{"gpu", "check"}}, nil); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("no runner: %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("no-runner error was not immediate")
	}
	// A runner that does not run jobs refuses the lease; with no other
	// runner the job fails with ErrNoRunner instead of waiting.
	startRunner(t, hostedRunner(t, a.url, tokADefault, "old-1", ""), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := a.c.RunJob(ctx, schemas.JobLease{JobID: "j2", Argv: []string{"gpu", "check"}}, nil); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("job-less runner: %v", err)
	}
}

func TestRunnerJobOrgIsolation(t *testing.T) {
	fastJobTiming(t)
	hub := NewRemoteHub(RemoteOptions{})
	a, b := newHostedOrg(t, hub, "org_a"), newHostedOrg(t, hub, "org_b")
	ja, jb := &fakeJobs{}, &fakeJobs{}
	startRunner(t, hostedRunner(t, b.url, tokBDefault, "b-1", ""), jb)

	// org_a has no runner of its own: org_b's runner never helps.
	if _, err := a.c.RunJob(context.Background(), schemas.JobLease{JobID: "ja", Argv: []string{"gpu", "check"}}, nil); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("org_a without runner: %v", err)
	}
	startRunner(t, hostedRunner(t, a.url, tokADefault, "a-1", ""), ja)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 3; i++ {
		out, err := a.c.RunJob(ctx, schemas.JobLease{JobID: "ja", Argv: []string{"gpu", "check"}}, nil)
		if err != nil {
			t.Fatal(err)
		}
		os.Remove(out.Archive)
		if out.Worker != "a-1" {
			t.Fatalf("org_a job ran on %s", out.Worker)
		}
	}
	if jb.ran.Load() != 0 || ja.ran.Load() != 3 {
		t.Fatalf("org_b runner ran %d org_a jobs; org_a runner ran %d", jb.ran.Load(), ja.ran.Load())
	}
}
