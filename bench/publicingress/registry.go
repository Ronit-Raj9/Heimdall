// Package publicingress publishes a worker's model gateway and tool gates to
// agents that run outside the worker's Docker networks: deployed endpoints
// (remote_http, A2A) and cloud sandboxes (Modal, Daytona, E2B). On the hosted
// stack Caddy forwards https://<domain>/gw/... and https://<domain>/rt/...
// to one listener in the shared worker process (Handler), which answers only
// for runs that are live on that worker:
//
//   - /gw/<path> goes to the gateway (Bifrost) only on an inference route and
//     only with the virtual key of a live run (the key Bifrost itself also
//     checks; it is revoked when the run ends).
//   - /rt/r/<run id>/<path> goes to that run's tool gate only while the run
//     is live and only with the run's tool bearer token (the gate checks it
//     again, and refuses every call after the run's deadline).
//
// Each run is rate limited on its own, and requests that fail to
// authenticate are rate limited per client address. Nothing here is
// reachable without a live run: an unknown key, run or token is a 401 or a
// 404, and the answer never says which.
package publicingress

import (
	"crypto/sha256"
	"crypto/subtle"
	"sync"
	"time"
)

// Registry holds the live runs a worker publishes. The engine adds a run's
// key when it is minted and drops the run at cleanup; the Docker environment
// adds a run's tool gate when it starts. Every entry also carries its own
// deadline, so an entry whose drop was missed stops working on time.
type Registry struct {
	mu    sync.Mutex
	now   func() time.Time
	keys  map[[32]byte]keyEntry // sha256 of the virtual key
	gates map[string]gateEntry  // run id
	runs  map[string][][32]byte // run id to its key hashes, for Drop
	rate  map[string]*bucket    // run id
	fails map[string]*bucket    // client address, failed authentication
	limit Limits
}

type keyEntry struct {
	run   string
	until time.Time
}

type gateEntry struct {
	addr  string // host:port the worker reaches the gate on
	token [32]byte
	until time.Time
}

// Limits are the per-run and per-client rates. Zero values use the
// defaults.
type Limits struct {
	// RunPerMinute is how many requests one live run may make per minute,
	// gateway and tools together; RunBurst how many at once.
	RunPerMinute int
	RunBurst     int
	// FailPerMinute is how many failed authentications one client address
	// may make per minute before it is refused outright.
	FailPerMinute int
}

// Default limits: an agent turn is a few model calls and tool calls, so 600
// a minute per run is far above any real agent and still bounds a leaked key.
const (
	DefaultRunPerMinute  = 600
	DefaultRunBurst      = 120
	DefaultFailPerMinute = 30
)

func (l Limits) withDefaults() Limits {
	if l.RunPerMinute <= 0 {
		l.RunPerMinute = DefaultRunPerMinute
	}
	if l.RunBurst <= 0 {
		l.RunBurst = DefaultRunBurst
	}
	if l.FailPerMinute <= 0 {
		l.FailPerMinute = DefaultFailPerMinute
	}
	return l
}

// NewRegistry returns an empty registry with the given limits.
func NewRegistry(l Limits) *Registry {
	return &Registry{now: time.Now, keys: map[[32]byte]keyEntry{}, gates: map[string]gateEntry{},
		runs: map[string][][32]byte{}, rate: map[string]*bucket{}, fails: map[string]*bucket{}, limit: l.withDefaults()}
}

// Default is the process's registry: the engine and the Docker environment
// write to it, and the worker's ingress listener (when it has one) reads it.
// A process without a listener only keeps a few small entries per live run.
var Default = NewRegistry(Limits{})

// SetLimits replaces the limits; existing buckets keep their state.
func (r *Registry) SetLimits(l Limits) {
	r.mu.Lock()
	r.limit = l.withDefaults()
	r.mu.Unlock()
}

