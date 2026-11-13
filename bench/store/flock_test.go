//go:build unix

package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockFileExcludesSecondOpen(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.lock")
	open := func() *os.File {
		f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE, 0o644)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	unlock1, err := lockFile(open())
	if err != nil {
		t.Fatalf("lock 1: %v", err)
	}
	acquired := make(chan func() error, 1)
	errs := make(chan error, 1)
	go func() {
		unlock2, err := lockFile(open())
		if err != nil {
			errs <- err
			return
		}
		acquired <- unlock2
	}()
	select {
	case <-acquired:
		t.Fatal("second lock acquired while first is held")
	case err := <-errs:
		t.Fatalf("lock 2: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := unlock1(); err != nil {
		t.Fatalf("unlock 1: %v", err)
	}
	select {
	case unlock2 := <-acquired:
		if err := unlock2(); err != nil {
			t.Fatalf("unlock 2: %v", err)
		}
	case err := <-errs:
		t.Fatalf("lock 2: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("second lock not acquired after release")
	}
}
