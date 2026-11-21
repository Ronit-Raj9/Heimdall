package engine

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
)

// A chunk whose answer was lost is sent again at the same offset; the
// coordinator acknowledges an identical replay of the last chunk instead of
// refusing every retry (audit 4-C-03).
func TestRemoteUploadReplayOfLastChunk(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{ChunkBytes: 8})
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	ctx := context.Background()
	first, second := []byte("abcdefgh"), []byte("ijk")
	if n, err := rw.c.Upload(ctx, l.LeaseID, "w1", 0, first); err != nil || n != 8 {
		t.Fatal(n, err)
	}
	if n, err := rw.c.Upload(ctx, l.LeaseID, "w1", 0, first); err != nil || n != 8 {
		t.Fatalf("replay of the last chunk: %d %v", n, err)
	}
	if _, err := rw.c.Upload(ctx, l.LeaseID, "w1", 0, []byte("abcdefgX")); !errors.Is(err, schemas.ErrUploadOffset) {
		t.Fatalf("different bytes at an old offset: %v", err)
	}
	if n, err := rw.c.Upload(ctx, l.LeaseID, "w1", 8, second); err != nil || n != 11 {
		t.Fatal(n, err)
	}
	if _, err := rw.c.Upload(ctx, l.LeaseID, "w1", 0, first); !errors.Is(err, schemas.ErrUploadOffset) {
		t.Fatalf("replay of an older chunk: %v", err)
	}
	if n, err := rw.c.Upload(ctx, l.LeaseID, "w1", 8, second); err != nil || n != 11 {
		t.Fatalf("replay of the new last chunk: %d %v", n, err)
	}
}

// faultyStore fails chosen store writes of the coordinator.
type faultyStore struct {
	*store.FSStore
	failWrites atomic.Int32 // WriteRunFile calls of metrics.json still to fail
	failVerify bool
}

func (f *faultyStore) WriteRunFile(ctx context.Context, run *schemas.Run, name string, data []byte) error {
	if name == "metrics.json" && f.failWrites.Add(-1) >= 0 {
		return errors.New("disk hiccup")
	}
	return f.FSStore.WriteRunFile(ctx, run, name, data)
}

func (f *faultyStore) Verify(ctx context.Context, expID, runID string) error {
	if f.failVerify {
		return errors.New("seal mismatch")
	}
	return f.FSStore.Verify(ctx, expID, runID)
}

// A transient copy error after the run folder exists is retried from the
// extracted upload; no half-copied run is left to settle the slot as
// infra_error (audit 4-C-18).
func TestRemoteCopyErrorRetriedWithoutPartialRun(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	fs := &faultyStore{FSStore: rw.coord.st}
	fs.failWrites.Store(1)
	rw.c.store = fs
	done := rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	res := w.Work(context.Background(), l)
	if res == nil || !res.Accepted {
		t.Fatalf("run after a transient copy error: %+v", res)
	}
	recs, err := rw.coord.st.SlotRuns(context.Background(), rw.m.ExpID, l.Slot.SlotID)
	if err != nil || len(recs) != 1 || !recs[0].Sealed || recs[0].Run.Outcome == schemas.OutcomeAbortedInfra {
		t.Fatalf("slot runs after retry: %+v %v", recs, err)
	}
	if sum := done(); sum.Remote != 1 || sum.RemoteRejected != 0 {
		t.Fatalf("summary %+v", sum)
	}
}

// A copy that keeps failing removes its unsealed folder and rejects, so the
// slot is queued again with nothing in the way.
func TestRemoteCopyErrorLeavesNoPartialRun(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	fs := &faultyStore{FSStore: rw.coord.st}
	fs.failWrites.Store(storeAttempts)
	rw.c.store = fs
	done := rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	if res := w.Work(context.Background(), l); res == nil || res.Accepted {
		t.Fatalf("persistent copy error accepted: %+v", res)
	}
	recs, _ := rw.coord.st.SlotRuns(context.Background(), rw.m.ExpID, l.Slot.SlotID)
	if len(recs) != 0 {
		t.Fatalf("half-copied run left: %+v", recs)
	}
	if got := len(rw.r.queue.pending); got != len(rw.m.Slots) {
		t.Fatalf("slot not queued again: pending %d", got)
	}
	done()
}

