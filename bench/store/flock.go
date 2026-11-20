//go:build unix

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// lockFile takes an exclusive advisory lock on f, blocking until it is free.
// The kernel drops the lock when the process dies, so a crash never leaves a
// slot or the seal chain locked; that is why this is flock and not a lock file.
func lockFile(f *os.File) (func() error, error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("flock %s: %w", f.Name(), err)
	}
	return unlocker(f), nil
}

// lockFileCtx is lockFile with nonblocking tries that stop when ctx ends: a
// process suspended while it holds the lock can then never block a caller
// that is cancelled or has a deadline.
func lockFileCtx(ctx context.Context, f *os.File) (func() error, error) {
	wait := time.Millisecond
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return unlocker(f), nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return nil, fmt.Errorf("flock %s: %w", f.Name(), err)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fmt.Errorf("flock %s: %w", f.Name(), context.Cause(ctx))
		case <-t.C:
		}
		wait = min(wait*2, 50*time.Millisecond)
	}
}

func unlocker(f *os.File) func() error {
	return func() error {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			return errors.Join(fmt.Errorf("unlock %s: %w", f.Name(), err), f.Close())
		}
		return f.Close()
	}
}
