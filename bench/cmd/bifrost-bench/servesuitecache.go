package main

import (
	"context"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// libraryLoadTimeout bounds one background full load of a library pack. It
// is detached from the request that asked for it, so a slow request never
// cancels a load other requests are waiting on.
const libraryLoadTimeout = 10 * time.Minute

// libraryLoads caches full loads of built-in library packs, process-wide
// (every organization reads the same folders), keyed by folder and
// suite.Stamp. A library pack can hold thousands of tasks and gigabytes of
// audio; loading it walks every task folder, so the suite page, health and
// the runs list must not redo that per request. Org packs are not cached
// here: they are small and edited on the web.
var libraryLoads = struct {
	mu sync.Mutex
	m  map[string]*libraryLoad
}{m: map[string]*libraryLoad{}}

type libraryLoad struct {
	stamp string
	done  chan struct{} // closed when s and err are set
	s     *schemas.Suite
	err   error
}

// SuiteStamp implements the suite list cache key (handlers/suitecache.go):
// the folder id loads from and its suite.Stamp. Org packs fold in their task
// folders, library packs only suite.yaml, suite.lock and tasks/.
func (s serveSuites) SuiteStamp(id string) (dir, stamp string) {
	dir, lib := suite.PickSuiteDir(s.root, s.library, id)
	return dir, suite.Stamp(dir, !lib)
}

// SuiteHead implements the cheap suite list entry: suite.yaml and suite.lock
// only (suite.LoadHead).
func (s serveSuites) SuiteHead(id string) (*schemas.Suite, error) {
	dir, _ := suite.PickSuiteDir(s.root, s.library, id)
	return suite.LoadHead(dir)
}

// loadLibraryCached returns the cached full load of the library pack in dir,
// loading it once per stamp in the background. The caller waits only as
// long as ctx allows; the load keeps going and lands in the cache.
func (s serveSuites) loadLibraryCached(ctx context.Context, dir, id string) (*schemas.Suite, error) {
	stamp := suite.Stamp(dir, false)
	libraryLoads.mu.Lock()
	e := libraryLoads.m[dir]
	if e == nil || e.stamp != stamp {
		e = &libraryLoad{stamp: stamp, done: make(chan struct{})}
		libraryLoads.m[dir] = e
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), libraryLoadTimeout)
			defer cancel()
			st, err := suite.Layered(s.root, s.library)[0].Load(bg, id)
			e.s, e.err = st, err
			close(e.done)
			if err != nil { // a failed load is retried by the next caller
				libraryLoads.mu.Lock()
				if libraryLoads.m[dir] == e {
					delete(libraryLoads.m, dir)
				}
				libraryLoads.mu.Unlock()
			}
		}()
	}
	libraryLoads.mu.Unlock()
	select {
	case <-e.done:
		return e.s, e.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
