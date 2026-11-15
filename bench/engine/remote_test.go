package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/valyala/fasthttp"
)

const remoteTestToken = "hd-remote-token-0123456789"

// remoteSide is one machine of a remote test: a bench with a real FSStore.
type remoteSide struct {
	b  *bench
	st *store.FSStore
}

func newRemoteSide(t *testing.T) *remoteSide {
	t.Helper()
	b := newBench(t)
	st, err := store.NewFSStore(filepath.Join(b.root, "runs"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b.reg.Store = st
	t.Cleanup(func() { writable(t, b.root) })
	return &remoteSide{b: b, st: st}
}

// writable makes sealed (read-only) run folders removable for TempDir.
func writable(t *testing.T, root string) {
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return os.Chmod(p, 0o755)
		}
		return nil
	})
	if err != nil {
		t.Logf("make writable: %v", err)
	}
}

// remoteWorld is a coordinator serving the worker API over real HTTP on a
// random loopback port, with one experiment attached.
type remoteWorld struct {
	coord *remoteSide
	c     *Coordinator
	url   string
	m     *schemas.Manifest
	r     *schedRun
	run   RunOptions // what attach and Run use
}

func newRemoteWorld(t *testing.T, e *schemas.Experiment, opts RemoteOptions) *remoteWorld {
	t.Helper()
	cs := newRemoteSide(t)
	if opts.Token == "" {
		opts.Token = remoteTestToken
	}
	if opts.StagingDir == "" {
		opts.StagingDir = t.TempDir()
	}
	c, err := NewCoordinator(cs.b.reg, opts)
	if err != nil {
		t.Fatal(err)
	}
	url := serveWorkers(t, c)
	return &remoteWorld{coord: cs, c: c, url: url, m: cs.b.plan(t, e), run: RunOptions{Remote: c}}
}

// serveWorkers serves the worker routes on 127.0.0.1 at a random port.
func serveWorkers(t *testing.T, api handlers.WorkerAPI) string {
	t.Helper()
	r := router.New()
	handlers.NewWorkersHandler(schemas.ModeLive, api).RegisterRoutes(r)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fasthttp.Server{Handler: r.Handler, MaxRequestBodySize: 1 << 20}
	go func() {
		if err := srv.Serve(ln); err != nil {
			t.Logf("serve: %v", err)
		}
	}()
	t.Cleanup(func() {
		if err := srv.Shutdown(); err != nil {
			t.Logf("shutdown: %v", err)
		}
	})
	return "http://" + ln.Addr().String()
}

// attach starts the experiment the way Run does and opens it to workers,
// without local workers, so a test drives every lease itself.
func (rw *remoteWorld) attach(t *testing.T) func() *Summary {
	t.Helper()
	ctx := context.Background()
	s := NewScheduler(rw.coord.b.reg, rw.coord.b.root, nil, nil)
	r, err := s.start(ctx, rw.m, rw.run)
	if err != nil {
		t.Fatal(err)
	}
	r.ctx, r.queue = ctx, newSlotQueue(rw.m.Slots)
	rw.r = r
	detach := rw.c.attach(r)
	return func() *Summary {
		detach()
		r.mu.Lock()
		defer r.mu.Unlock()
		out := r.sum
		return &out
	}
}

func (rw *remoteWorld) worker(t *testing.T, id string, side *remoteSide) *Worker {
	t.Helper()
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: rw.url, Token: remoteTestToken, WorkerID: id, Version: BenchVersion, PollEvery: 10 * time.Millisecond,
			ScratchDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w
}

// lease asks the coordinator for a lease over HTTP; nil means no work.
func lease(t *testing.T, w *Worker) *schemas.WorkerLease {
	t.Helper()
	var l schemas.WorkerLease
	if err := w.call(context.Background(), http.MethodPost, workerAPI+"/lease", schemas.WorkerLeaseRequest{WorkerID: w.ID()}, &l); err != nil {
		t.Fatal(err)
	}
	if l.LeaseID == "" {
		return nil
	}
	return &l
}

// countedRuns returns slot id -> counted sealed runs in the coordinator store.
func countedRuns(t *testing.T, rw *remoteWorld) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, slot := range rw.m.Slots {
		recs, err := rw.coord.st.SlotRuns(context.Background(), rw.m.ExpID, slot.SlotID)
		if err != nil && !errors.Is(err, schemas.ErrNotFound) {
			t.Fatal(err)
		}
		for _, rec := range recs {
			if rec.Sealed && rec.Run.Outcome != schemas.OutcomeAbortedInfra {
				out[slot.SlotID]++
			}
		}
	}
	return out
}

