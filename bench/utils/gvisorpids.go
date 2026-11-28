package utils

import "strconv"

// GVisorMinPids is the least --pids-limit a container under gVisor (runsc)
// gets. Under runsc the sandbox's own processes and Go threads (sentry,
// gofer, prewarmer) are counted in the container's pids cgroup, so a limit
// that is fine for runc (8, 16, 64) can be used up by gVisor itself before
// the workload starts, more often on a busy host where the Go runtime starts
// more threads. The start then fails with "fork/exec
// /usr/bin/gvisor-bin/gvisor-sentry-prewarmer: resource temporarily
// unavailable" (round 4; a suspicion until a busy run shows fewer failures).
const GVisorMinPids = 512

// RaisePidsForGVisor returns args with every --pids-limit below GVisorMinPids
// raised to it when the call runs under runtime runsc (an explicit
// --runtime in args wins over runtime). Other runtimes keep their limits.
func RaisePidsForGVisor(args []string, runtime string) []string {
	for i, a := range args {
		if a == "--runtime" && i+1 < len(args) {
			runtime = args[i+1]
		} else if v, ok := cutFlag(a, "--runtime"); ok {
			runtime = v
		}
	}
	if runtime != "runsc" {
		return args
	}
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--pids-limit" && i+1 < len(args) {
			out = append(out, a, raisePids(args[i+1]))
			i++
			continue
		}
		if v, ok := cutFlag(a, "--pids-limit"); ok {
			a = "--pids-limit=" + raisePids(v)
		}
		out = append(out, a)
	}
	return out
}

func cutFlag(a, name string) (string, bool) {
	if len(a) > len(name)+1 && a[:len(name)+1] == name+"=" {
		return a[len(name)+1:], true
	}
	return "", false
}

func raisePids(v string) string {
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 || n >= GVisorMinPids { // unparsable or unlimited: as given
		return v
	}
	return strconv.Itoa(GVisorMinPids)
}
