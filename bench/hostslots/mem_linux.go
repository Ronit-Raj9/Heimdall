//go:build linux

package hostslots

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// hostMemory reads MemTotal from /proc/meminfo, and the cgroup v2 memory
// limit when the worker runs in a container with a lower one; 0 if unknown.
func hostMemory() int64 {
	var total int64
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) >= 2 && fields[0] == "MemTotal:" {
				if kb, err := strconv.ParseInt(fields[1], 10, 64); err == nil {
					total = kb << 10
				}
				break
			}
		}
		f.Close()
	}
	if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if v, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64); err == nil && v > 0 && (total == 0 || v < total) {
			total = v
		}
	}
	return total
}
