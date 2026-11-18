//go:build !unix

package traceinbox

import "os"

// flock is a no-op where flock is unavailable; the store's mutex still
// serializes writers within one process.
func flock(f *os.File) (func() error, error) {
	return f.Close, nil
}
