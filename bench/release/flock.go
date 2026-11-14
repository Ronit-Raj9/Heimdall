//go:build unix

package release

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on f, blocking until it is free.
// The kernel drops the lock when the process dies, so a crash never leaves the
// release log locked; that is why this is flock and not a lock file.
func lockFile(f *os.File) (func() error, error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("flock %s: %w", f.Name(), err)
	}
	return func() error {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
			return errors.Join(fmt.Errorf("unlock %s: %w", f.Name(), err), f.Close())
		}
		return f.Close()
	}, nil
}
