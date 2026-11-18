// Package tenancy makes organization isolation structural in the hosted
// platform (docs/platform-design.md, Tenancy). Every hosted API request is
// resolved to one organization before any handler runs; the handlers it
// reaches belong to an OrgRuntime built over that organization's folders
// only, so a handler holds no reference to another organization's data.
//
// The package owns the Registry (org id to OrgRuntime, built lazily, cached,
// evicted), the folder layout, the role policy table (policy.go), org
// selection (resolve.go) and the hosted dispatcher (dispatch.go). It does not
// build handlers itself: cmd/bifrost-bench supplies a Builder that assembles
// server.Deps for one org root, the same function local mode uses once.
package tenancy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/auth"
	"github.com/valyala/fasthttp"
)

// LocalOrgID is the one organization of local mode.
const LocalOrgID = auth.LocalOrgID

// Org is the tenancy view of an organization row.
type Org struct {
	ID         string `json:"id"`
	Slug       string `json:"slug"`
	Name       string `json:"name"`
	PublicRead bool   `json:"public_read"`
}

// OrgRuntime is everything that serves one organization: its folders and the
// router built from server.Deps over them. Fields other than Handler are
// informational; Close releases what the builder opened.
type OrgRuntime struct {
	Org   Org
	Paths OrgPaths
	// Handler serves the org-scoped API routes (everything under /api/bench
	// that is not a platform route) for this org only.
	Handler fasthttp.RequestHandler
	// Routes lists the registered patterns by method (router.List()).
	Routes map[string][]string
	// Busy, when set, reports work in flight that eviction must not cut
	// (a running experiment's live event fan-out). A busy runtime is kept.
	Busy func() bool
	// Close releases the runtime. Called once, after the last request using
	// the runtime has finished.
	Close func() error
}

// Builder builds the runtime of one org. It is called at most once per org
// at a time; concurrent requests for the same org wait for that call.
type Builder func(ctx context.Context, org Org, paths OrgPaths) (*OrgRuntime, error)

// Options tune the registry cache.
type Options struct {
	// MaxOrgs caps the cached runtimes; the least recently used idle one is
	// evicted past it. 0 means 64.
	MaxOrgs int
	// IdleTTL evicts a runtime unused for this long on the next Sweep. 0
	// means 30 minutes; negative never evicts on idleness.
	IdleTTL time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Registry maps org ids to runtimes. It is safe for concurrent use.
type Registry struct {
	layout Layout
	build  Builder
	opts   Options

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
}

// entry is one cached (or building) runtime.
type entry struct {
	ready    chan struct{} // closed when the build finished
	rt       *OrgRuntime
	err      error
	refs     int
	lastUsed time.Time
	evicted  bool // out of the map; closed when refs drops to 0
	closed   bool
}

// ErrRegistryClosed is returned after Close.
var ErrRegistryClosed = errors.New("tenancy registry is closed")

// NewRegistry returns a registry that builds runtimes with build over the
// folders layout gives.
func NewRegistry(layout Layout, build Builder, opts Options) *Registry {
	if opts.MaxOrgs <= 0 {
		opts.MaxOrgs = 64
	}
	if opts.IdleTTL == 0 {
		opts.IdleTTL = 30 * time.Minute
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Registry{layout: layout, build: build, opts: opts, entries: map[string]*entry{}}
}

// Layout returns the registry's folder layout.
func (r *Registry) Layout() Layout { return r.layout }

// Acquire returns the runtime of org, building it on first use, and a
// release func the caller must call when its request is done. The runtime
// is never closed while acquired.
func (r *Registry) Acquire(ctx context.Context, org Org) (*OrgRuntime, func(), error) {
	paths, err := r.layout.Paths(org.ID)
	if err != nil {
		return nil, nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, nil, ErrRegistryClosed
	}
	e, ok := r.entries[org.ID]
	if !ok {
		e = &entry{ready: make(chan struct{})}
		r.entries[org.ID] = e
		e.refs++ // the builder's hold, handed to this caller on success
		r.mu.Unlock()
		r.runBuild(ctx, e, org, paths)
		r.mu.Lock()
	} else {
		e.refs++
		r.mu.Unlock()
		select {
		case <-e.ready:
		case <-ctx.Done():
			r.release(org.ID, e)
			return nil, nil, ctx.Err()
		}
		r.mu.Lock()
	}
	if e.err != nil {
		err := e.err
		e.refs--
		r.mu.Unlock()
		return nil, nil, err
	}
	e.lastUsed = r.opts.Now()
	r.mu.Unlock()
	r.evictOverflow()
	var once sync.Once
	return e.rt, func() { once.Do(func() { r.release(org.ID, e) }) }, nil
}

// runBuild runs the builder outside the lock. A failed build is dropped from
// the map so the next request tries again.
func (r *Registry) runBuild(ctx context.Context, e *entry, org Org, paths OrgPaths) {
	var rt *OrgRuntime
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("build org %s: panic: %v", org.ID, p)
			}
		}()
		rt, err = r.build(context.WithoutCancel(ctx), org, paths)
	}()
	if err == nil && (rt == nil || rt.Handler == nil) {
		err = fmt.Errorf("build org %s: builder returned no handler", org.ID)
	}
	closeNow := false
	r.mu.Lock()
	if err != nil {
		e.err = fmt.Errorf("org %s: %w", org.ID, err)
		if r.entries[org.ID] == e {
			delete(r.entries, org.ID)
		}
		e.evicted = true
	} else {
		rt.Org, rt.Paths = org, paths
		e.rt = rt
		if r.closed {
			// Closed while building: hand nothing out.
			e.err = ErrRegistryClosed
			e.evicted, e.closed, closeNow = true, true, true
		}
	}
	close(e.ready)
	r.mu.Unlock()
	if closeNow {
		closeRuntime(rt)
	}
}

// release drops one hold on e and closes it when it was evicted and this
// was the last hold.
func (r *Registry) release(orgID string, e *entry) {
	r.mu.Lock()
	e.refs--
	e.lastUsed = r.opts.Now()
	shouldClose := e.evicted && e.refs <= 0 && !e.closed && e.rt != nil
	if shouldClose {
		e.closed = true
	}
	r.mu.Unlock()
	if shouldClose {
		closeRuntime(e.rt)
	}
}

func closeRuntime(rt *OrgRuntime) {
	if rt != nil && rt.Close != nil {
		_ = rt.Close()
	}
}

// Evict drops org's runtime from the cache. It is closed now when no request
// holds it, else when the last one finishes. The next request rebuilds it:
// call Evict after an import or a project change that the runtime caches.
func (r *Registry) Evict(orgID string) {
	r.mu.Lock()
	e, ok := r.entries[orgID]
	if !ok {
		r.mu.Unlock()
		return
	}
	select {
	case <-e.ready:
	default:
		// Still building: let it finish; mark it so the result is not kept.
		delete(r.entries, orgID)
		e.evicted = true
		r.mu.Unlock()
		return
	}
	delete(r.entries, orgID)
	e.evicted = true
	shouldClose := e.refs <= 0 && !e.closed && e.rt != nil
	if shouldClose {
		e.closed = true
	}
	r.mu.Unlock()
	if shouldClose {
		closeRuntime(e.rt)
	}
}

// Sweep evicts runtimes idle for longer than IdleTTL that are not busy.
// The server calls it on a ticker.
func (r *Registry) Sweep() {
	if r.opts.IdleTTL < 0 {
		return
	}
	now := r.opts.Now()
	for _, id := range r.idle(func(e *entry) bool { return now.Sub(e.lastUsed) > r.opts.IdleTTL }) {
		r.Evict(id)
	}
}

// evictOverflow evicts least recently used idle runtimes past MaxOrgs.
func (r *Registry) evictOverflow() {
	r.mu.Lock()
	over := len(r.entries) - r.opts.MaxOrgs
	r.mu.Unlock()
	if over <= 0 {
		return
	}
	ids := r.idle(func(*entry) bool { return true })
	for i := 0; i < over && i < len(ids); i++ {
		r.Evict(ids[i])
	}
}

// idle lists ready, unheld, not busy entries matching keep, least recently
// used first.
func (r *Registry) idle(keep func(*entry) bool) []string {
	type cand struct {
		id string
		at time.Time
		rt *OrgRuntime
	}
	r.mu.Lock()
	var cs []cand
	for id, e := range r.entries {
		select {
		case <-e.ready:
		default:
			continue
		}
		if e.refs > 0 || e.rt == nil || !keep(e) {
			continue
		}
		cs = append(cs, cand{id, e.lastUsed, e.rt})
	}
	r.mu.Unlock()
	sort.Slice(cs, func(i, j int) bool { return cs[i].at.Before(cs[j].at) })
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		if c.rt.Busy != nil && c.rt.Busy() {
			continue
		}
		out = append(out, c.id)
	}
	return out
}

// Cached lists the org ids with a runtime in the cache, sorted.
func (r *Registry) Cached() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.entries))
	for id := range r.entries {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Close evicts every runtime and refuses new acquisitions. Runtimes still
// held close when released.
func (r *Registry) Close() error {
	r.mu.Lock()
	r.closed = true
	ids := make([]string, 0, len(r.entries))
	for id := range r.entries {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.Evict(id)
	}
	return nil
}
