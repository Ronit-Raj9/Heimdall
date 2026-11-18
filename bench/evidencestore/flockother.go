//go:build !unix

package evidencestore

import "os"

// flock is a no-op where flock is unavailable; the in-process mutex still
// serializes passes inside one process.
func flock(*os.File) error { return nil }

func funlock(*os.File) error { return nil }
