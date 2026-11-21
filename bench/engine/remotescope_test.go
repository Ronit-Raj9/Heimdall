package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
	"github.com/valyala/fasthttp"
	"gopkg.in/yaml.v3"
)

// Hosted runners (Runners area, docs/runners.md): one coordinator per
// organization, runner tokens scoped to one organization and one pool.

// Runner tokens of the hosted tests: two of org_a (pools default and gpu),
// one of org_b.
const (
	tokADefault = "hmr_aaaaaaaa_default"
	tokAGPU     = "hmr_bbbbbbbb_gpu"
	tokBDefault = "hmr_cccccccc_orgb"
	tokARevoked = "hmr_dddddddd_revoked"
)

var hostedTokens = map[string]schemas.RunnerScope{
	tokADefault: {OrgID: "org_a", Pool: schemas.DefaultRunnerPool, TokenID: "rtk_a1"},
	tokAGPU:     {OrgID: "org_a", Pool: "gpu", TokenID: "rtk_a2"},
	tokBDefault: {OrgID: "org_b", Pool: schemas.DefaultRunnerPool, TokenID: "rtk_b1"},
}

func hostedVerify(_ context.Context, tok string) (schemas.RunnerScope, error) {
	s, ok := hostedTokens[tok]
	if !ok {
		return s, errors.New("runner token unknown, revoked or expired")
	}
	return s, nil
}

// fakeBook records what a hosted coordinator books.
type fakeBook struct {
	mu   sync.Mutex
	seen []bookEntry
}

type bookEntry struct {
	scope      schemas.RunnerScope
	info       schemas.WorkerInfo
	registered bool
}

func (b *fakeBook) RunnerSeen(_ context.Context, scope schemas.RunnerScope, info schemas.WorkerInfo, registered bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seen = append(b.seen, bookEntry{scope, info, registered})
}

// hostedOrg is one organization's coordinator side, serving the worker
// routes the way the hosted server's org router does.
type hostedOrg struct {
	side *remoteSide
	c    *Coordinator
	url  string
}

func newHostedOrg(t *testing.T, hub *RemoteHub, orgID string) *hostedOrg {
	t.Helper()
	side := newRemoteSide(t)
	c, err := hub.For(orgID, side.b.reg)
	if err != nil {
		t.Fatal(err)
	}
	r := router.New()
	handlers.NewWorkersHandler(schemas.ModeLive, c).WithRunnerVerifier(hostedVerify).RegisterRoutes(r)
	return &hostedOrg{side: side, c: c, url: serveRouter(t, r)}
}

func serveRouter(t *testing.T, r *router.Router) string {
	t.Helper()
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

// attachTo starts an experiment on side and opens it to c, without local
// workers, as remoteWorld.attach does.
func attachTo(t *testing.T, side *remoteSide, c *Coordinator, m *schemas.Manifest, opts RunOptions) (*schedRun, func() *Summary) {
	t.Helper()
	ctx := context.Background()
	s := NewScheduler(side.b.reg, side.b.root, nil, nil)
	r, err := s.start(ctx, m, opts)
	if err != nil {
		t.Fatal(err)
	}
	r.ctx, r.queue = ctx, newSlotQueue(m.Slots)
	detach := c.attach(r)
	return r, func() *Summary {
		detach()
		r.mu.Lock()
		defer r.mu.Unlock()
		out := r.sum
		return &out
	}
}

// hostedRunner is a runner machine with its own bench, registered with tok.
func hostedRunner(t *testing.T, url, tok, id, pool string) *Worker {
	t.Helper()
	side := newRemoteSide(t)
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: url, Token: tok, WorkerID: id, Version: BenchVersion, PollEvery: 10 * time.Millisecond,
			ScratchDir: t.TempDir(), Pool: pool})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.register(context.Background()); err != nil {
		t.Fatal(err)
	}
	return w
}

func pooled(pool string) *schemas.Experiment {
	e := testExperiment()
	if pool != "" {
		e.Runner = &schemas.RunnerSpec{Pool: pool}
	}
	return e
}

