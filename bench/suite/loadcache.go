package suite

// Process-wide cache of full pack loads (D4.slow). A full load decodes and
// schema-checks every task.yaml and walks every task folder for symlinks and
// the files it names: about 3 s of CPU for a 3,600-task library pack, paid
// by the planner of every estimate, by every analysis (the unreviewed task
// count), the migration report, the compliance pack and the catalog. The
// cache serves those repeats from memory.
//
// Safety:
//   - Key: the absolute pack folder. A load reads only that folder, so an
//     entry answers only for the same folder: two organizations with
//     different project roots never share an entry (they share library
//     packs, which are one folder). The id and version a ref asks for are
//     checked by Load on every answer.
//   - Invalidation: every hit re-stamps the folder (loadStamp): suite.yaml,
//     suite.lock, the quarantine and review sidecars, the tasks folder, every
//     tasks/<id>/ folder and its task.yaml, the probes folder and every probe
//     file, the compose file of every task that has one and, for an
//     organization's own pack, every folder in it. Any change to their size
//     or modification time reloads. A read-only library pack skips the
//     folder walk; loadCacheTTL bounds how long a change deep inside one of
//     its task folders (which a deploy never makes alone) goes unseen.
//   - Concurrency: concurrent misses for one key share a single load, which
//     runs detached from the request that started it (bounded by
//     loadCacheLoadTimeout) so a cancelled request never fails the others.
//     Errors are never cached.
//   - Sharing: every caller gets its own copy of the suite, its slices, its
//     lock (with its own task map) and every task and probe struct, so a
//     caller that changes a field changes only its copy. Deeper maps and
//     slices inside a task are shared and must be treated as read-only, as
//     loaded suites always were (the library cache already shared them).
//   - Memory: at most loadCacheMaxSuites packs and loadCacheMaxTasks tasks
//     are kept, least recently used first out. A 3,600-task pack holds about
//     the decoded task.yaml files (a few KB each), so the bound is in the
//     tens of MB.

import (
	"context"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

const (
	// loadCacheMaxSuites and loadCacheMaxTasks bound the cache. The newest
	// entry is always kept, even when it alone is over the task bound.
	loadCacheMaxSuites = 8
	loadCacheMaxTasks  = 40000
	// loadCacheTTL bounds how long an entry is served without a full load.
	loadCacheTTL = 15 * time.Minute
	// loadCacheLoadTimeout bounds one detached load.
	loadCacheLoadTimeout = 10 * time.Minute
)

type loadCacheKey = string // absolute pack folder

type loadCacheEntry struct {
	stamp  string
	done   chan struct{} // closed when s and err are set
	s      *schemas.Suite
	err    error
	loaded time.Time
	used   uint64 // last use, for least recently used eviction
	tasks  int
}

var loadCache = struct {
	mu    sync.Mutex
	m     map[loadCacheKey]*loadCacheEntry
	tick  uint64
	off   bool
	now   func() time.Time
	loads atomic.Int64 // full loads from disk, for tests
}{m: map[loadCacheKey]*loadCacheEntry{}, now: time.Now}

// FullLoads is the number of full pack loads read from disk by this process
// (cache misses and loads with the cache off). Tests use it to count work.
func FullLoads() int64 { return loadCache.loads.Load() }

// SetLoadCache turns the full load cache on or off (off also empties it);
// it returns the previous setting. Tests compare answers with and without
// it.
func SetLoadCache(on bool) (was bool) {
	loadCache.mu.Lock()
	defer loadCache.mu.Unlock()
	was = !loadCache.off
	loadCache.off = !on
	if !on {
		loadCache.m = map[loadCacheKey]*loadCacheEntry{}
	}
	return was
}

// countedLoad runs load, counted in FullLoads.
func countedLoad(ctx context.Context, load func(context.Context) (*schemas.Suite, error)) (*schemas.Suite, error) {
	loadCache.loads.Add(1)
	return load(ctx)
}

// loadCacheOn reports whether the cache is on (SetLoadCache).
func loadCacheOn() bool {
	loadCache.mu.Lock()
	defer loadCache.mu.Unlock()
	return !loadCache.off
}

// cachedLoad is load (a full read of the pack in root) through the cache.
func cachedLoad(ctx context.Context, root string, deep bool, load func(context.Context) (*schemas.Suite, error)) (*schemas.Suite, error) {
	counted := func(ctx context.Context) (*schemas.Suite, error) { return countedLoad(ctx, load) }
	key := loadCacheKey(root)
	for {
		loadCache.mu.Lock()
		e := loadCache.m[key]
		loadCache.mu.Unlock()
		stamp, inFlight := "", false
		if e != nil {
			select {
			case <-e.done:
				if e.err == nil {
					stamp = loadStamp(root, deep, e.s) // outside the lock: it stats every task folder
				}
			default:
				inFlight = true // a load in flight is shared
			}
		}
		loadCache.mu.Lock()
		if cur := loadCache.m[key]; cur != e { // replaced meanwhile: look again
			loadCache.mu.Unlock()
			continue
		}
		fresh := inFlight || (e != nil && e.err == nil && e.stamp == stamp && loadCache.now().Sub(e.loaded) < loadCacheTTL)
		if !fresh {
			e = &loadCacheEntry{done: make(chan struct{})}
			loadCache.m[key] = e
			go runCachedLoad(key, e, root, deep, counted)
		}
		loadCache.tick++
		e.used = loadCache.tick
		loadCache.mu.Unlock()
		select {
		case <-e.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if e.err != nil {
			return nil, e.err
		}
		return cloneSuite(e.s), nil
	}
}

// runCachedLoad fills e. The stamp is taken before the load, so a change
// during the load is seen by the next lookup.
func runCachedLoad(key loadCacheKey, e *loadCacheEntry, root string, deep bool, load func(context.Context) (*schemas.Suite, error)) {
	stamp := loadStamp(root, deep, nil)
	ctx, cancel := context.WithTimeout(context.Background(), loadCacheLoadTimeout)
	defer cancel()
	s, err := load(ctx)
	if err == nil {
		// The stamp of a loaded pack also covers its tasks' compose files.
		stamp = loadStampWith(stamp, s)
	}
	loadCache.mu.Lock()
	defer loadCache.mu.Unlock()
	e.s, e.err, e.stamp, e.loaded = s, err, stamp, loadCache.now()
	if s != nil {
		e.tasks = len(s.Tasks)
	}
	close(e.done)
	if err != nil {
		if loadCache.m[key] == e {
			delete(loadCache.m, key)
		}
		return
	}
	evictLocked(e)
}

// evictLocked drops least recently used finished entries other than keep
// until the cache is within its bounds.
func evictLocked(keep *loadCacheEntry) {
	for {
		n, tasks := 0, 0
		var oldKey loadCacheKey
		var old *loadCacheEntry
		for k, e := range loadCache.m {
			n++
			tasks += e.tasks
			if e == keep {
				continue
			}
			select {
			case <-e.done:
			default:
				continue // in flight
			}
			if old == nil || e.used < old.used {
				oldKey, old = k, e
			}
		}
		if (n <= loadCacheMaxSuites && tasks <= loadCacheMaxTasks) || old == nil {
			return
		}
		delete(loadCache.m, oldKey)
	}
}

// loadStamp stamps what a full load of the pack in root depends on. Every
// pack: the size and modification time of suite.yaml, suite.lock, the
// sidecars, the tasks folder, every tasks/<id>/ folder and its task.yaml
// (Stamp with perTask), the probes folder and every probe file, and, for a
// loaded suite s, its tasks' compose files. An organization's own pack
// (deep) also stamps every folder in it: a file added, removed, renamed or
// turned into a symlink anywhere changes its folder's time, and plain file
// contents deeper in a task are never read by a load, only checked to
// exist. A library pack is read-only (replaced as a whole on deploy) and
// skips that walk, which costs about 0.1 s per 1,000 tasks.
func loadStamp(root string, deep bool, s *schemas.Suite) string {
	var b strings.Builder
	b.WriteString(Stamp(root, true))
	probes := filepath.Join(root, probesDir)
	stampAdd(&b, probes)
	if entries, err := os.ReadDir(probes); err == nil {
		for _, e := range entries {
			b.WriteString("|" + e.Name())
			stampAdd(&b, filepath.Join(probes, e.Name()))
		}
	}
	if deep {
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				b.WriteString("|!" + path)
				return nil
			}
			if !d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			b.WriteString("|d:" + rel)
			if fi, err := d.Info(); err == nil {
				b.WriteString(":" + strconv.FormatInt(fi.ModTime().UnixNano(), 36))
			}
			return nil
		})
	}
	return loadStampWith(b.String(), s)
}

