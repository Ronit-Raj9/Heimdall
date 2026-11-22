package engine

import (
	"sort"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// X2 fair leases. The coordinator used to offer experiments to a worker in
// attach order, and a slot whose run ended aborted_infra went straight back
// to the head of its queue, so the oldest experiment held every host slot
// through all its retries while newer ones waited.

// infraRetryBase is how long a slot whose run ended aborted_infra waits
// before a remote worker may lease it again, per attempt so far.
const infraRetryBase = 30 * time.Second

// infraRetryDelay is the wait after attempt n ended aborted_infra: attempt 1
// waits infraRetryBase, attempt 2 twice that.
func infraRetryDelay(n int) time.Duration {
	return infraRetryBase * time.Duration(max(n, 1))
}

// fairOrderLocked sorts the experiments a worker may lease from: fewest
// leases out first, then the one whose last lease is oldest (never leased
// first), then attach order. The coordinator's mu is held.
func (c *Coordinator) fairOrderLocked(exps []*schedRun) []*schedRun {
	if len(exps) < 2 {
		return exps
	}
	out := make(map[*schedRun]int, len(exps))
	for _, l := range c.leases {
		out[l.r]++
	}
	sort.SliceStable(exps, func(i, j int) bool {
		a, b := exps[i], exps[j]
		if out[a] != out[b] {
			return out[a] < out[b]
		}
		return a.lastLeased.Before(b.lastLeased)
	})
	return exps
}

// retryWaitMatch wraps match so a slot still inside its retry wait
// (notBefore) is not taken: it stays queued and the worker's slot goes to
// other work. Expired waits are dropped. The waits are read once, before the
// queue's lock is taken, since requeue holds mu while it takes that lock.
func (r *schedRun) retryWaitMatch(match func(*schemas.Slot) bool) func(*schemas.Slot) bool {
	r.mu.Lock()
	var wait map[string]bool
	if len(r.notBefore) > 0 {
		now := time.Now()
		if r.retryNow != nil {
			now = r.retryNow()
		}
		for id, at := range r.notBefore {
			if now.Before(at) {
				if wait == nil {
					wait = map[string]bool{}
				}
				wait[id] = true
			} else {
				delete(r.notBefore, id)
			}
		}
	}
	r.mu.Unlock()
	if wait == nil {
		return match
	}
	return func(s *schemas.Slot) bool {
		return !wait[s.SlotID] && (match == nil || match(s))
	}
}