func scoped(tok string) context.Context {
	return schemas.WithRunnerScope(context.Background(), hostedTokens[tok])
}

func storeRuns(t *testing.T, side *remoteSide, expID string) int {
	t.Helper()
	runs, err := side.st.SealedRuns(context.Background(), expID, "")
	if err != nil && !errors.Is(err, schemas.ErrNotFound) {
		t.Fatal(err)
	}
	return len(runs)
}

func TestHostedCoordinatorOptions(t *testing.T) {
	side := newRemoteSide(t)
	if _, err := NewCoordinator(side.b.reg, RemoteOptions{OrgID: "org_a", Token: remoteTestToken}); err == nil {
		t.Fatal("hosted coordinator accepted a shared token")
	}
	if _, err := NewCoordinator(side.b.reg, RemoteOptions{}); err == nil {
		t.Fatal("local coordinator without a token")
	}
	hub := NewRemoteHub(RemoteOptions{Token: "ignored"})
	if _, err := hub.For("", side.b.reg); err == nil {
		t.Fatal("hub built a coordinator without an organization")
	}
	c, err := hub.For("org_a", side.b.reg)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := hub.For("org_a", newRemoteSide(t).b.reg); again != c {
		t.Fatal("hub built a second coordinator for one organization")
	}
	if c.OrgID() != "org_a" || c.CheckToken("") || c.CheckToken(remoteTestToken) || c.CheckToken("ignored") {
		t.Fatal("a hosted coordinator accepts shared tokens")
	}
	if want := filepath.Join(side.st.Root(), stagingFolder); c.opts.StagingDir != want {
		t.Fatalf("staging %s, want the organization's store %s", c.opts.StagingDir, want)
	}
}

// TestHostedTokenScope: every call needs a runner scope of the
// coordinator's organization, a registration may not leave its token's pool,
// and another token of the same organization can not act for a runner.
func TestHostedTokenScope(t *testing.T) {
	book := &fakeBook{}
	hub := NewRemoteHub(RemoteOptions{Book: book})
	a := newHostedOrg(t, hub, "org_a")
	m := a.side.b.plan(t, pooled(""))
	_, done := attachTo(t, a.side, a.c, m, RunOptions{})
	defer done()
	ctx := context.Background()
	reg := &schemas.WorkerRegister{WorkerID: "r1", Slots: 1, Version: BenchVersion}

	if _, err := a.c.Register(ctx, reg); !errors.Is(err, schemas.ErrWorkerAuth) {
		t.Fatalf("register without a scope: %v", err)
	}
	if _, err := a.c.Register(scoped(tokBDefault), reg); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("register with another organization's token: %v", err)
	}
	if _, err := a.c.Register(scoped(tokADefault), &schemas.WorkerRegister{WorkerID: "r1", Slots: 1, Pool: "gpu"}); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("register outside the token's pool: %v", err)
	}
	if _, err := a.c.Register(scoped(tokADefault), &schemas.WorkerRegister{WorkerID: "r1", Slots: 1, Pool: "bad pool"}); !errors.Is(err, schemas.ErrWorkerRequest) {
		t.Fatalf("register with a malformed pool: %v", err)
	}
	if _, err := a.c.Register(scoped(tokADefault), reg); err != nil {
		t.Fatal(err)
	}
	l, err := a.c.Lease(scoped(tokADefault), "r1")
	if err != nil || l == nil {
		t.Fatalf("lease: %+v %v", l, err)
	}
	for name, tok := range map[string]string{"other organization": tokBDefault, "other token of the organization": tokAGPU} {
		ctx := scoped(tok)
		if _, err := a.c.Lease(ctx, "r1"); !errors.Is(err, schemas.ErrWorkerScope) {
			t.Fatalf("%s: lease: %v", name, err)
		}
		if err := a.c.Heartbeat(ctx, l.LeaseID, "r1"); !errors.Is(err, schemas.ErrWorkerScope) {
			t.Fatalf("%s: heartbeat: %v", name, err)
		}
		if _, err := a.c.Upload(ctx, l.LeaseID, "r1", 0, []byte("x")); !errors.Is(err, schemas.ErrWorkerScope) {
			t.Fatalf("%s: upload: %v", name, err)
		}
		if _, err := a.c.Complete(ctx, l.LeaseID, &schemas.WorkerComplete{WorkerID: "r1"}); !errors.Is(err, schemas.ErrWorkerScope) {
			t.Fatalf("%s: complete: %v", name, err)
		}
		if err := a.c.Release(ctx, l.LeaseID, &schemas.WorkerRelease{WorkerID: "r1", Reason: "x"}); !errors.Is(err, schemas.ErrWorkerScope) {
			t.Fatalf("%s: release: %v", name, err)
		}
		if _, err := a.c.Manifest(ctx, "r1", m.ExpID); !errors.Is(err, schemas.ErrWorkerScope) {
			t.Fatalf("%s: manifest: %v", name, err)
		}
	}
	// Another token of the same organization can not take the name over
	// (4-B-01): its registration is refused and the lease stays.
	if _, err := a.c.Register(scoped(tokAGPU), &schemas.WorkerRegister{WorkerID: "r1", Slots: 1, Version: BenchVersion}); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("register a taken name with another token: %v", err)
	}
	// The lease survived every refused call.
	if err := a.c.Heartbeat(scoped(tokADefault), l.LeaseID, "r1"); err != nil {
		t.Fatalf("heartbeat after refused calls: %v", err)
	}
	info := a.c.Workers()
	if len(info) != 1 || info[0].Pool != schemas.DefaultRunnerPool || info[0].TokenID != "rtk_a1" {
		t.Fatalf("workers %+v", info)
	}
	book.mu.Lock()
	defer book.mu.Unlock()
	if len(book.seen) == 0 || !book.seen[0].registered || book.seen[0].scope.TokenID != "rtk_a1" || book.seen[0].info.ID != "r1" {
		t.Fatalf("book %+v", book.seen)
	}
}

