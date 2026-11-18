//go:build unix

package evidencestore

import (
	"testing"
	"time"
)

// TestLockExpAcrossMirrors checks that two Mirrors (as two processes would
// be: separate in-process mutexes) still serialize on the lock file.
func TestLockExpAcrossMirrors(t *testing.T) {
	org := newOrg(t, "local")
	a, _ := newDirMirror(t)
	b, _ := newDirMirror(t)
	unlockA, err := a.lockExp(org, "e_1")
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{})
	go func() {
		unlockB, err := b.lockExp(org, "e_1")
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
	if _, err := a.lockExp(org, "../x"); err == nil {
		t.Fatal("bad experiment id accepted")
	}
}
