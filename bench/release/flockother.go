//go:build !unix

package release

import "os"

// lockFile is a no-op where flock is unavailable. The in-process mutexes still
// serialize one process; several processes on one log are unix-only.
func lockFile(f *os.File) (func() error, error) {
	return f.Close, nil
}