// TestHostedLeaseOnlyOwnPool: a runner leases only experiments of its
// token's pool; an experiment without a pool runs on pool default.
func TestHostedLeaseOnlyOwnPool(t *testing.T) {
	hub := NewRemoteHub(RemoteOptions{})
	a := newHostedOrg(t, hub, "org_a")
	mDefault := a.side.b.plan(t, pooled(""))
	mGPU := a.side.b.plan(t, pooled("gpu"))
	_, doneGPU := attachTo(t, a.side, a.c, mGPU, RunOptions{})
	_, doneDefault := attachTo(t, a.side, a.c, mDefault, RunOptions{})
	ctx := context.Background()
	wd := hostedRunner(t, a.url, tokADefault, "cpu-1", "")
	wg := hostedRunner(t, a.url, tokAGPU, "gpu-1", "gpu")

	if _, err := a.c.Manifest(scoped(tokADefault), "cpu-1", mGPU.ExpID); !errors.Is(err, schemas.ErrNotFound) {
		t.Fatalf("manifest of another pool's experiment: %v", err)
	}
	for range mDefault.Slots {
		l := lease(t, wd)
		if l == nil || l.ExpID != mDefault.ExpID {
			t.Fatalf("default runner lease %+v, want %s", l, mDefault.ExpID)
		}
		if res := wd.Work(ctx, l); res == nil || !res.Accepted {
			t.Fatalf("run %+v", res)
		}
	}
	if l := lease(t, wd); l != nil {
		t.Fatalf("default runner leased %s of pool gpu", l.ExpID)
	}
	for range mGPU.Slots {
		l := lease(t, wg)
		if l == nil || l.ExpID != mGPU.ExpID {
			t.Fatalf("gpu runner lease %+v, want %s", l, mGPU.ExpID)
		}
		if res := wg.Work(ctx, l); res == nil || !res.Accepted {
			t.Fatalf("run %+v", res)
		}
	}
	if sum := doneDefault(); sum.Remote != len(mDefault.Slots) {
		t.Fatalf("default summary %+v", sum)
	}
	if sum := doneGPU(); sum.Remote != len(mGPU.Slots) {
		t.Fatalf("gpu summary %+v", sum)
	}
	who, err := a.c.Whoami(scoped(tokAGPU))
	if err != nil || who.Mode != "hosted" || who.Pool != "gpu" || len(who.Workers) != 1 || who.Workers[0].ID != "gpu-1" {
		t.Fatalf("whoami %+v %v", who, err)
	}
}

