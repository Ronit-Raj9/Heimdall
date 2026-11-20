//go:build unix

package faultproxy

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// lockLedger takes an exclusive, non-blocking advisory lock on the ledger
// file for the lifetime of f, so two proxies never append competing chain
// successors to one ledger.
func lockLedger(path string, f *os.File) error {
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return fmt.Errorf("ledger %s is already open by another fault proxy; refusing a second writer", path)
		}
		return fmt.Errorf("lock ledger %s: %w", path, err)
	}
	return nil
}
