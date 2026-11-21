//go:build unix

package engine

import (
	"fmt"
	"os"
	"syscall"
)

// lockSpendFile takes an exclusive advisory lock on the runner spend file's
// lock file, so runner processes sharing one runs root update the daily
// total one at a time (QC2-07-09). The kernel drops it when a process dies.
func lockSpendFile(path string) (func(), error) {
	f, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("spend lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("spend lock %s: %w", f.Name(), err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}