// TestHostedTwoOrgsIsolation: two organizations, two runners. Each runner
// runs only its own organization's experiment into that organization's
// store; a runner token never reaches the other organization's coordinator,
// and an upload for one organization's lease sent to the other is refused.
func TestHostedTwoOrgsIsolation(t *testing.T) {
	hub := NewRemoteHub(RemoteOptions{})
	a, b := newHostedOrg(t, hub, "org_a"), newHostedOrg(t, hub, "org_b")
	mA, mB := a.side.b.plan(t, pooled("")), b.side.b.plan(t, pooled(""))
	_, doneA := attachTo(t, a.side, a.c, mA, RunOptions{})
	_, doneB := attachTo(t, b.side, b.c, mB, RunOptions{})
	ctx := context.Background()

	// org_a's token on org_b's server: refused at the door.
	side := newRemoteSide(t)
	stray, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: b.url, Token: tokADefault, WorkerID: "a-1", PollEvery: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := stray.Run(ctx); !errors.Is(err, schemas.ErrWorkerScope) || !strings.Contains(err.Error(), "403") {
		t.Fatalf("org_a runner on org_b: %v", err)
	}
	// A revoked or unknown token is a 401.
	stray.opts.Token = tokARevoked
	if err := stray.Run(ctx); !errors.Is(err, schemas.ErrWorkerAuth) {
		t.Fatalf("revoked token: %v", err)
	}

	wa := hostedRunner(t, a.url, tokADefault, "a-1", "")
	wb := hostedRunner(t, b.url, tokBDefault, "b-1", "")
	la := lease(t, wa)
	if la == nil || la.ExpID != mA.ExpID {
		t.Fatalf("org_a lease %+v", la)
	}
	// org_a's lease sent to org_b: org_b's coordinator refuses the scope,
	// and over HTTP org_b's router refuses org_a's token.
	if _, err := b.c.Upload(scoped(tokADefault), la.LeaseID, "a-1", 0, []byte("x")); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("upload to the wrong organization: %v", err)
	}
	if _, err := b.c.Complete(scoped(tokADefault), la.LeaseID, &schemas.WorkerComplete{WorkerID: "a-1"}); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("complete on the wrong organization: %v", err)
	}
	stray.opts.Token = tokADefault
	var state schemas.WorkerUploadState
	if err := stray.callRaw(ctx, workerAPI+"/leases/"+la.LeaseID+"/upload?worker_id=a-1&offset=0", []byte("x"), &state); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("upload of org_a's lease to org_b's server: %v", err)
	}
	if err := stray.call(ctx, http.MethodPost, workerAPI+"/leases/"+la.LeaseID+"/complete", schemas.WorkerComplete{WorkerID: "a-1"}, nil); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("complete of org_a's lease on org_b's server: %v", err)
	}
	// org_b's own runner does not know org_a's lease either.
	if _, err := b.c.Upload(scoped(tokBDefault), la.LeaseID, "b-1", 0, []byte("x")); !errors.Is(err, schemas.ErrLeaseGone) {
		t.Fatalf("org_b runner with org_a's lease: %v", err)
	}
	if res := wa.Work(ctx, la); res == nil || !res.Accepted {
		t.Fatalf("org_a run %+v", res)
	}
	for _, w := range []*Worker{wa, wb} {
		for {
			l := lease(t, w)
			if l == nil {
				break
			}
			if res := w.Work(ctx, l); res == nil || !res.Accepted {
				t.Fatalf("run %+v", res)
			}
		}
	}
	if sa, sb := doneA(), doneB(); sa.Remote != len(mA.Slots) || sb.Remote != len(mB.Slots) {
		t.Fatalf("summaries %+v %+v", sa, sb)
	}
	if storeRuns(t, a.side, mA.ExpID) != len(mA.Slots) || storeRuns(t, b.side, mB.ExpID) != len(mB.Slots) {
		t.Fatal("runs missing from their organization's store")
	}
	if storeRuns(t, a.side, mB.ExpID) != 0 || storeRuns(t, b.side, mA.ExpID) != 0 {
		t.Fatal("a run landed in the other organization's store")
	}
	if len(hub.Workers("org_a")) != 1 || len(hub.Workers("org_b")) != 1 || hub.Workers("org_a")[0].ID != "a-1" {
		t.Fatalf("hub workers %+v %+v", hub.Workers("org_a"), hub.Workers("org_b"))
	}
	for _, d := range []string{a.side.st.Root(), b.side.st.Root()} {
		if _, err := os.Stat(filepath.Join(d, stagingFolder)); err != nil {
			t.Fatalf("staging under the organization's runs root: %v", err)
		}
	}
}

