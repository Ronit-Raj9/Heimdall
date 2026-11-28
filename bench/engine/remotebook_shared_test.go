package engine

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/fasthttp/router"
	"github.com/maximhq/bifrost/bench/handlers"
	"github.com/maximhq/bifrost/bench/jobs"
	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/valyala/fasthttp"
)

// The shared worker claims a fresh token at every start (sharedpool
// ClaimToken revokes the earlier one), so after a restart it calls with a
// token id the lease book never saw.
const (
	tokShared1 = "hmr_eeeeeeee_shared1"
	tokShared2 = "hmr_ffffffff_shared2"
)

func sharedVerify(ctx context.Context, tok string) (schemas.RunnerScope, error) {
	switch tok {
	case tokShared1:
		return schemas.RunnerScope{OrgID: "org_a", Pool: schemas.SharedRunnerPool, TokenID: "rtk_s1"}, nil
	case tokShared2:
		return schemas.RunnerScope{OrgID: "org_a", Pool: schemas.SharedRunnerPool, TokenID: "rtk_s2"}, nil
	}
	return hostedVerify(ctx, tok)
}

func (s *swapServer) setHosted(api handlers.WorkerAPI) {
	r := router.New()
	handlers.NewWorkersHandler(schemas.ModeLive, api).WithRunnerVerifier(sharedVerify).RegisterRoutes(r)
	var h fasthttp.RequestHandler = r.Handler
	s.h.Store(&h)
}

type hostedRestart struct {
	srv  *swapServer
	c    *Coordinator
	r    *schedRun
	stop context.CancelCauseFunc
	det  func()
	m    *schemas.Manifest
	l    *schemas.WorkerLease
	stg  string
}

// restartWithKeptLease leases one slot to shared-runner-org_a under
// tokShared1, restarts the server (new coordinator from the same lease book)
// and resumes the experiment, as the incident did.
func restartWithKeptLease(t *testing.T, stale time.Duration) *hostedRestart {
	t.Helper()
	cs := newRemoteSide(t)
	staging := t.TempDir()
	opts := RemoteOptions{OrgID: "org_a", StagingDir: staging, Leases: FileLeaseBook{Dir: filepath.Join(staging, "leases")}, StaleAfter: stale, Logger: tlog{t}}
	m := cs.b.plan(t, pooled(schemas.SharedRunnerPool))
	srv := newSwapServer(t)
	p1 := startBookProcess(t, cs, m, opts, false)
	srv.setHosted(p1.c)
	w1 := hostedRunner(t, srv.url, tokShared1, "shared-runner-org_a", "")
	l := lease(t, w1)
	if l == nil {
		t.Fatal("no lease")
	}
	p1.stop(jobs.ErrShutdown)
	p1.detach()
	p1.c.flushBook()
	srv.set(nil)

	c2, err := NewCoordinator(cs.b.reg, opts)
	if err != nil {
		t.Fatal(err)
	}
	srv.setHosted(c2)
	pctx, stop := context.WithCancelCause(context.Background())
	t.Cleanup(func() { stop(nil) })
	s := NewScheduler(cs.b.reg, cs.b.root, nil, nil)
	r, err := s.start(pctx, m, RunOptions{Remote: c2, Resume: true, RemoteOnly: true, StaleAfter: stale, HeartbeatEvery: stale / 6})
	if err != nil {
		t.Fatal(err)
	}
	r.ctx, r.queue = pctx, newSlotQueue(m.Slots)
	det := c2.attach(r)
	return &hostedRestart{srv: srv, c: c2, r: r, stop: stop, det: det, m: m, l: l, stg: staging}
}

// TestSharedWorkerRenewedTokenAfterRestart is the incident: the worker
// entry restored from the kept lease must not lock out the shared worker,
// which comes back with a new token of the same organization and pool.
func TestSharedWorkerRenewedTokenAfterRestart(t *testing.T) {
	h := restartWithKeptLease(t, 3*time.Second)
	if ws := h.c.Workers(); len(ws) != 1 || ws[0].ID != "shared-runner-org_a" {
		t.Fatalf("workers after the restart = %+v", ws)
	}
	// Another organization's token and a token of another pool are still
	// refused while only the placeholder holds the name.
	if _, err := h.c.Register(schemas.WithRunnerScope(context.Background(), hostedTokens[tokBDefault]), &schemas.WorkerRegister{WorkerID: "shared-runner-org_a", Slots: 1}); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("other organization = %v, want ErrWorkerScope", err)
	}
	if _, err := h.c.Register(scoped(tokADefault), &schemas.WorkerRegister{WorkerID: "shared-runner-org_a", Slots: 1}); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("own runner of another pool = %v, want ErrWorkerScope", err)
	}

	w2 := hostedRunner(t, h.srv.url, tokShared2, "shared-runner-org_a", "")
	ctx := context.Background()
	if err := h.c.Heartbeat(schemas.WithRunnerScope(ctx, schemas.RunnerScope{OrgID: "org_a", Pool: schemas.SharedRunnerPool, TokenID: "rtk_s2"}), h.l.LeaseID, w2.ID()); err == nil {
		t.Fatal("the old process's kept lease is still live after the worker registered again")
	}
	for range len(h.m.Slots) {
		l2 := lease(t, w2)
		if l2 == nil {
			t.Fatal("no lease after the renewed register")
		}
		if res := w2.Work(ctx, l2); res == nil || !res.Accepted {
			t.Fatalf("run: %+v", res)
		}
	}
	// Now a live registration: a third token of the same pool is refused.
	if _, err := h.c.Register(schemas.WithRunnerScope(ctx, schemas.RunnerScope{OrgID: "org_a", Pool: schemas.SharedRunnerPool, TokenID: "rtk_s3"}),
		&schemas.WorkerRegister{WorkerID: "shared-runner-org_a", Slots: 1}); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("takeover of a live registration = %v, want ErrWorkerScope", err)
	}
	h.det()
	h.r.mu.Lock()
	sum := h.r.sum
	h.r.mu.Unlock()
	if sum.LeaseLost != 1 || sum.Remote != len(h.m.Slots) {
		t.Fatalf("summary %+v", sum)
	}
}

// TestRestoredPlaceholderConfirmedByOldToken: a runner that kept its token
// across the restart and calls again is a live registration, so another
// token cannot adopt its name.
func TestRestoredPlaceholderConfirmedByOldToken(t *testing.T) {
	h := restartWithKeptLease(t, 3*time.Second)
	s1 := schemas.WithRunnerScope(context.Background(), schemas.RunnerScope{OrgID: "org_a", Pool: schemas.SharedRunnerPool, TokenID: "rtk_s1"})
	if err := h.c.Heartbeat(s1, h.l.LeaseID, "shared-runner-org_a"); err != nil {
		t.Fatalf("old token heartbeat: %v", err)
	}
	s2 := schemas.WithRunnerScope(context.Background(), schemas.RunnerScope{OrgID: "org_a", Pool: schemas.SharedRunnerPool, TokenID: "rtk_s2"})
	if _, err := h.c.Register(s2, &schemas.WorkerRegister{WorkerID: "shared-runner-org_a", Slots: 1}); !errors.Is(err, schemas.ErrWorkerScope) {
		t.Fatalf("takeover of a confirmed runner = %v, want ErrWorkerScope", err)
	}
}

// TestRestoredPlaceholderGoneWithLease: nobody calls after the restart, the
// kept lease is lost, and the placeholder entry is removed with it.
func TestRestoredPlaceholderGoneWithLease(t *testing.T) {
	stale := 1500 * time.Millisecond
	h := restartWithKeptLease(t, stale)
	deadline := time.Now().Add(5 * stale)
	for len(h.c.Workers()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("placeholder still there: %+v", h.c.Workers())
		}
		time.Sleep(50 * time.Millisecond)
	}
	h.r.mu.Lock()
	lost := h.r.sum.LeaseLost
	h.r.mu.Unlock()
	if lost != 1 {
		t.Fatalf("lease_lost = %d", lost)
	}
}
