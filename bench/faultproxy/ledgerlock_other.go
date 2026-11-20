//go:build !unix

package faultproxy

import "os"

// lockLedger is a no-op where flock is unavailable; the proxy image is Linux.
func lockLedger(path string, f *os.File) error { return nil }
