//go:build unix

package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// TestLeaseLockHeldElsewhereIsBounded is 2-A-10: a slot flock held by another
// open file (a suspended process) must not block lease changes forever. A
// cancelled caller returns at once, and an uncancelled one gives up after
// leaseLockWait with ErrLeaseBusy.
func TestLeaseLockHeldElsewhereIsBounded(t *testing.T) {
	s := newTestStore(t, nil)
	mustCreateExp(t, s, "e_test")
	const slot = "s_a1"
	if ok, err := s.Lease(context.Background(), slot, "w1", time.Minute); !ok || err != nil {
		t.Fatalf("lease: %v %v", ok, err)
	}
	dir, err := s.slotDir(slot)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(dir, leaseLockFile), os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := s.Heartbeat(ctx, slot, "w1"); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("heartbeat under held lock: %v", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("cancelled heartbeat took %s", d)
	}

	old := leaseLockWait
	leaseLockWait = 100 * time.Millisecond
	defer func() { leaseLockWait = old }()
	if _, err := s.Lease(context.Background(), slot, "w2", time.Minute); !errors.Is(err, schemas.ErrLeaseBusy) {
		t.Fatalf("lease under held lock: %v, want ErrLeaseBusy", err)
	}
	syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	if err := s.Heartbeat(context.Background(), slot, "w1"); err != nil {
		t.Fatalf("heartbeat after unlock: %v", err)
	}
}
