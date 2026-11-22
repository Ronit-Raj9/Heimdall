package engine

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

type tlog struct{ t *testing.T }

func (l tlog) Debug(string, ...any)      {}
func (l tlog) Info(m string, kv ...any)  { l.t.Logf("INFO %s %v", m, kv) }
func (l tlog) Warn(m string, kv ...any)  { l.t.Logf("WARN %s %v", m, kv) }
func (l tlog) Error(m string, kv ...any) { l.t.Logf("ERROR %s %v", m, kv) }

// swapServer serves the worker routes of whichever coordinator is current,
// or 502 while none is (the server process is down), on one address, so a
// worker sees one server restart.
type swapServer struct {
	url string
	h   atomic.Pointer[fasthttp.RequestHandler]
}

func newSwapServer(t *testing.T) *swapServer {
	t.Helper()
	s := &swapServer{}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &fasthttp.Server{MaxRequestBodySize: 1 << 20, Handler: func(ctx *fasthttp.RequestCtx) {
		if h := s.h.Load(); h != nil {
			(*h)(ctx)
			return
		}
		ctx.SetStatusCode(fasthttp.StatusBadGateway)
		ctx.SetBodyString("server restarting")
	}}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	s.url = "http://" + ln.Addr().String()
	return s
}

func (s *swapServer) set(api handlers.WorkerAPI) {
	if api == nil {
		s.h.Store(nil)
		return
	}
	r := router.New()
	handlers.NewWorkersHandler(schemas.ModeLive, api).RegisterRoutes(r)
	var h fasthttp.RequestHandler = r.Handler
	s.h.Store(&h)
}

// bookProcess is one server process of a restart test: its coordinator and
// the attached experiment, stopped like serve stops (cause ErrShutdown).
type bookProcess struct {
	c      *Coordinator
	r      *schedRun
	stop   context.CancelCauseFunc
	detach func()
}

