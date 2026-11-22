// Package hostslots is the shared worker's host-wide run limit (Z10,
// INFRA_I4 item 2). The shared worker runs one loop per assigned
// organization, each with its own slots, and nothing capped the host: many
// organizations at once could oversubscribe the machine. A Pool holds the
// host's run slots (sized from its CPUs and memory, 2 vCPU and 4 GB per
// run, docs/hosting.md) and shares them across organizations by weighted
// fair share: when slots are contended, a freed slot goes to the waiting
// organization holding the fewest relative to its weight, oldest waiter
// first among equals. Each organization's own cap (its slots, capped by its
// max concurrent runs) still applies on top, in its loop.
package hostslots

import (
	"context"
	"runtime"
	"sync"
)

// Per-run resources, as docs/hosting.md sizes the shared worker.
const (
	CPUsPerSlot  = 2
	BytesPerSlot = 4 << 30
)

// Size returns how many runs fit on a host with cpus CPUs and mem bytes of
// memory (0 when unknown: CPUs only); at least 1.
func Size(cpus int, mem int64) int {
	n := cpus / CPUsPerSlot
	if mem > 0 {
		n = min(n, int(mem/BytesPerSlot))
	}
	return max(n, 1)
}

// HostSize is Size for this machine (memory from /proc/meminfo on Linux).
func HostSize() int {
	return Size(runtime.NumCPU(), hostMemory())
}

// Pool is a fair counting semaphore over a host's run slots.
type Pool struct {
	mu      sync.Mutex
	cap     int
	total   int
	held    map[string]int
	weight  map[string]int
	waiters []*waiter
}

type waiter struct {
	org   string
	ready chan struct{}
	done  bool // granted (or given up) under mu
}

// New returns a pool of n slots (at least 1).
func New(n int) *Pool {
	return &Pool{cap: max(n, 1), held: map[string]int{}, weight: map[string]int{}}
}

// Cap is the pool's size.
func (p *Pool) Cap() int { return p.cap }

// SetWeight sets an organization's share weight (default 1; at least 1). A
// weight of 2 lets it hold twice the slots of a weight-1 organization when
// both wait.
func (p *Pool) SetWeight(org string, w int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.weight[org] = max(w, 1)
}

// InUse returns the slots held in total and by org.
func (p *Pool) InUse(org string) (total, byOrg int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total, p.held[org]
}

// Acquire takes one slot for org, waiting while the host is full. The
// returned release gives it back (idempotent). It fails only when ctx ends.
func (p *Pool) Acquire(ctx context.Context, org string) (func(), error) {
	p.mu.Lock()
	if p.total < p.cap && len(p.waiters) == 0 {
		p.grantLocked(org)
		p.mu.Unlock()
		return p.releaser(org), nil
	}
	w := &waiter{org: org, ready: make(chan struct{})}
	p.waiters = append(p.waiters, w)
	p.dispatchLocked()
	p.mu.Unlock()
	select {
	case <-w.ready:
		return p.releaser(org), nil
	case <-ctx.Done():
		p.mu.Lock()
		if w.done { // granted just as ctx ended: hand it straight back
			p.mu.Unlock()
			p.releaser(org)()
			return nil, context.Cause(ctx)
		}
		w.done = true
		for i, x := range p.waiters {
			if x == w {
				p.waiters = append(p.waiters[:i], p.waiters[i+1:]...)
				break
			}
		}
		p.mu.Unlock()
		return nil, context.Cause(ctx)
	}
}

func (p *Pool) grantLocked(org string) {
	p.total++
	p.held[org]++
}

func (p *Pool) releaser(org string) func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			p.mu.Lock()
			defer p.mu.Unlock()
			p.total--
			if p.held[org]--; p.held[org] <= 0 {
				delete(p.held, org)
			}
			p.dispatchLocked()
		})
	}
}

// dispatchLocked grants free slots to waiters: each time the waiter whose
// organization holds the fewest slots per unit of weight, oldest first.
func (p *Pool) dispatchLocked() {
	for p.total < p.cap && len(p.waiters) > 0 {
		best := 0
		for i := 1; i < len(p.waiters); i++ {
			if p.share(p.waiters[i].org) < p.share(p.waiters[best].org) {
				best = i
			}
		}
		w := p.waiters[best]
		p.waiters = append(p.waiters[:best], p.waiters[best+1:]...)
		p.grantLocked(w.org)
		w.done = true
		close(w.ready)
	}
}

// share is the slots org holds per unit of its weight.
func (p *Pool) share(org string) float64 {
	w := p.weight[org]
	if w < 1 {
		w = 1
	}
	return float64(p.held[org]) / float64(w)
}

// For returns org's view of the pool, the shape engine.WorkerOptions takes.
func (p *Pool) For(org string) func(ctx context.Context) (func(), error) {
	return func(ctx context.Context) (func(), error) { return p.Acquire(ctx, org) }
}