// TestHostedLostRunnerReleased: a hosted runner that goes silent loses its
// lease under the existing rules, the slot is re-leased by another runner
// of the pool, and the late upload is refused.
func TestHostedLostRunnerReleased(t *testing.T) {
	stale := 300 * time.Millisecond
	hub := NewRemoteHub(RemoteOptions{StaleAfter: stale})
	a := newHostedOrg(t, hub, "org_a")
	m := a.side.b.plan(t, pooled(""))
	freeze := freezableClock(a.c)
	// r1 never heartbeats (RunSlot outside Work); r2 heartbeats through Work.
	_, done := attachTo(t, a.side, a.c, m, RunOptions{StaleAfter: stale, HeartbeatEvery: stale / 6})
	ctx := context.Background()
	w1 := hostedRunner(t, a.url, tokADefault, "r1", "")
	w2 := hostedRunner(t, a.url, tokADefault, "r2", "")
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
	a.c.Sweep(time.Now())
	freeze()
	l2 := lease(t, w2)
	if l2 == nil || l2.Slot.SlotID != l1.Slot.SlotID {
		t.Fatalf("lost slot not re-leased first: %+v", l2)
	}
	if res2 := w2.Work(ctx, l2); res2 == nil || !res2.Accepted {
		t.Fatalf("re-leased run: %+v", res2)
	}
	if _, err := w1.upload(ctx, l1, res1); !errors.Is(err, schemas.ErrLeaseGone) {
		t.Fatalf("late upload: %v, want ErrLeaseGone", err)
	}
	if sum := done(); sum.LeaseLost != 1 || sum.Remote != 1 {
		t.Fatalf("summary %+v", sum)
	}
}

// TestHostedRemoteOnly: a remote-only run starts no local agent; every slot
// is run by the organization's runner.
func TestHostedRemoteOnly(t *testing.T) {
	hub := NewRemoteHub(RemoteOptions{})
	a := newHostedOrg(t, hub, "org_a")
	m := a.side.b.plan(t, pooled(""))
	w := hostedRunner(t, a.url, tokADefault, "r1", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if err := w.Run(ctx); err != nil {
			t.Errorf("runner: %v", err)
		}
	}()
	if _, err := NewScheduler(a.side.b.reg, a.side.b.root, nil, nil).Run(context.Background(), m, RunOptions{RemoteOnly: true}); err == nil {
		t.Fatal("remote only without a coordinator")
	}
	sum, err := NewScheduler(a.side.b.reg, a.side.b.root, nil, nil).Run(context.Background(), m,
		RunOptions{Concurrency: 4, Remote: a.c, RemoteOnly: true})
	cancel()
	<-stopped
	if err != nil {
		t.Fatal(err)
	}
	if sum.Sealed != len(m.Slots) || sum.Remote != len(m.Slots) {
		t.Fatalf("summary %+v", sum)
	}
	if n := a.side.b.w.count("exec"); n != 0 {
		t.Fatalf("%d agents ran on the hosted server", n)
	}
}