func startBookProcess(t *testing.T, cs *remoteSide, m *schemas.Manifest, opts RemoteOptions, resume bool) *bookProcess {
	t.Helper()
	c, err := NewCoordinator(cs.b.reg, opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancelCause(context.Background())
	s := NewScheduler(cs.b.reg, cs.b.root, nil, nil)
	r, err := s.start(ctx, m, RunOptions{Remote: c, Resume: resume, RemoteOnly: resume, StaleAfter: 3 * time.Second, HeartbeatEvery: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	r.ctx, r.queue = ctx, newSlotQueue(m.Slots)
	return &bookProcess{c: c, r: r, stop: stop, detach: c.attach(r)}
}

func (p *bookProcess) summary() Summary {
	p.r.mu.Lock()
	defer p.r.mu.Unlock()
	return p.r.sum
}

func journalEvents(t *testing.T, dir, expID, lease string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, expID+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"lease":"`+lease+`"`) {
			i := strings.Index(line, `"event":"`) + len(`"event":"`)
			out = append(out, line[i:i+strings.Index(line[i:], `"`)])
		}
	}
	return out
}

// TestDurableLeaseSurvivesRestart restarts the server while a remote run is
// in flight: the lease is kept (no stop charge, store lease still held),
// the worker's heartbeats ride through the restart, the new process
// re-attaches the lease when the experiment resumes, and the run is
// accepted and counted once, with no unsettled charge.
func TestDurableLeaseSurvivesRestart(t *testing.T) {
	cs := newRemoteSide(t)
	staging := t.TempDir()
	book := FileLeaseBook{Dir: filepath.Join(staging, "leases")}
	opts := RemoteOptions{Token: remoteTestToken, StagingDir: staging, Leases: book, StaleAfter: 3 * time.Second, Logger: tlog{t}}
	m := cs.b.plan(t, testExperiment())
	srv := newSwapServer(t)

	p1 := startBookProcess(t, cs, m, opts, false)
	srv.set(p1.c)
	side := newRemoteSide(t)
	side.b.w.execDelay = 1500 * time.Millisecond
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: srv.url, Token: remoteTestToken, WorkerID: "w1", Version: BenchVersion, PollEvery: 10 * time.Millisecond, ScratchDir: t.TempDir(), Logger: tlog{t}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := w.register(ctx); err != nil {
		t.Fatal(err)
	}
	l := lease(t, w)
	if l == nil {
		t.Fatal("no lease")
	}
	p1.c.flushBook()
	if recs, _ := book.OpenLeases(ctx, ""); len(recs) != 1 || recs[0].ID != l.LeaseID {
		t.Fatalf("lease book after the lease: %+v", recs)
	}
	result := make(chan *schemas.WorkerResult, 1)
	go func() { result <- w.Work(ctx, l) }()
	time.Sleep(700 * time.Millisecond) // the lease is confirmed and the run started

	// The server stops (SIGTERM: the job runner shuts down) mid-run.
	p1.stop(jobs.ErrShutdown)
	p1.detach()
	if err := p1.c.Heartbeat(ctx, l.LeaseID, "w1"); err != nil {
		t.Fatalf("heartbeat of a kept lease before the process exits: %v", err)
	}
	srv.set(nil)
	p1.c.flushBook()
	if recs, _ := book.OpenLeases(ctx, ""); len(recs) != 1 {
		t.Fatalf("kept lease not open in the book: %+v", recs)
	}
	if ev := journalEvents(t, staging, m.ExpID, l.LeaseID); len(ev) != 1 || ev[0] != "leased" {
		t.Fatalf("journal after the stop: %v (want only leased: nothing charged)", ev)
	}
	time.Sleep(400 * time.Millisecond) // the server is down; heartbeats fail and are retried

	// The new process loads the kept lease; the experiment resumes.
	p2c, err := NewCoordinator(cs.b.reg, opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.set(p2c)
	if err := p2c.Heartbeat(ctx, l.LeaseID, "w1"); err != nil {
		t.Fatalf("heartbeat of a kept lease before its experiment resumed: %v", err)
	}
	if _, err := p2c.Upload(ctx, l.LeaseID, "w1", 0, []byte("x")); err == nil || !strings.Contains(err.Error(), "re-attaching") {
		t.Fatalf("upload before re-attach: %v, want a retry answer", err)
	}
	p2 := &bookProcess{c: p2c}
	{
		pctx, stop := context.WithCancelCause(context.Background())
		s := NewScheduler(cs.b.reg, cs.b.root, nil, nil)
		r, err := s.start(pctx, m, RunOptions{Remote: p2c, Resume: true, RemoteOnly: true, StaleAfter: 3 * time.Second, HeartbeatEvery: 100 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		if r.sum.UnknownCostRuns != 0 || r.sum.SpentUSD != 0 {
			t.Fatalf("the resumed scheduler charged the kept lease: %+v", r.sum)
		}
		r.ctx, r.queue = pctx, newSlotQueue(m.Slots)
		p2.r, p2.stop, p2.detach = r, stop, p2c.attach(r)
	}
	select {
	case res := <-result:
		if res == nil || !res.Accepted {
			t.Fatalf("run across the restart: %+v", res)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the run across the restart did not finish")
	}
	for range len(m.Slots) - 1 {
		l2 := lease(t, w)
		if l2 == nil {
			t.Fatal("no lease for the remaining slots")
		}
		if l2.Slot.SlotID == l.Slot.SlotID {
			t.Fatal("the re-attached slot was leased again")
		}
		if res := w.Work(ctx, l2); res == nil || !res.Accepted {
			t.Fatalf("run: %+v", res)
		}
	}
	p2.detach()
	sum := p2.summary()
	if sum.Reattached != 1 || sum.LeaseLost != 0 || sum.UnknownCostRuns != 0 || sum.Remote != len(m.Slots) {
		t.Fatalf("summary %+v", sum)
	}
	if ev := journalEvents(t, staging, m.ExpID, l.LeaseID); len(ev) != 2 || ev[1] != "accepted" {
		t.Fatalf("journal of the kept lease: %v", ev)
	}
	for _, slot := range m.Slots {
		if n := countedRuns(t, &remoteWorld{coord: cs, m: m})[slot.SlotID]; n != 1 {
			t.Fatalf("slot %s has %d counted runs", slot.SlotID, n)
		}
	}
	p2c.flushBook()
	if recs, _ := book.OpenLeases(ctx, ""); len(recs) != 0 {
		t.Fatalf("open leases after every run: %+v", recs)
	}
}

// TestDurableLeaseLostAfterGrace: a kept lease whose worker never calls
// again is lost once its experiment attaches and the stale threshold
// passes: the slot runs again and the lost run is charged once (lost),
// never twice (unsettled and lost).
func TestDurableLeaseLostAfterGrace(t *testing.T) {
	cs := newRemoteSide(t)
	staging := t.TempDir()
	stale := 1500 * time.Millisecond
	opts := RemoteOptions{Token: remoteTestToken, StagingDir: staging, Leases: FileLeaseBook{Dir: filepath.Join(staging, "leases")}, StaleAfter: stale, Logger: tlog{t}}
	m := cs.b.plan(t, testExperiment())
	srv := newSwapServer(t)
	p1 := startBookProcess(t, cs, m, opts, false)
	srv.set(p1.c)
	ws := newRemoteSide(t)
	w, err := NewWorker(ws.b.reg, NewPipeline(ws.b.reg, ws.b.root, nil), NewPlanner(ws.b.reg, ws.b.root, nil).Plan,
		WorkerOptions{Server: srv.url, Token: remoteTestToken, WorkerID: "w1", Version: BenchVersion, PollEvery: 10 * time.Millisecond, ScratchDir: t.TempDir(), Logger: tlog{t}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := w.register(ctx); err != nil {
		t.Fatal(err)
	}
	l := lease(t, w)
	p1.stop(jobs.ErrShutdown)
	p1.detach()
	p1.c.flushBook()

	p2c, err := NewCoordinator(cs.b.reg, opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.set(p2c)
	pctx, stop := context.WithCancelCause(context.Background())
	defer stop(nil)
	s := NewScheduler(cs.b.reg, cs.b.root, nil, nil)
	r, err := s.start(pctx, m, RunOptions{Remote: p2c, Resume: true, RemoteOnly: true, StaleAfter: stale, HeartbeatEvery: stale / 6})
	if err != nil {
		t.Fatal(err)
	}
	r.ctx, r.queue = pctx, newSlotQueue(m.Slots)
	detach := p2c.attach(r)
	time.Sleep(stale + 300*time.Millisecond) // nobody heartbeats: the sweep loses it
	if err := p2c.Heartbeat(ctx, l.LeaseID, "w1"); err == nil {
		t.Fatal("heartbeat of a lost kept lease accepted")
	}
	for range len(m.Slots) {
		l2 := lease(t, w)
		if l2 == nil {
			t.Fatal("no lease")
		}
		if res := w.Work(ctx, l2); res == nil || !res.Accepted {
			t.Fatalf("run: %+v", res)
		}
	}
	detach()
	r.mu.Lock()
	sum := r.sum
	r.mu.Unlock()
	if sum.LeaseLost != 1 || sum.UnknownCostRuns != 1 || sum.Reattached != 1 || sum.Remote != len(m.Slots) {
		t.Fatalf("summary %+v", sum)
	}
	if ev := journalEvents(t, staging, m.ExpID, l.LeaseID); len(ev) != 2 || ev[1] != "lost" {
		t.Fatalf("journal of the lost kept lease: %v", ev)
	}
}

// TestShutdownWithoutBookEndsLeases: with no lease book a shutdown still
// ends and charges the leases out, as before durable leases.
func TestShutdownWithoutBookEndsLeases(t *testing.T) {
	cs := newRemoteSide(t)
	staging := t.TempDir()
	m := cs.b.plan(t, testExperiment())
	p := startBookProcess(t, cs, m, RemoteOptions{Token: remoteTestToken, StagingDir: staging}, false)
	url := serveWorkers(t, p.c)
	ws := newRemoteSide(t)
	w, err := NewWorker(ws.b.reg, NewPipeline(ws.b.reg, ws.b.root, nil), NewPlanner(ws.b.reg, ws.b.root, nil).Plan,
		WorkerOptions{Server: url, Token: remoteTestToken, WorkerID: "w1", Version: BenchVersion, ScratchDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.register(context.Background()); err != nil {
		t.Fatal(err)
	}
	l := lease(t, w)
	p.stop(jobs.ErrShutdown)
	p.detach()
	if ev := journalEvents(t, staging, m.ExpID, l.LeaseID); len(ev) != 2 || ev[1] != "stopped" {
		t.Fatalf("journal: %v", ev)
	}
}
