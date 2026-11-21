//go:build unix

package evidencestore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestLockExpAcrossMirrors checks that two Mirrors (as two processes would
// be: separate in-process mutexes) still serialize on the lock file.
func TestLockExpAcrossMirrors(t *testing.T) {
	org := newOrg(t, "local")
	a, _ := newDirMirror(t)
	b, _ := newDirMirror(t)
	unlockA, err := a.lockExp(context.Background(), org, "e_1")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		unlockB, err := b.lockExp(context.Background(), org, "e_1")
		if err != nil {
			t.Error(err)
			close(got)
			return
		}
		close(got)
		unlockB()
	}()
	select {
	case <-got:
		t.Fatal("second mirror took the lock while the first held it")
	case <-time.After(150 * time.Millisecond):
	}
	unlockA()
	select {
	case <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("lock not released")
	}
	if _, err := a.lockExp(context.Background(), org, "../x"); err == nil {
		t.Fatal("bad experiment id accepted")
	}
}

// A pass deadline also ends the wait for a lock file another process holds
// (audit 4-C-12).
func TestLockExpHonoursContext(t *testing.T) {
	org := newOrg(t, "local")
	a, _ := newDirMirror(t)
	b, _ := newDirMirror(t)
	unlockA, err := a.lockExp(context.Background(), org, "e_1")
	if err != nil {
		t.Fatal(err)
	}
	defer unlockA()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := b.lockExp(ctx, org, "e_1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held lock file: %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatal("lock wait ignored the deadline")
	}
	// The same mirror's in-process lock is cancellable too.
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if _, err := a.lockExp(ctx2, org, "e_1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("held in-process lock: %v", err)
	}
}

// TestSweepSkipsRecoveryOfBusyExperiment is audit qc2-19-02: a staging
// leftover whose experiment lock is held by a live restore must not stall
// the sweep, and a cancelled context ends recovery at once.
func TestSweepSkipsRecoveryOfBusyExperiment(t *testing.T) {
	old := recoverLockWait
	recoverLockWait = 50 * time.Millisecond
	t.Cleanup(func() { recoverLockWait = old })
	org := newOrg(t, "local")
	a, _ := newDirMirror(t)
	b, _ := newDirMirror(t)
	unlock, err := a.lockExp(context.Background(), org, "e_busy")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if err := os.MkdirAll(filepath.Join(org.root(), stateDir, "staging", "e_busy.x1"), 0o755); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.recover(context.Background(), org) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("recover with a busy experiment: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("recovery blocked on a held experiment lock")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.recover(ctx, org); !errors.Is(err, context.Canceled) {
		t.Fatalf("recover after cancel = %v, want context.Canceled", err)
	}
	if _, err := b.Sweep(context.Background(), []Org{org}); err != nil {
		t.Fatalf("sweep: %v", err)
	}
}