func TestRemoteHappyPath(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	ctx := context.Background()
	for range rw.m.Slots {
		l := lease(t, w)
		if l == nil {
			t.Fatal("no lease while slots are pending")
		}
		res := w.Work(ctx, l)
		if res == nil || !res.Accepted || res.SealHash == "" {
			t.Fatalf("run not accepted: %+v", res)
		}
	}
	if l := lease(t, w); l != nil {
		t.Fatalf("lease %s after every slot ran", l.LeaseID)
	}
	sum := done()
	if sum.Sealed != 4 || sum.Remote != 4 || sum.RemoteRejected != 0 || sum.LeaseLost != 0 {
		t.Fatalf("summary %+v", sum)
	}
	for slot, n := range countedRuns(t, rw) {
		if n != 1 {
			t.Fatalf("slot %s has %d counted runs", slot, n)
		}
	}
	if err := rw.coord.st.VerifyChain(rw.m.ExpID); err != nil {
		t.Fatalf("chain: %v", err)
	}
	runs, err := rw.coord.st.SealedRuns(ctx, rw.m.ExpID, "")
	if err != nil || len(runs) != 4 {
		t.Fatalf("sealed runs %d, %v", len(runs), err)
	}
	for _, sr := range runs {
		if len(sr.Problems) > 0 {
			t.Fatalf("run %s problems %v", sr.Run.RunID, sr.Problems)
		}
		dir, err := filepath.Glob(filepath.Join(rw.coord.st.Root(), rw.m.ExpID, "slots", "*", "runs", sr.Run.RunID))
		if err != nil || len(dir) != 1 {
			t.Fatalf("run folder of %s: %v %v", sr.Run.RunID, dir, err)
		}
		for _, f := range []string{"remote/worker.json", "remote/seal.json", "metrics.json"} {
			if _, err := os.Stat(filepath.Join(dir[0], f)); err != nil {
				t.Fatalf("run %s: %v", sr.Run.RunID, err)
			}
		}
	}
	info := rw.c.Workers()
	if len(info) != 1 || info[0].Completed != 4 || info[0].State != schemas.WorkerIdle || len(info[0].Running) != 0 {
		t.Fatalf("workers %+v", info)
	}
}

func TestRemoteRefusesMismatch(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, s *remoteSide)
		want   string
	}{
		{"agent config", func(t *testing.T, s *remoteSide) {
			p := filepath.Join(s.b.root, "agents/claude-code.yaml")
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(strings.Replace(string(b), "model: claude-test", "model: claude-other", 1)), 0o644); err != nil {
				t.Fatal(err)
			}
		}, "recomputed locally"},
		{"suite lock", func(_ *testing.T, s *remoteSide) { s.b.suite.LockHash = "lock-other" }, "suite lock hash"},
		{"part version", func(_ *testing.T, s *remoteSide) {
			s.b.reg.Gates = append(s.b.reg.Gates, fakeGate{part: part{"extra_gate"}, w: s.b.w})
		}, "part versions differ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
			done := rw.attach(t)
			side := newRemoteSide(t)
			tc.change(t, side)
			w := rw.worker(t, "w1", side)
			l := lease(t, w)
			if l == nil {
				t.Fatal("no lease")
			}
			if res := w.Work(context.Background(), l); res != nil {
				t.Fatalf("worker ran a refused lease: %+v", res)
			}
			if side.b.w.count("exec") != 0 {
				t.Fatal("agent ran on a refused lease")
			}
			if l2 := lease(t, w); l2 != nil {
				t.Fatalf("refused experiment offered again: %s", l2.LeaseID)
			}
			w.mu.Lock()
			reason := w.exps[rw.m.ExpID].refused
			w.mu.Unlock()
			if !strings.Contains(reason, tc.want) {
				t.Fatalf("reason %q, want %q", reason, tc.want)
			}
			journal, err := os.ReadFile(filepath.Join(rw.c.opts.StagingDir, rw.m.ExpID+".jsonl"))
			if err != nil || !strings.Contains(string(journal), `"event":"refused"`) {
				t.Fatalf("journal %s %v", journal, err)
			}
			if got := len(rw.r.queue.pending); got != len(rw.m.Slots) {
				t.Fatalf("pending %d after refusal, want %d", got, len(rw.m.Slots))
			}
			if sum := done(); sum.Sealed != 0 || sum.SpentUSD != 0 {
				t.Fatalf("summary %+v", sum)
			}
		})
	}
}

