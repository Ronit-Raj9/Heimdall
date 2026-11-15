//go:build unix

package schedule

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// tryLock takes an exclusive advisory lock on f without waiting. It returns
// errBusy when another process holds it. The kernel drops the lock when the
// process dies, so a crashed daemon never leaves a schedule locked.
func tryLock(f *os.File) (func() error, error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errBusy
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
