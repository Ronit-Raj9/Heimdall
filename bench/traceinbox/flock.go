//go:build unix

package traceinbox

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// flock takes an exclusive advisory lock on f, waiting for it. The kernel
// drops the lock when the process dies.
func flock(f *os.File) (func() error, error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("flock %s: %w", f.Name(), err)
	}
	return func() error {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return errors.Join(err, f.Close())
	}, nil
}
