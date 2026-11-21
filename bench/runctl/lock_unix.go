//go:build unix

package runctl

import (
	"fmt"
	"os"
	"syscall"
)

// lockFile takes an exclusive flock on f; the kernel drops it when the
// process dies, so a crash never leaves the controls locked.
func lockFile(f *os.File) (func(), error) {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("flock %s: %w", f.Name(), err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}