func TestRemoteRejectsTamperedUpload(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	side := newRemoteSide(t)
	w := rw.worker(t, "w1", side)
	ctx := context.Background()
	l := lease(t, w)
	exp, reason, _ := w.admit(ctx, l)
	if reason != "" {
		t.Fatal(reason)
	}
	res, err := w.runner.RunSlot(ctx, SlotInput{Manifest: exp.m, Suite: exp.suite, Slot: exp.slot(l.Slot.SlotID), Attempt: l.Attempt})
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(res.Dir, "grade.json")
	if err := os.Chmod(res.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"passed": true, "tampered": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := w.upload(ctx, l, res)
	if err != nil {
		t.Fatal(err)
	}
	if out.Accepted || !strings.Contains(out.Reason, "grade.json changed") {
		t.Fatalf("tampered upload: %+v", out)
	}
	if n := len(countedRuns(t, rw)); n != 0 {
		t.Fatalf("%d slots got a run from a tampered upload", n)
	}
	if got := len(rw.r.queue.pending); got != len(rw.m.Slots) {
		t.Fatalf("pending %d after rejection, want %d", got, len(rw.m.Slots))
	}
	// The released slot is leased again and a clean run is accepted.
	l2 := lease(t, w)
	if l2 == nil || l2.Slot.SlotID != l.Slot.SlotID || l2.Attempt != 1 {
		t.Fatalf("re-lease %+v", l2)
	}
	if res2 := w.Work(ctx, l2); res2 == nil || !res2.Accepted {
		t.Fatalf("clean run after rejection: %+v", res2)
	}
	sum := done()
	if sum.RemoteRejected != 1 || sum.Remote != 1 || sum.Sealed != 1 {
		t.Fatalf("summary %+v", sum)
	}
}

func TestRemoteLostWorkerReleasedAndLateUploadRefused(t *testing.T) {
	stale := 300 * time.Millisecond
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{StaleAfter: stale})
	rw.run.StaleAfter, rw.run.HeartbeatEvery = stale, time.Hour
	done := rw.attach(t)
	ctx := context.Background()
	w1 := rw.worker(t, "w1", newRemoteSide(t))
	w2 := rw.worker(t, "w2", newRemoteSide(t))
	l1 := lease(t, w1)
	exp, reason, _ := w1.admit(ctx, l1)
	if reason != "" {
		t.Fatal(reason)
	}
	res1, err := w1.runner.RunSlot(ctx, SlotInput{Manifest: exp.m, Suite: exp.suite, Slot: exp.slot(l1.Slot.SlotID), Attempt: l1.Attempt})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(stale + 100*time.Millisecond)
	rw.c.Sweep(time.Now())
	if info := rw.c.Workers(); info[0].State != schemas.WorkerLost {
		t.Fatalf("silent worker state %q", info[0].State)
	}
	l2 := lease(t, w2)
	if l2 == nil || l2.Slot.SlotID != l1.Slot.SlotID {
		t.Fatalf("lost slot not re-leased first: %+v", l2)
	}
	if res2 := w2.Work(ctx, l2); res2 == nil || !res2.Accepted {
		t.Fatalf("re-leased run: %+v", res2)
	}
	_, err = w1.upload(ctx, l1, res1)
	if !errors.Is(err, schemas.ErrLeaseGone) {
		t.Fatalf("late upload: %v, want ErrLeaseGone", err)
	}
	if n := countedRuns(t, rw)[l1.Slot.SlotID]; n != 1 {
		t.Fatalf("slot %s has %d counted runs", l1.Slot.SlotID, n)
	}
	sum := done()
	if sum.LeaseLost != 1 || sum.Remote != 1 || sum.UnknownCostRuns != 1 {
		t.Fatalf("summary %+v", sum)
	}
}