// AddKey publishes a live run's virtual key until the given time.
func (r *Registry) AddKey(run, key string, until time.Time) {
	if run == "" || key == "" {
		return
	}
	h := sha256.Sum256([]byte(key))
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked()
	r.keys[h] = keyEntry{run: run, until: until}
	r.runs[run] = append(r.runs[run], h)
}

// AddGate publishes a live run's tool gate, reached at addr, for callers
// holding the token whose sha256 is tokenSHA.
func (r *Registry) AddGate(run, addr string, tokenSHA [32]byte, until time.Time) {
	if run == "" || addr == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gates[run] = gateEntry{addr: addr, token: tokenSHA, until: until}
}

// Drop removes everything published for a run.
func (r *Registry) Drop(run string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, h := range r.runs[run] {
		if e, ok := r.keys[h]; ok && e.run == run {
			delete(r.keys, h)
		}
	}
	delete(r.runs, run)
	delete(r.gates, run)
	delete(r.rate, run)
}

// Live reports how many runs have a key or a gate published.
func (r *Registry) Live() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sweepLocked()
	seen := map[string]bool{}
	for _, e := range r.keys {
		seen[e.run] = true
	}
	for run := range r.gates {
		seen[run] = true
	}
	return len(seen)
}

// runForKey is the live run a key belongs to, or "".
func (r *Registry) runForKey(key string) string {
	if key == "" {
		return ""
	}
	h := sha256.Sum256([]byte(key))
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.keys[h]
	if !ok || !r.now().Before(e.until) {
		return ""
	}
	return e.run
}

// gateFor is a live run's gate address when token is its token.
func (r *Registry) gateFor(run, token string) (string, bool) {
	if run == "" || token == "" {
		return "", false
	}
	h := sha256.Sum256([]byte(token))
	r.mu.Lock()
	defer r.mu.Unlock()
	g, ok := r.gates[run]
	if !ok || !r.now().Before(g.until) || subtle.ConstantTimeCompare(h[:], g.token[:]) != 1 {
		return "", false
	}
	return g.addr, true
}

// allowRun takes one request from a run's bucket.
func (r *Registry) allowRun(run string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.rate[run]
	if b == nil {
		b = newBucket(r.limit.RunPerMinute, r.limit.RunBurst, r.now())
		r.rate[run] = b
	}
	return b.take(r.now())
}

// failed records a failed authentication from client and reports whether
// the client is still under its limit.
func (r *Registry) failed(client string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.fails) > 10000 {
		r.fails = map[string]*bucket{}
	}
	b := r.fails[client]
	if b == nil {
		b = newBucket(r.limit.FailPerMinute, r.limit.FailPerMinute, r.now())
		r.fails[client] = b
	}
	return b.take(r.now())
}

// blocked reports whether client has used up its failed authentications.
func (r *Registry) blocked(client string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := r.fails[client]
	return b != nil && b.empty(r.now())
}

// sweepLocked drops entries past their deadline.
func (r *Registry) sweepLocked() {
	now := r.now()
	for h, e := range r.keys {
		if !now.Before(e.until) {
			delete(r.keys, h)
		}
	}
	for run, g := range r.gates {
		if !now.Before(g.until) {
			delete(r.gates, run)
		}
	}
}

// bucket is a token bucket refilled continuously.
type bucket struct {
	tokens float64
	max    float64
	perSec float64
	last   time.Time
}

func newBucket(perMinute, burst int, now time.Time) *bucket {
	return &bucket{tokens: float64(burst), max: float64(burst), perSec: float64(perMinute) / 60, last: now}
}

func (b *bucket) refill(now time.Time) {
	if d := now.Sub(b.last).Seconds(); d > 0 {
		b.tokens = min(b.max, b.tokens+d*b.perSec)
		b.last = now
	}
}

func (b *bucket) take(now time.Time) bool {
	b.refill(now)
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

func (b *bucket) empty(now time.Time) bool {
	b.refill(now)
	return b.tokens < 1
}
