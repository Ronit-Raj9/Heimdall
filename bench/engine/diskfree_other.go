//go:build !unix

package engine

import "errors"

// diskFree is not measured on this platform; the free disk floor is off.
func diskFree(string) (int64, error) {
	return 0, errors.New("free disk is not measured on this platform")
}
