package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Kind-aware runner job leases and lease secrets (lane W01,
// audit/ISSUEWORKER_CONTRACT.md): a runner registered with JobKinds is
// offered only those kinds, and a lease's Secrets reach the claiming
// runner's executor without ever reaching a coordinator log line.

const (
	kindIssueWork  = "issuebench.work"
	kindAgentBuild = "agent.build"
	fakeGHToken    = "ghp_testtoken123456"
	fakeGatewayVK  = "sk-bf-vk-testkey-abcdef"
)

// syncBuf is a goroutine-safe log sink.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// recordJobs is a JobExecutor that records every lease it ran.
type recordJobs struct {
	mu   sync.Mutex
	got  []schemas.JobLease
	name string
}

func (r *recordJobs) RunJob(ctx context.Context, job *schemas.JobLease, log func(string)) (*JobRun, error) {
	r.mu.Lock()
	cp := *job
	cp.Secrets = map[string]string{}
	for k, v := range job.Secrets {
		cp.Secrets[k] = v
	}
	r.got = append(r.got, cp)
	r.mu.Unlock()
	log(r.name + " ran " + job.JobKind)
	return &JobRun{Result: json.RawMessage(`{"ok":true}`)}, nil
}

func (r *recordJobs) leases() []schemas.JobLease {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]schemas.JobLease(nil), r.got...)
}

