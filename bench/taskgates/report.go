package taskgates

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Report is the cheap gates over one suite.
type Report struct {
	Suite         string             `json:"suite"`
	Tasks         int                `json:"tasks"`
	LibraryTasks  int                `json:"library_tasks"` // tasks in the contamination index
	Duplicates    []DuplicatePair    `json:"duplicates"`
	Contamination []ContaminationHit `json:"contamination"`
	// Errors counts what blocks: a held-out task that overlaps the library.
	// Warnings counts duplicates and public tasks that overlap it.
	Errors   int    `json:"errors"`
	Warnings int    `json:"warnings"`
	Summary  string `json:"summary"`
}

// Check runs dedup inside s and contamination against ix (nil skips it).
// threshold <= 0 uses DefaultNearThreshold.
func Check(s *schemas.Suite, ix *Index, threshold float64) *Report {
	items := ItemsOf(s)
	r := &Report{Suite: s.ID, Tasks: len(items), LibraryTasks: ix.Len(), Duplicates: Dedup(items, nil, threshold)}
	if ix != nil {
		split := map[string]string{}
		for _, t := range s.Tasks {
			split[s.ID+"/"+t.ID] = splitOf(t)
		}
		r.Contamination = ix.Check(items, func(it Item) string { return split[it.Key()] })
	}
	r.Warnings = len(r.Duplicates)
	for _, h := range r.Contamination {
		if h.Severity == SeverityError {
			r.Errors++
		} else {
			r.Warnings++
		}
	}
	var parts []string
	if n := len(r.Duplicates); n > 0 {
		parts = append(parts, fmt.Sprintf("%d duplicate pair(s)", n))
	}
	if r.Errors > 0 {
		parts = append(parts, fmt.Sprintf("%d held-out task(s) already public", r.Errors))
	}
	if n := len(r.Contamination) - r.Errors; n > 0 {
		parts = append(parts, fmt.Sprintf("%d task(s) overlapping public benchmarks", n))
	}
	if len(parts) == 0 {
		r.Summary = fmt.Sprintf("%d tasks: no duplicates, nothing found in %d public tasks", r.Tasks, r.LibraryTasks)
	} else {
		r.Summary = strings.Join(parts, "; ")
	}
	return r
}

// LibraryLoader loads one built-in library suite.
type LibraryLoader func(ctx context.Context, id string) (*schemas.Suite, error)

// libraryCache holds the last library index built, keyed by its suite list:
// the library is read-only on a server, so it is built once per process.
var libraryCache struct {
	mu  sync.Mutex
	key string
	ix  *Index
}

// LibraryIndex indexes every task of the library suites ids (a suite that
// fails to load is skipped). The index is cached for the same ids: the
// library is read-only on a server.
func LibraryIndex(ctx context.Context, ids []string, load LibraryLoader) *Index {
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	key := strings.Join(ids, ",")
	libraryCache.mu.Lock()
	defer libraryCache.mu.Unlock()
	if libraryCache.ix != nil && libraryCache.key == key {
		return libraryCache.ix
	}
	ix := NewIndex(nil)
	for _, id := range ids {
		if ctx.Err() != nil {
			return ix // a cancelled build is not cached
		}
		s, err := load(ctx, id)
		if err != nil || s == nil {
			continue
		}
		for _, it := range ItemsOf(s) {
			ix.Add(it)
		}
	}
	libraryCache.key, libraryCache.ix = key, ix
	return ix
}