// TestHostedRemoveRunner: removing a runner or revoking its token drops its
// registration and loses its leases at once.
func TestHostedRemoveRunner(t *testing.T) {
	hub := NewRemoteHub(RemoteOptions{})
	a := newHostedOrg(t, hub, "org_a")
	m := a.side.b.plan(t, pooled(""))
	r, done := attachTo(t, a.side, a.c, m, RunOptions{})
	w1 := hostedRunner(t, a.url, tokADefault, "r1", "")
	hostedRunner(t, a.url, tokAGPU, "r2", "gpu")
	if l := lease(t, w1); l == nil {
		t.Fatal("no lease")
	}
	if !hub.Forget("org_a", "r1", "removed by an admin") || hub.Forget("org_a", "r1", "again") || hub.Forget("org_b", "r1", "x") {
		t.Fatal("forget")
	}
	if got := len(r.queue.pending); got != len(m.Slots) {
		t.Fatalf("pending %d after removal, want %d", got, len(m.Slots))
	}
	if _, err := a.c.Lease(scoped(tokADefault), "r1"); !errors.Is(err, schemas.ErrWorkerUnknown) {
		t.Fatalf("removed runner: %v", err)
	}
	if n := hub.ForgetToken("org_a", "rtk_a2", "token revoked"); n != 1 {
		t.Fatalf("forget token removed %d", n)
	}
	if len(a.c.Workers()) != 0 {
		t.Fatalf("workers %+v", a.c.Workers())
	}
	if sum := done(); sum.LeaseLost != 1 {
		t.Fatalf("summary %+v", sum)
	}
}

