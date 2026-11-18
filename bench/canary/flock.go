//go:build unix

package canary

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// tryLock takes an exclusive advisory lock on f without waiting; ErrBusy
// when another process holds it. The kernel drops it when the process dies,
// so a crash never leaves a canary locked.
func tryLock(f *os.File) (func() error, error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}
		return nil, fmt.Errorf("flock %s: %w", f.Name(), err)
	}
	return func() error {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			return errors.Join(fmt.Errorf("unlock %s: %w", f.Name(), err), f.Close())
		}
		return f.Close()
	}, nil
}
