package forecast

import (
	"context"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/leaderboard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// historyCacheMax bounds a HistoryCache; past it, expired entries go first,
// then everything.
const historyCacheMax = 128

// HistoryCache keeps the sealed history of a suite version per store for a
// short time, so the estimates the evaluation wizard asks for on every
// change do not rescan every manifest and sealed run of the store (D4.slow).
// An entry is keyed by the store, the suite id, version and lock hash; it is
// served while it is younger than the TTL and the store's fingerprint (what
// it holds, for example the number of experiments and sealed runs) is the
// one it was read under. Concurrent misses for the same key share one read.
// A nil cache never caches.
type HistoryCache struct {
	ttl     time.Duration
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]historyEntry
	flight  map[string]*historyFlight
}

type historyEntry struct {
	runs        *SuiteRuns
	fingerprint string
	expires     time.Time
}

type historyFlight struct {
	done        chan struct{}
	fingerprint string
	runs        *SuiteRuns
	err         error
}

// NewHistoryCache is an empty cache whose entries live for ttl.
func NewHistoryCache(ttl time.Duration) *HistoryCache {
	return &HistoryCache{ttl: ttl, now: time.Now, entries: map[string]historyEntry{}, flight: map[string]*historyFlight{}}
}

// Suite is every sealed run of m's suite version in st, from the cache when
// an entry for (storeKey, suite) read under the same fingerprint has not
// expired; hit says so. A blank storeKey or fingerprint reads without the
// cache. Errors are never cached.
func (c *HistoryCache) Suite(ctx context.Context, st Store, storeKey, fingerprint string, m *schemas.Manifest, read leaderboard.RunFileReader) (*SuiteRuns, bool, error) {
	if c == nil || storeKey == "" || fingerprint == "" {
		s, err := readSuite(ctx, st, m.SuiteID, m.SuiteVersion, read, nil)
		return s, false, err
	}
	key := storeKey + "\x00" + m.SuiteID + "@" + m.SuiteVersion + "\x00" + m.SuiteLockHash
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && e.fingerprint == fingerprint && c.now().Before(e.expires) {
		c.mu.Unlock()
		return e.runs, true, nil
	}
	if fl, ok := c.flight[key]; ok && fl.fingerprint == fingerprint {
		c.mu.Unlock()
		select {
		case <-fl.done:
			return fl.runs, fl.err == nil, fl.err
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	}
	fl := &historyFlight{done: make(chan struct{}), fingerprint: fingerprint}
	c.flight[key] = fl
	c.mu.Unlock()

	fl.runs, fl.err = readSuite(ctx, st, m.SuiteID, m.SuiteVersion, read, nil)
	c.mu.Lock()
	if c.flight[key] == fl {
		delete(c.flight, key)
	}
	if fl.err == nil {
		c.putLocked(key, historyEntry{runs: fl.runs, fingerprint: fingerprint, expires: c.now().Add(c.ttl)})
	}
	c.mu.Unlock()
	close(fl.done)
	return fl.runs, false, fl.err
}

func (c *HistoryCache) putLocked(key string, e historyEntry) {
	if len(c.entries) >= historyCacheMax {
		now := c.now()
		for k, old := range c.entries {
			if !now.Before(old.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= historyCacheMax {
			c.entries = map[string]historyEntry{}
		}
	}
	c.entries[key] = e
}
