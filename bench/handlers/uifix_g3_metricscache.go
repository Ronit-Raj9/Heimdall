package handlers

import (
	"maps"
	"reflect"
	"sync"
)

// sealedMetricsMax bounds the metrics cache. Past it the cache starts empty
// again, which only costs the next reads their file reads.
const sealedMetricsMax = 50000

// metricsCache keeps the parsed metrics.json of sealed runs (UI fix wave,
// G3). The experiments list, the evaluations list and the comparison status
// read the metrics.json of every sealed run on every request; a sealed run
// folder never changes (store.Seal hashes every file and makes the folder
// read-only), so its parsed file is reused.
//
// Invalidation rule: an entry is keyed by the run id AND the run's seal hash
// (the sha256 of its seal.json, which hashes every file of the folder). A run
// without a seal hash (not sealed yet) is never cached, a read error is never
// cached, and a run sealed again or imported with other files has another
// seal hash, so it misses and is read from disk. The key also holds the
// index the file was read through (each organization has its own), so two
// run folders never share an entry; an index whose type cannot be a map key
// is never cached.
type metricsCache struct {
	mu      sync.Mutex
	entries map[metricsKey]runMetricsRead
}

type metricsKey struct {
	index         any
	runID, sealed string
}

// sealedMetrics is the process-wide cache runMetrics reads through.
var sealedMetrics = &metricsCache{entries: map[metricsKey]runMetricsRead{}}

// key returns the cache key, false when the read cannot be cached.
func (c *metricsCache) key(index ReadIndex, runID, sealHash string) (metricsKey, bool) {
	if index == nil || sealHash == "" || !reflect.TypeOf(index).Comparable() {
		return metricsKey{}, false
	}
	return metricsKey{index: index, runID: runID, sealed: sealHash}, true
}

// get returns a copy of the cached read of a sealed run, false when the run
// has no seal hash or is not cached.
func (c *metricsCache) get(index ReadIndex, runID, sealHash string) (runMetricsRead, bool) {
	k, ok := c.key(index, runID, sealHash)
	if !ok {
		return runMetricsRead{}, false
	}
	c.mu.Lock()
	r, ok := c.entries[k]
	c.mu.Unlock()
	if ok && r.values != nil {
		r.values = maps.Clone(r.values) // callers may change their map
	}
	return r, ok
}

// put stores the read of a sealed run; it does nothing without a seal hash
// or for a read error.
func (c *metricsCache) put(index ReadIndex, runID, sealHash string, r runMetricsRead) {
	k, ok := c.key(index, runID, sealHash)
	if !ok || r.err {
		return
	}
	if r.values != nil {
		r.values = maps.Clone(r.values)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= sealedMetricsMax {
		c.entries = map[metricsKey]runMetricsRead{}
	}
	c.entries[k] = r
}
