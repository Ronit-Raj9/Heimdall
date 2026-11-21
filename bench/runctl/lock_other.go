//go:build !unix

package runctl

import "os"

// lockFile is a no-op where flock is unavailable; the process lock still
// serializes one server.
func lockFile(f *os.File) (func(), error) {
	return func() { _ = f.Close() }, nil
}
