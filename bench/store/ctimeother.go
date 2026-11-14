//go:build !linux

package store

import "io/fs"

// changeTime is 0 where the bench does not read the inode change time; the
// verification cache then relies on size, modification time and mode.
func changeTime(fs.FileInfo) int64 {
	return 0
}