// TestLocalCoordinatorIgnoresPools: local mode keeps one shared token and
// leases every attached experiment, whatever pool it or the worker names.
func TestLocalCoordinatorIgnoresPools(t *testing.T) {
	rw := newRemoteWorld(t, pooled("gpu"), RemoteOptions{})
	done := rw.attach(t)
	side := newRemoteSide(t)
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: rw.url, Token: remoteTestToken, WorkerID: "w1", Version: BenchVersion, PollEvery: 10 * time.Millisecond,
			ScratchDir: t.TempDir(), Pool: "cpu", Labels: []string{"x86", "region=eu"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.register(context.Background()); err != nil {
		t.Fatal(err)
	}
	l := lease(t, w)
	if l == nil {
		t.Fatal("local worker got no lease of a pooled experiment")
	}
	if res := w.Work(context.Background(), l); res == nil || !res.Accepted {
		t.Fatalf("run %+v", res)
	}
	// A scope on the context changes nothing locally.
	if _, err := rw.c.Lease(scoped(tokBDefault), "w1"); err != nil {
		t.Fatal(err)
	}
	info := rw.c.Workers()
	if len(info) != 1 || info[0].Pool != "cpu" || len(info[0].Labels) != 2 || info[0].TokenID != "" {
		t.Fatalf("workers %+v", info)
	}
	who, err := rw.c.Whoami(context.Background())
	if err != nil || who.Mode != "local" || len(who.Workers) != 1 || who.Open != 1 {
		t.Fatalf("whoami %+v %v", who, err)
	}
	done()
}

// TestRunnerSpendCap: a runner stops leasing once its own spend for the UTC
// day reaches the cap, and the total survives a restart.
func TestRunnerSpendCap(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(schemas.ConditionSpec{ID: "clean", Tasks: schemas.TaskSelector{IDs: []string{"t01", "t02"}}, Trials: 2}), RemoteOptions{})
	done := rw.attach(t)
	side := newRemoteSide(t)
	side.b.w.cost = 0.6
	spendFile := filepath.Join(t.TempDir(), "spend.json")
	w, err := NewWorker(side.b.reg, NewPipeline(side.b.reg, side.b.root, nil), NewPlanner(side.b.reg, side.b.root, nil).Plan,
		WorkerOptions{Server: rw.url, Token: remoteTestToken, WorkerID: "w1", Version: BenchVersion, PollEvery: 10 * time.Millisecond,
			ScratchDir: t.TempDir(), SpendCapUSD: 1.0, SpendFile: spendFile})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		if err := w.Run(ctx); err != nil {
			t.Errorf("worker: %v", err)
		}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for side.b.w.count("exec") < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	// Cancel only once both runs are charged: cancelling while the second
	// run is still measured stops it with an unknown cost, which charges
	// the reserve instead of its 0.60 USD.
	for time.Now().Before(deadline) {
		if sp, err := newRunnerSpend(1.0, spendFile); err == nil && sp.state.Runs >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // a third lease would start here if the cap failed
	cancel()
	<-stopped
	if n := side.b.w.count("exec"); n != 2 {
		t.Fatalf("%d runs under a 1.00 USD cap at 0.60 USD a run, want 2", n)
	}
	again, err := newRunnerSpend(1.0, spendFile)
	if err != nil {
		t.Fatal(err)
	}
	if reached, spent := again.reached(); !reached || spent < 1.19 || spent > 1.21 {
		t.Fatalf("after restart: reached %v spent %v", reached, spent)
	}
	again.now = func() time.Time { return time.Now().Add(24 * time.Hour) }
	if reached, _ := again.reached(); reached {
		t.Fatal("the cap did not reset on the next UTC day")
	}
	done()
}

// TestExperimentRunnerHash: an experiment without runner encodes, and so
// hashes, exactly as before the field existed (JSON and YAML); with a pool
// set the pool is part of the manifest hash; a bad pool is refused at plan
// time.
func TestExperimentRunnerHash(t *testing.T) {
	e := testExperiment()
	raw, err := json.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"runner"`)) {
		t.Fatalf("an experiment without a runner pool encodes one: %s", raw)
	}
	y, err := yaml.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(y, []byte("runner")) {
		t.Fatalf("an experiment without a runner pool encodes one in YAML: %s", y)
	}
	if e.RunnerPool() != schemas.DefaultRunnerPool || (&schemas.Experiment{Runner: &schemas.RunnerSpec{}}).RunnerPool() != schemas.DefaultRunnerPool {
		t.Fatal("default pool")
	}
	without := schemas.Manifest{ExpID: "e_x", Experiment: schemas.Experiment{ID: "e_x", Name: "x"}}
	with := without
	with.Experiment.Runner = &schemas.RunnerSpec{Pool: "gpu"}
	h1, err := utils.HashJSON(without)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := utils.HashJSON(with)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h2 {
		t.Fatal("runner.pool does not change the manifest hash")
	}
	var back schemas.Experiment
	if err := yaml.Unmarshal([]byte("name: x\nrunner:\n  pool: gpu\n"), &back); err != nil || back.RunnerPool() != "gpu" {
		t.Fatalf("yaml runner.pool: %+v %v", back.Runner, err)
	}
	b := newBench(t)
	bad := pooled("has space")
	if _, err := NewPlanner(b.reg, b.root, nil).Plan(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "runner pool") {
		t.Fatalf("bad pool planned: %v", err)
	}
	m := b.plan(t, pooled("gpu"))
	if m.Experiment.RunnerPool() != "gpu" {
		t.Fatalf("planned pool %q", m.Experiment.RunnerPool())
	}
}

// TestHostedSameNameOtherTokenRefused is audit qc2-17-05: a registration
// under a name another runner token already holds is not a restart, so it
// is refused and the first runner keeps its registration.
func TestHostedSameNameOtherTokenRefused(t *testing.T) {
	hub := NewRemoteHub(RemoteOptions{})
	a := newHostedOrg(t, hub, "org_a")
	reg := &schemas.WorkerRegister{WorkerID: "w-shared", Slots: 1}
	if _, err := a.c.Register(scoped(tokADefault), reg); err != nil {
		t.Fatal(err)
	}
	if _, err := a.c.Register(scoped(tokAGPU), &schemas.WorkerRegister{WorkerID: "w-shared", Slots: 1}); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("same name, other token = %v, want ErrWorkerScope", err)
	}
	ws := a.c.Workers()
	if len(ws) != 1 || ws[0].TokenID != "rtk_a1" {
		t.Fatalf("workers after refused takeover = %+v", ws)
	}
	if _, err := a.c.Register(scoped(tokADefault), reg); err != nil {
		t.Fatalf("restart under the same token: %v", err)
	}
}
