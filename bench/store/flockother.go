//go:build !unix

package store

import "os"

// lockFile is a no-op where flock is unavailable. The in-process mutexes still
// serialize one scheduler; several processes on one store are unix-only.
func lockFile(f *os.File) (func() error, error) {
	return f.Close, nil
}
