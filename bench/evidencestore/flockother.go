//go:build !unix

package evidencestore

import "os"

// tryFlock is a no-op where flock is unavailable; the in-process mutex
// still serializes passes inside one process.
func tryFlock(*os.File) (bool, error) { return false, nil }

func funlock(*os.File) error { return nil }
