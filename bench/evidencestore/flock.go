//go:build unix

package evidencestore

import (
	"errors"
	"os"
	"syscall"
)

// tryFlock tries to take an exclusive advisory lock on f without blocking;
// held reports that another process has it.
func tryFlock(f *os.File) (held bool, err error) {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return false, nil
		case errors.Is(err, syscall.EWOULDBLOCK):
			return true, nil
		case !errors.Is(err, syscall.EINTR):
			return false, err
		}
	}
}

func funlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
