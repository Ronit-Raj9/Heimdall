//go:build unix

package engine

import "syscall"

// diskFree is the space an unprivileged process may still write on the file
// system holding path.
func diskFree(path string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil //nolint:unconvert // field types differ across platforms
}
