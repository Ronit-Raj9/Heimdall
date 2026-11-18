//go:build !unix

package canary

import (
	"os"
	"sync"
)

// held keeps one canary from overlapping itself inside this process where
// flock is unavailable; a second process on the same project is unix-only.
var held sync.Map

// tryLock is an in-process lock keyed by the lock file's name.
func tryLock(f *os.File) (func() error, error) {
	if _, loaded := held.LoadOrStore(f.Name(), true); loaded {
		f.Close()
		return nil, ErrBusy
	}
	return func() error {
		held.Delete(f.Name())
		return f.Close()
	}, nil
}