// A run whose seal does not verify on the coordinator is not accepted and
// not counted (audit 4-C-04).
func TestRemoteUnverifiedSealNotAccepted(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	rw.c.store = &faultyStore{FSStore: rw.coord.st, failVerify: true}
	done := rw.attach(t)
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	res := w.Work(context.Background(), l)
	if res == nil || res.Accepted || !strings.Contains(res.Reason, "does not verify") {
		t.Fatalf("unverified run: %+v", res)
	}
	if got := len(rw.r.queue.pending); got != len(rw.m.Slots)-1 {
		t.Fatalf("unverified slot queued again: pending %d", got)
	}
	if sum := done(); sum.Remote != 0 || sum.Sealed != 0 || sum.RemoteRejected != 1 {
		t.Fatalf("summary %+v", sum)
	}
}

// A lease the coordinator dropped while the runner was still admitting it
// is detected before the slot runs (audit 4-C-13).
func TestRemoteLeaseCheckedBeforeSlotRuns(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	side := newRemoteSide(t)
	side.b.w.execDelay = 5 * time.Second
	w := rw.worker(t, "w1", side)
	l := lease(t, w)
	rw.c.mu.Lock()
	rw.c.loseLocked(rw.c.leases[l.LeaseID], "test: swept during admission")
	rw.c.mu.Unlock()
	start := time.Now()
	if res := w.Work(context.Background(), l); res != nil {
		t.Fatalf("gone lease ran: %+v", res)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("the slot started on a gone lease")
	}
	done()
}

// Uploads left by a coordinator that stopped uncleanly are removed when the
// next one starts; fresh files and journals stay (audit 4-C-05).
func TestCoordinatorReapsStaleStaging(t *testing.T) {
	side := newRemoteSide(t)
	dir := t.TempDir()
	old := time.Now().Add(-2 * stagingReapAfter)
	mk := func(name string, isDir, stale bool) string {
		p := filepath.Join(dir, name)
		var err error
		if isDir {
			err = os.MkdirAll(filepath.Join(p, "inner"), 0o700)
		} else {
			err = os.WriteFile(p, []byte("x"), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
		if stale {
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
		}
		return p
	}
	gone := []string{mk("l_old1.tar.gz", false, true), mk("l_old2", true, true)}
	kept := []string{mk("l_new.tar.gz", false, false), mk("l_new", true, false), mk("e_exp.jsonl", false, true), mk("other", true, true)}
	if _, err := NewCoordinator(side.b.reg, RemoteOptions{Token: remoteTestToken, StagingDir: dir}); err != nil {
		t.Fatal(err)
	}
	for _, p := range gone {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s not reaped: %v", p, err)
		}
	}
	for _, p := range kept {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s removed: %v", p, err)
		}
	}
}

// TestRemoteConcurrentUploadReplays is QC2-08-01: concurrent replays and
// stale-offset chunks decide replay under the lock and release only their
// own guard; afterwards the next chunk still appends (run with -race).
func TestRemoteConcurrentUploadReplays(t *testing.T) {
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{ChunkBytes: 8})
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	ctx := context.Background()
	first := []byte("abcdefgh")
	if n, err := rw.c.Upload(ctx, l.LeaseID, "w1", 0, first); err != nil || n != 8 {
		t.Fatal(n, err)
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 40 {
				if g%2 == 0 {
					rw.c.Upload(ctx, l.LeaseID, "w1", 0, first) //nolint:errcheck // a replay may race another call
				} else {
					rw.c.Upload(ctx, l.LeaseID, "w1", 3, []byte("zz")) //nolint:errcheck // a stale offset is refused
				}
			}
		}()
	}
	wg.Wait()
	if n, err := rw.c.Upload(ctx, l.LeaseID, "w1", 8, []byte("ijk")); err != nil || n != 11 {
		t.Fatalf("next chunk after concurrent replays: %d %v", n, err)
	}
}

// TestConfirmLeaseRequiresPositiveAnswer is QC2-08-02: a coordinator that
// cannot be reached before the slot starts is no permission to run it.
func TestConfirmLeaseRequiresPositiveAnswer(t *testing.T) {
	oldTries, oldBackoff := confirmTries, confirmBackoff
	confirmTries, confirmBackoff = 2, time.Millisecond
	defer func() { confirmTries, confirmBackoff = oldTries, oldBackoff }()
	rw := newRemoteWorld(t, testExperiment(), RemoteOptions{})
	done := rw.attach(t)
	defer done()
	w := rw.worker(t, "w1", newRemoteSide(t))
	l := lease(t, w)
	if why, _ := w.confirmLease(context.Background(), l); why != "" {
		t.Fatalf("held lease not confirmed: %s", why)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "http://" + ln.Addr().String()
	ln.Close()
	w.opts.Server = dead
	why, transient := w.confirmLease(context.Background(), l)
	if why == "" || !transient {
		t.Fatalf("unreachable coordinator: why=%q transient=%v, want a refusal", why, transient)
	}
}
