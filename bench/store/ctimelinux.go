//go:build linux

package store

import (
	"io/fs"
	"syscall"
)

// changeTime returns the file's inode change time in nanoseconds. The kernel
// sets it on every write and metadata change and user space cannot set it, so
// the verification cache can tell a rewritten file even when its size and
// modification time were put back.
func changeTime(info fs.FileInfo) int64 {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return st.Ctim.Sec*1e9 + st.Ctim.Nsec
}
