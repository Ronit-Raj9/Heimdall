package main

import (
	"path/filepath"
	"slices"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

// lockDriftCacheSize is the most pack drift results kept at once.
const lockDriftCacheSize = 64

// lockDriftKey names one lock drift result: the pack folder, its
// suite.Stamp, the hash of its suite.lock and whether that lock carries
// fingerprints (they change what is hashed).
type lockDriftKey struct {
	dir           string
	stamp         string
	lockHash      string
	fingerprinted bool
}

// lockDriftCache keeps the lock drift of read-only packs, so the suite list
// does not rehash a library pack of thousands of tasks on every refresh. It
// is process-wide: every organization reads the same library folders.
// Errors are not kept, and the oldest entry is dropped first.
type lockDriftCache struct {
	mu    sync.Mutex
	max   int
	m     map[lockDriftKey][]schemas.SuiteDrift
	order []lockDriftKey // oldest first
}

var lockDrifts = newLockDriftCache(lockDriftCacheSize)

func newLockDriftCache(max int) *lockDriftCache {
	return &lockDriftCache{max: max, m: map[lockDriftKey][]schemas.SuiteDrift{}}
}

// get returns a copy of the drift cached under k, computing and keeping it
// on a miss. Two callers that miss at once may both compute.
func (c *lockDriftCache) get(k lockDriftKey, compute func() ([]schemas.SuiteDrift, error)) ([]schemas.SuiteDrift, error) {
	c.mu.Lock()
	d, ok := c.m[k]
	c.mu.Unlock()
	if ok {
		return copyDrift(d), nil
	}
	d, err := compute()
	if err != nil {
		return nil, err
	}
	kept := copyDrift(d)
	c.mu.Lock()
	if _, ok := c.m[k]; !ok {
		c.m[k] = kept
		c.order = append(c.order, k)
		for len(c.order) > c.max {
			delete(c.m, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.mu.Unlock()
	return copyDrift(kept), nil
}

// libraryDriftKey is the cache key of st's lock drift when st was loaded
// from the built-in library folder; ok is false for an organization's own
// pack. Those are hashed on every call: their stamp folds in task.yaml files
// only, so an edit deeper in a task folder would not change it, and they are
// small.
func (s serveSuites) libraryDriftKey(st *schemas.Suite) (lockDriftKey, bool) {
	if s.library == "" || st == nil || st.Root == "" || st.Lock == nil {
		return lockDriftKey{}, false
	}
	dir, lib := suite.PickSuiteDir(s.root, s.library, st.ID)
	if !lib {
		return lockDriftKey{}, false
	}
	a, err1 := filepath.Abs(dir)
	b, err2 := filepath.Abs(st.Root)
	if err1 != nil || err2 != nil || a != b {
		return lockDriftKey{}, false
	}
	return lockDriftKey{dir: a, stamp: suite.Stamp(a, false), lockHash: st.LockHash, fingerprinted: st.Lock.Fingerprints != nil}, true
}

// copyDrift is a deep copy of d, so no caller can change a cached entry.
func copyDrift(d []schemas.SuiteDrift) []schemas.SuiteDrift {
	out := make([]schemas.SuiteDrift, len(d))
	for i, x := range d {
		x.Fields = slices.Clone(x.Fields)
		if x.Fields == nil {
			x.Fields = []string{}
		}
		out[i] = x
	}
	return out
}