func TestRemoteHeartbeatLostStopsWorker(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	rw.run.StaleAfter, rw.run.HeartbeatEvery = 2*time.Second, 200*time.Millisecond
	done := rw.attach(t)
	side := newRemoteSide(t)
	side.b.w.execDelay = 5 * time.Second
	w := rw.worker(t, "w1", side)
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	// Another owner takes the slot's store lease, as a scheduler that
	// reclaimed it would.
	if err := rw.coord.st.Release(context.Background(), l.Slot.SlotID, "worker:w1:"+l.LeaseID); err != nil {
		t.Fatal(err)
	}
	if ok, err := rw.coord.st.Lease(context.Background(), l.Slot.SlotID, "someone-else", time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	start := time.Now()
	if res := w.Work(context.Background(), l); res != nil {
		t.Fatalf("lost lease uploaded: %+v", res)
	}
	if time.Since(start) > 4*time.Second {
		t.Fatal("the run was not stopped on lease loss")
	}
	if sum := done(); sum.LeaseLost != 1 || sum.Remote != 0 {
		t.Fatalf("summary %+v", sum)
	}
}

func TestRemoteSpendCapAcrossWorkers(t *testing.T) {
	e := testExperiment()
	e.Budget.TotalUSD = 1.0
	rw := newRemoteWorld(t, e, RemoteOptions{})
	done := rw.attach(t)
	ctx := context.Background()
	s1, s2 := newRemoteSide(t), newRemoteSide(t)
	s1.b.w.cost, s2.b.w.cost = 0.6, 0.6
	w1, w2 := rw.worker(t, "w1", s1), rw.worker(t, "w2", s2)
	l1 := lease(t, w1)
	if l1 == nil {
		t.Fatal("no first lease")
	}
	// Before any cost is observed the first run reserves the whole budget.
	if l := lease(t, w2); l != nil {
		t.Fatalf("second worker leased past the reservation: %s", l.LeaseID)
	}
	if res := w1.Work(ctx, l1); res == nil || !res.Accepted {
		t.Fatalf("first run %+v", res)
	}
	l2 := lease(t, w2)
	if l2 == nil {
		t.Fatal("no lease below the cap")
	}
	if res := w2.Work(ctx, l2); res == nil || !res.Accepted {
		t.Fatalf("second run %+v", res)
	}
	for _, w := range []*Worker{w1, w2} {
		if l := lease(t, w); l != nil {
			t.Fatalf("leased past the cap: %s", l.LeaseID)
		}
	}
	sum := done()
	if !sum.BudgetStopped || sum.Remote != 2 || sum.SpentUSD < 1.19 || sum.SpentUSD > 1.21 || sum.Pending != 2 {
		t.Fatalf("summary %+v", sum)
	}
	if s1.b.w.count("exec")+s2.b.w.count("exec") != 2 {
		t.Fatal("agents ran past the cap")
	}
}

func TestSchedulerWithRemoteWorker(t *testing.T) {
	e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 2})
	rw := newRemoteWorld(t, e, RemoteOptions{})
	rw.coord.b.w.execDelay = 200 * time.Millisecond
	side := newRemoteSide(t)
	w := rw.worker(t, "w1", side)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		if err := w.Run(ctx); err != nil {
			t.Errorf("worker: %v", err)
		}
	}()
	sum, err := NewScheduler(rw.coord.b.reg, rw.coord.b.root, nil, nil).Run(context.Background(), rw.m,
		RunOptions{Concurrency: 1, Remote: rw.c})
	cancel()
	<-workerDone
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != len(rw.m.Slots) || sum.Remote == 0 || sum.Remote == len(rw.m.Slots) || sum.Errors != 0 {
		t.Fatalf("summary %+v: want local and remote runs sharing %d slots", sum, len(rw.m.Slots))
	}
	counted := countedRuns(t, rw)
	for _, slot := range rw.m.Slots {
		if counted[slot.SlotID] != 1 {
			t.Fatalf("slot %s has %d counted runs", slot.SlotID, counted[slot.SlotID])
		}
	}
	if err := rw.coord.st.VerifyChain(rw.m.ExpID); err != nil {
		t.Fatalf("chain: %v", err)
	}
}

func TestRemoteBadToken(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	side := newRemoteSide(t)
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: rw.url, Token: "wrong", WorkerID: "w1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Run(context.Background()); !errors.Is(err, schemas.ErrWorkerAuth) || !strings.Contains(err.Error(), "401") {
		t.Fatalf("run with a bad token: %v", err)
	}
	if len(rw.c.Workers()) != 0 {
		t.Fatal("a worker with a bad token registered")
	}
	if rw.c.CheckToken("") || rw.c.CheckToken("wrong") || !rw.c.CheckToken(remoteTestToken) {
		t.Fatal("token check")
	}
}

