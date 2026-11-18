package verdict

import (
	"fmt"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/gpu"
	"github.com/maximhq/bifrost/bench/schemas"
)

// InstrumentHardwareParity is the GPU parity check (X7, docs/gpu-tasks.md).
const InstrumentHardwareParity = "hardware_parity"

// withHardwareParity appends the hardware parity check when some counted
// run held a GPU (Run.Hardware); an experiment without GPU runs gets no new
// check, so its analysis reads exactly as before.
func withHardwareParity(checks []schemas.InstrumentCheck, idx runIndex) []schemas.InstrumentCheck {
	if c, ok := hardwareParity(idx); ok {
		return append(checks, c)
	}
	return checks
}

// hardwareParity compares the GPU models the counted runs of every arm ran
// on, from each run's sealed hardware record. A GPU model is the name and
// the memory size (gpu.Model). When the counted GPU runs span more than one
// model the check fails and the experiment is INVALID: a speedup, a GPU time
// or a time to target measured on two different cards is a difference of
// hardware as much as of agents, and the paired test cannot tell the two
// apart. PerArm counts, per arm, the GPU runs on the experiment's most common
// model over its GPU runs. ok is false when no counted run held a GPU.
func hardwareParity(idx runIndex) (schemas.InstrumentCheck, bool) {
	c := schemas.InstrumentCheck{Name: InstrumentHardwareParity, Threshold: "every counted GPU run of every arm on one GPU model", PerArm: map[string]schemas.Ratio{}}
	perArm := map[string]map[string]int{}
	total := map[string]int{}
	for _, arm := range armsIn(idx, "") {
		for _, run := range idx.armRuns("", arm) {
			h := run.Run.Hardware
			if h == nil || len(h.GPUs) == 0 {
				continue
			}
			if perArm[arm] == nil {
				perArm[arm] = map[string]int{}
			}
			seen := map[string]bool{}
			for _, g := range h.GPUs {
				seen[gpu.Model(g)] = true
			}
			models := make([]string, 0, len(seen))
			for m := range seen {
				models = append(models, m)
			}
			sort.Strings(models)
			key := strings.Join(models, " + ")
			perArm[arm][key]++
			total[key]++
		}
	}
	if len(total) == 0 {
		return c, false
	}
	common := ""
	for _, m := range sortedKeys(total) {
		if common == "" || total[m] > total[common] {
			common = m
		}
	}
	for _, arm := range sortedKeys(perArm) {
		var r schemas.Ratio
		for m, n := range perArm[arm] {
			r.Den += n
			if m == common {
				r.Num += n
			}
		}
		c.PerArm[arm] = r
	}
	if len(total) == 1 {
		c.Status = schemas.InstrumentPass
		c.Detail = fmt.Sprintf("every counted GPU run ran on %s", common)
		return c, true
	}
	var parts []string
	for _, arm := range sortedKeys(perArm) {
		var ms []string
		for _, m := range sortedKeys(perArm[arm]) {
			ms = append(ms, fmt.Sprintf("%d on %s", perArm[arm][m], m))
		}
		parts = append(parts, "arm "+arm+": "+strings.Join(ms, ", "))
	}
	c.Status = schemas.InstrumentFail
	c.Detail = "hardware parity: counted GPU runs ran on different GPU models (" + strings.Join(parts, "; ") +
		"), so a difference between the arms could be the hardware; run every arm on one GPU model (gpu.devices, or remote workers with the same GPU)"
	return c, true
}