// kindRunner registers a hosted runner that takes only kinds (nil: every
// kind) and logs to logs.
func kindRunner(t *testing.T, url, id string, kinds []string, logs *syncBuf) *Worker {
	t.Helper()
	side := newRemoteSide(t)
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: url, Token: tokADefault, WorkerID: id, Version: BenchVersion, PollEvery: 10 * time.Millisecond,
			ScratchDir: t.TempDir(), JobKinds: kinds, Logger: utils.NewStdLogger(logs, true)})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestRunnerJobKindsSharedWorker(t *testing.T) {
	fastJobTiming(t)
	var coordLog, workerLog syncBuf
	hub := NewRemoteHub(RemoteOptions{Logger: utils.NewStdLogger(&coordLog, true)})
	a := newHostedOrg(t, hub, "org_a")

	shared := &recordJobs{name: "shared"}
	startRunner(t, kindRunner(t, a.url, "shared-1", []string{kindIssueWork}, &workerLog), shared)

	if a.c.HasJobRunner() {
		t.Fatal("HasJobRunner counts a runner limited to issuebench.work")
	}
	if !a.c.HasJobRunnerFor(kindIssueWork) {
		t.Fatal("HasJobRunnerFor(issuebench.work) is false with the shared runner online")
	}
	if a.c.HasJobRunnerFor(kindAgentBuild) {
		t.Fatal("HasJobRunnerFor(agent.build) is true with only the shared runner online")
	}

	// With only the shared runner, an agent.build job has no runner at all.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := a.c.RunJob(ctx, schemas.JobLease{JobID: "job_b0", JobKind: kindAgentBuild, Argv: []string{"agent", "build"}}, nil); !errors.Is(err, ErrNoRunner) {
		t.Fatalf("agent.build with only the shared runner: %v, want ErrNoRunner", err)
	}

	// The issuebench.work job reaches the shared runner with its secrets.
	secrets := map[string]string{"github_token": fakeGHToken, "gateway_key": fakeGatewayVK}
	var mu sync.Mutex
	var lines []string
	out, err := a.c.RunJob(ctx, schemas.JobLease{JobID: "job_w1", JobKind: kindIssueWork, Argv: []string{"issuebench", "work"}, Secrets: secrets},
		func(l string) { mu.Lock(); lines = append(lines, l); mu.Unlock() })
	if err != nil {
		t.Fatal(err)
	}
	if out.Worker != "shared-1" || out.Error != "" {
		t.Fatalf("outcome %+v", out)
	}
	got := shared.leases()
	if len(got) != 1 || got[0].JobKind != kindIssueWork {
		t.Fatalf("shared runner ran %+v", got)
	}
	if got[0].Secrets["github_token"] != fakeGHToken || got[0].Secrets["gateway_key"] != fakeGatewayVK {
		t.Fatalf("secrets did not reach the executor: %v", got[0].Secrets)
	}

	// An own runner (no JobKinds) registers but does not poll yet: the
	// agent.build job queues, and the shared runner never takes it.
	own := &recordJobs{name: "own"}
	ownW := kindRunner(t, a.url, "own-1", nil, &workerLog)
	if !a.c.HasJobRunner() {
		t.Fatal("HasJobRunner is false with an own runner registered")
	}
	type res struct {
		out *JobOutcome
		err error
	}
	done := make(chan res, 1)
	go func() {
		o, err := a.c.RunJob(ctx, schemas.JobLease{JobID: "job_b1", JobKind: kindAgentBuild, Argv: []string{"agent", "build"}}, nil)
		done <- res{o, err}
	}()
	time.Sleep(300 * time.Millisecond) // many shared-runner polls
	if n := len(shared.leases()); n != 1 {
		t.Fatalf("the shared runner took the agent.build job (%d leases)", n)
	}
	select {
	case r := <-done:
		t.Fatalf("agent.build ended before the own runner polled: %+v %v", r.out, r.err)
	default:
	}
	startRunner(t, ownW, own)
	r := <-done
	if r.err != nil || r.out.Worker != "own-1" {
		t.Fatalf("agent.build outcome %+v %v", r.out, r.err)
	}
	if g := own.leases(); len(g) != 1 || g[0].JobKind != kindAgentBuild {
		t.Fatalf("own runner ran %+v", g)
	}
	if n := len(shared.leases()); n != 1 {
		t.Fatalf("shared runner leases %d, want 1", n)
	}
	// The shared runner was never marked as refusing jobs.
	if !a.c.HasJobRunnerFor(kindIssueWork) {
		t.Fatal("the shared runner stopped counting for issuebench.work")
	}

	for name, logs := range map[string]string{"coordinator": coordLog.String(), "worker": workerLog.String()} {
		for _, s := range []string{fakeGHToken, fakeGatewayVK} {
			if strings.Contains(logs, s) {
				t.Fatalf("a secret value is in the %s log:\n%s", name, logs)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, l := range lines {
		if strings.Contains(l, fakeGHToken) || strings.Contains(l, fakeGatewayVK) {
			t.Fatalf("a secret value is in a job log line: %q", l)
		}
	}
}

func TestRunnerJobKindsOldWorkerTakesEverything(t *testing.T) {
	fastJobTiming(t)
	hub := NewRemoteHub(RemoteOptions{})
	a := newHostedOrg(t, hub, "org_a")
	old := &recordJobs{name: "old"}
	startRunner(t, kindRunner(t, a.url, "old-1", nil, &syncBuf{}), old)
	if !a.c.HasJobRunner() || !a.c.HasJobRunnerFor(kindIssueWork) || !a.c.HasJobRunnerFor(kindAgentBuild) {
		t.Fatal("a runner without JobKinds does not count for every kind")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i, kind := range []string{kindIssueWork, kindAgentBuild, ""} {
		out, err := a.c.RunJob(ctx, schemas.JobLease{JobID: "job_o" + string(rune('0'+i)), JobKind: kind, Argv: []string{"x"}}, nil)
		if err != nil || out.Worker != "old-1" {
			t.Fatalf("kind %q: %+v %v", kind, out, err)
		}
	}
	if n := len(old.leases()); n != 3 {
		t.Fatalf("old runner ran %d jobs, want 3", n)
	}
}

func TestRunnerJobKindsReRegister(t *testing.T) {
	hub := NewRemoteHub(RemoteOptions{})
	a := newHostedOrg(t, hub, "org_a")
	ctx := scoped(tokADefault)
	reg := func(kinds []string) error {
		_, err := a.c.Register(ctx, &schemas.WorkerRegister{WorkerID: "r-1", Host: "h", Version: BenchVersion, Slots: 1, JobKinds: kinds})
		return err
	}
	if err := reg([]string{kindIssueWork}); err != nil {
		t.Fatal(err)
	}
	if a.c.HasJobRunner() || !a.c.HasJobRunnerFor(kindIssueWork) {
		t.Fatal("first registration: kinds not applied")
	}
	if err := reg(nil); err != nil {
		t.Fatal(err)
	}
	if !a.c.HasJobRunner() || !a.c.HasJobRunnerFor(kindAgentBuild) {
		t.Fatal("re-registration without kinds still limits the runner")
	}
	if err := reg([]string{""}); !errors.Is(err, schemas.ErrWorkerRequest) {
		t.Fatalf("empty job kind: %v", err)
	}
}

func TestJobLeaseRedacted(t *testing.T) {
	l := schemas.JobLease{JobID: "j", Secrets: map[string]string{"github_token": fakeGHToken}}
	r := l.Redacted()
	if r.Secrets["github_token"] != "[redacted]" {
		t.Fatalf("redacted %v", r.Secrets)
	}
	if l.Secrets["github_token"] != fakeGHToken {
		t.Fatal("Redacted changed the original lease")
	}
	b, _ := json.Marshal(r)
	if strings.Contains(string(b), fakeGHToken) {
		t.Fatalf("redacted lease JSON holds the token: %s", b)
	}
	if (schemas.JobLease{}).Redacted().Secrets != nil {
		t.Fatal("a lease without secrets gained a map")
	}
}