func TestRemoteUploadLimits(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{MaxUploadBytes: 10, ChunkBytes: 8})
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	ctx := context.Background()
	if _, err := rw.c.Upload(ctx, l.LeaseID, "w1", 0, make([]byte, 9)); !errors.Is(err, schemas.ErrUploadTooLarge) {
		t.Fatalf("chunk past the chunk size: %v", err)
	}
	if n, err := rw.c.Upload(ctx, l.LeaseID, "w1", 0, make([]byte, 8)); err != nil || n != 8 {
		t.Fatal(n, err)
	}
	if _, err := rw.c.Upload(ctx, l.LeaseID, "w1", 0, make([]byte, 2)); !errors.Is(err, schemas.ErrUploadOffset) {
		t.Fatalf("repeated offset: %v", err)
	}
	if _, err := rw.c.Upload(ctx, l.LeaseID, "w1", 8, make([]byte, 3)); !errors.Is(err, schemas.ErrUploadTooLarge) {
		t.Fatalf("archive past the cap: %v", err)
	}
	if _, err := rw.c.Upload(ctx, l.LeaseID, "w2", 8, make([]byte, 1)); !errors.Is(err, schemas.ErrWorkerUnknown) {
		t.Fatalf("unknown worker: %v", err)
	}
}

// Environment of the two-process test's child processes.
const (
	roleEnv = "HD_REMOTE_ROLE" // coordinator | worker
	dirEnv  = "HD_REMOTE_DIR"  // shared folder for the address and the result
)

// TestRemoteTwoProcesses runs a coordinator and a worker as two processes of
// this test binary, talking HTTP on a random loopback port with their own
// temporary run folders, fake parts and a $0 cost. The coordinator's local
// scheduler and the worker share the experiment.
func TestRemoteTwoProcesses(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns processes")
	}
	dir := t.TempDir()
	start := func(role string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestRemoteProcessHelper$", "-test.v")
		cmd.Env = append(os.Environ(), roleEnv+"="+role, dirEnv+"="+dir)
		out, err := os.Create(filepath.Join(dir, role+".log"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { out.Close() })
		cmd.Stdout, cmd.Stderr = out, out
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return cmd
	}
	coord := start("coordinator")
	worker := start("worker")
	defer func() {
		if worker.Process != nil {
			if err := worker.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Logf("kill worker: %v", err)
			}
			if err := worker.Wait(); err != nil {
				t.Logf("worker exit: %v", err)
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- coord.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("coordinator: %v\n%s\nworker:\n%s", err, readLog(dir, "coordinator.log"), readLog(dir, "worker.log"))
		}
	case <-time.After(90 * time.Second):
		t.Fatal("coordinator did not finish")
	}
	b, err := os.ReadFile(filepath.Join(dir, "summary.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sum Summary
	if err := json.Unmarshal(b, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != 8 || sum.Remote == 0 || sum.Remote == 8 || sum.Errors != 0 || sum.RemoteRejected != 0 {
		t.Fatalf("summary %+v", sum)
	}
}

// TestRemoteProcessHelper is one side of TestRemoteTwoProcesses; it does
// nothing unless HD_REMOTE_ROLE is set.
func TestRemoteProcessHelper(t *testing.T) {
	dir := os.Getenv(dirEnv)
	switch os.Getenv(roleEnv) {
	case "coordinator":
		e := testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 2})
		rw := newRemoteWorld(t, e, RemoteOptions{})
		rw.coord.b.w.execDelay = 300 * time.Millisecond
		if err := os.WriteFile(filepath.Join(dir, "url.tmp"), []byte(rw.url), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(dir, "url.tmp"), filepath.Join(dir, "url")); err != nil {
			t.Fatal(err)
		}
		sum, err := NewScheduler(rw.coord.b.reg, rw.coord.b.root, nil, nil).Run(context.Background(), rw.m,
			RunOptions{Concurrency: 1, Remote: rw.c})
		if err != nil {
			t.Fatal(err)
		}
		for slot, n := range countedRuns(t, rw) {
			if n != 1 {
				t.Fatalf("slot %s has %d counted runs", slot, n)
			}
		}
		if err := rw.coord.st.VerifyChain(rw.m.ExpID); err != nil {
			t.Fatalf("chain: %v", err)
		}
		b, err := json.Marshal(sum)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "summary.json"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	case "worker":
		var url []byte
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if b, err := os.ReadFile(filepath.Join(dir, "url")); err == nil {
				url = b
				break
			}
		}
		if len(url) == 0 {
			t.Fatal("no coordinator address")
		}
		side := newRemoteSide(t)
		w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
			WorkerOptions{Server: string(url), Token: remoteTestToken, WorkerID: "proc-worker", PollEvery: 20 * time.Millisecond,
				ScratchDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Run(context.Background()); err != nil {
			t.Fatal(err)
		}
	default:
		t.Skip("child process of TestRemoteTwoProcesses only")
	}
}

// readLog returns a child's log for a failure message.
func readLog(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return err.Error()
	}
	return string(b)
}
