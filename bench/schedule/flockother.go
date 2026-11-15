//go:build !unix

package schedule

import "os"

// tryLock is a no-op where flock is unavailable. The daemon's in-process
// guard still keeps one schedule from overlapping itself; a second process
// on the same project is unix-only.
func tryLock(f *os.File) (func() error, error) {
	return f.Close, nil
}