func loadStampWith(stamp string, s *schemas.Suite) string {
	if s == nil {
		return stamp
	}
	var b strings.Builder
	b.WriteString(stamp)
	for _, t := range s.Tasks {
		if t != nil && t.Compose != "" {
			b.WriteString("|c:" + t.ID)
			stampAdd(&b, filepath.Join(t.Dir, filepath.FromSlash(t.Compose)))
		}
	}
	return b.String()
}

func stampAdd(b *strings.Builder, path string) {
	fi, err := os.Stat(path)
	if err != nil {
		b.WriteString("|-")
		return
	}
	b.WriteString("|")
	b.WriteString(strconv.FormatInt(fi.ModTime().UnixNano(), 36))
	b.WriteString(":")
	b.WriteString(strconv.FormatInt(fi.Size(), 36))
}

// cloneSuite copies s for one caller: the suite, its slices and tier map,
// its lock and the lock's task map, and every task and probe struct.
func cloneSuite(s *schemas.Suite) *schemas.Suite {
	if s == nil {
		return nil
	}
	c := *s
	c.TaskIDs = slices.Clone(s.TaskIDs)
	c.ProbeIDs = slices.Clone(s.ProbeIDs)
	c.Services = slices.Clone(s.Services)
	c.External = slices.Clone(s.External)
	c.Tiers = maps.Clone(s.Tiers)
	if s.Tasks != nil {
		c.Tasks = make([]*schemas.Task, len(s.Tasks))
		tasks := make([]schemas.Task, len(s.Tasks))
		for i, t := range s.Tasks {
			if t != nil {
				tasks[i] = *t
				c.Tasks[i] = &tasks[i]
			}
		}
	}
	if s.Probes != nil {
		c.Probes = make([]*schemas.ProbeSpec, len(s.Probes))
		for i, p := range s.Probes {
			if p != nil {
				cp := *p
				c.Probes[i] = &cp
			}
		}
	}
	if s.Lock != nil {
		l := *s.Lock
		l.Tasks = maps.Clone(s.Lock.Tasks)
		c.Lock = &l
	}
	return &c
}
