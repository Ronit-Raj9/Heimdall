package clusters

import (
	"reflect"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Sealed evidence cache. collect reads the ledger, events, tools and stderr
// of every failing run on every request; a sealed run's files never change
// (store.Seal hashes every file of the run folder and makes it read-only, and
// VerifiedRuns has just checked that seal), so the bytes of one (source, run,
// seal hash, file) are kept and served again.
//
// Rules, each tested in TestSealedEvidenceCache:
//   - the key holds the Source itself, so each organization's index (its own
//     Source value) never shares an entry with another; a Source that cannot
//     be a map key is never cached;
//   - a run without a seal hash, or with any evidence problem, is always
//     read fresh;
//   - another seal hash of the same run misses;
//   - a read error is never kept; a missing file is kept as missing;
//   - one file over evidenceCacheMaxFile is never kept, and the cache empties
//     itself past evidenceCacheMaxBytes or evidenceCacheMaxEntries.
//
// Callers get the cached slice; nothing in the package writes into evidence
// bytes (they are parsed and copied into strings).

// Variables so tests can shrink them.
var (
	evidenceCacheMaxEntries       = 20000
	evidenceCacheMaxBytes   int64 = 256 << 20
	evidenceCacheMaxFile    int64 = 16 << 20
)

type evidenceKey struct {
	src  any
	run  string
	seal string
	name string
}

var evidenceCache = struct {
	sync.Mutex
	m     map[evidenceKey][]byte
	bytes int64
	reads int64 // RunEvidence calls made through runEvidence, for tests
}{m: map[evidenceKey][]byte{}}

// cacheable reports whether src can be part of a map key.
func cacheable(src Source) bool {
	v := reflect.ValueOf(src)
	return v.IsValid() && v.Comparable()
}

// runEvidence is readEvidence for a verified run: from the cache when the
// run is sealed and verified, fresh otherwise.
func runEvidence(src Source, r schemas.SealedRun, name string) ([]byte, error) {
	if r.SealHash == "" || len(r.Problems) > 0 || !cacheable(src) {
		return countedRead(src, r.Run.RunID, name)
	}
	k := evidenceKey{src: src, run: r.Run.RunID, seal: r.SealHash, name: name}
	evidenceCache.Lock()
	data, ok := evidenceCache.m[k]
	evidenceCache.Unlock()
	if ok {
		return data, nil
	}
	data, err := countedRead(src, r.Run.RunID, name)
	if err != nil || int64(len(data)) > evidenceCacheMaxFile {
		return data, err
	}
	evidenceCache.Lock()
	defer evidenceCache.Unlock()
	if len(evidenceCache.m) >= evidenceCacheMaxEntries || evidenceCache.bytes+int64(len(data)) > evidenceCacheMaxBytes {
		evidenceCache.m, evidenceCache.bytes = map[evidenceKey][]byte{}, 0
	}
	evidenceCache.m[k] = data
	evidenceCache.bytes += int64(len(data))
	return data, nil
}

func countedRead(src Source, runID, name string) ([]byte, error) {
	evidenceCache.Lock()
	evidenceCache.reads++
	evidenceCache.Unlock()
	return readEvidence(src, runID, name)
}
