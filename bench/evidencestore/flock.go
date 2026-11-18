//go:build unix

package evidencestore

import (
	"errors"
	"os"
	"syscall"
)

// flock takes an exclusive advisory lock on f, blocking until it is free.
func flock(f *os.File) error {
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

func funlock(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }
