package handlers

// Caches for the evaluation wizard's live answers. The wizard asks for an
// estimate on every change and for compatibility on every agent pick; both
// used to load and plan every benchmark per request (about 10 s on a server
// with the built-in library). Forecasts are cached per experiment, agent
// config hash and suite stamp for forecastCacheTTL, so a sealed run shows up
// in the estimate within that window; compatibility answers are cached per
// agent config hash and the stamps of every benchmark (agentcompat.go).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/suite"
)

const (
	// forecastCacheTTL is how long a cached forecast is served. History
	// grows as runs seal; a few minutes of lag is fine for an estimate.
	forecastCacheTTL = 5 * time.Minute
	// forecastCacheMax bounds the cache; past it, expired entries go first,
	// then everything.
	forecastCacheMax = 512
)

// forecastCache holds forecasts by key with an expiry. A nil cache never
// caches. Concurrent asks for the same key share one forecast.
type forecastCache struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]forecastEntry
	flight  map[string]*forecastFlight
}

type forecastEntry struct {
	f       *schemas.Forecast
	expires time.Time
}

type forecastFlight struct {
	done chan struct{}
	f    *schemas.Forecast
	err  error
}

func newForecastCache() *forecastCache {
	return &forecastCache{now: time.Now, entries: map[string]forecastEntry{}, flight: map[string]*forecastFlight{}}
}

// estimate is src.Estimate(exp) through the cache. Errors are never cached:
// a refused plan is asked again next time.
func (c *forecastCache) estimate(ctx context.Context, src ForecastSource, exp *schemas.Experiment, key string) (*schemas.Forecast, error) {
	if c == nil || key == "" {
		return src.Estimate(ctx, exp)
	}
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && c.now().Before(e.expires) {
		c.mu.Unlock()
		return e.f, nil
	}
	if fl, ok := c.flight[key]; ok {
		c.mu.Unlock()
		select {
		case <-fl.done:
			return fl.f, fl.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	fl := &forecastFlight{done: make(chan struct{})}
	c.flight[key] = fl
	c.mu.Unlock()

	fl.f, fl.err = src.Estimate(ctx, exp)
	c.mu.Lock()
	delete(c.flight, key)
	if fl.err == nil && fl.f != nil {
		c.putLocked(key, fl.f)
	}
	c.mu.Unlock()
	close(fl.done)
	return fl.f, fl.err
}

func (c *forecastCache) putLocked(key string, f *schemas.Forecast) {
	now := c.now()
	if len(c.entries) >= forecastCacheMax {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= forecastCacheMax {
			c.entries = map[string]forecastEntry{}
		}
	}
	c.entries[key] = forecastEntry{f: f, expires: now.Add(forecastCacheTTL)}
}

// forecastKey identifies one benchmark's forecast: the org root, the
// experiment the estimate built (arms, models, tasks, conditions, trials,
// budget), the config hash of every agent file it names and the suite's
// stamp and lock. "" when the experiment cannot be encoded (no caching).
func forecastKey(root string, st *schemas.Suite, exp *schemas.Experiment, req *EvalEstimateRequest, agents map[string]*schemas.AgentConfig) string {
	h := sha256.New()
	write := func(v any) bool {
		data, err := json.Marshal(v)
		if err != nil {
			return false
		}
		h.Write(data)
		h.Write([]byte{0})
		return true
	}
	if !write(root) || !write(exp) {
		return ""
	}
	var paths []string
	for _, a := range req.Arms {
		p, _ := normalizeAgent(a.Agent)
		paths = append(paths, p, "agents/"+agentBase(p)+"-task.yaml")
	}
	slices.Sort(paths)
	for _, p := range slices.Compact(paths) {
		if !write(p) || !write(agentConfigHash(agents[p])) {
			return ""
		}
	}
	if st != nil {
		write(st.Root)
		write(st.Version)
		write(st.LockHash)
		write(suiteStampOf(st))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// agentBase is the id of agents/<id>.yaml.
func agentBase(path string) string {
	const pre, suf = "agents/", ".yaml"
	if len(path) > len(pre)+len(suf) && path[:len(pre)] == pre && path[len(path)-len(suf):] == suf {
		return path[len(pre) : len(path)-len(suf)]
	}
	return path
}

// agentConfigHash is the sha256 of an agent config's JSON, "" for none.
func agentConfigHash(cfg *schemas.AgentConfig) string {
	if cfg == nil {
		return ""
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// suiteStampOf is the suite.Stamp-like cache key of a loaded suite's folder,
// "" when it has none (an in-memory suite).
func suiteStampOf(st *schemas.Suite) string {
	if st == nil || st.Root == "" {
		return ""
	}
	return suite.Stamp(st.Root, false) // task edits in place wait out the TTL
}
