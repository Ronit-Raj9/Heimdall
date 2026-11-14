package store

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
)

// fileStamp is what the verification cache compares to decide that a file is
// the one it hashed: size, modification time, change time and mode.
type fileStamp struct {
	size  int64
	mtime int64
	ctime int64
	mode  fs.FileMode
}

// verifyEntry is one cached file comparison of a run folder.
type verifyEntry struct {
	sealHash string
	stamps   map[string]fileStamp
	err      error // nil, or the schemas.ErrSealMismatch the comparison found
}

// verifyCache keeps the result of comparing a run folder's files with its
// seal, per folder. Verifying hashes every file, and one run's gateway log can
// be tens of megabytes, while the API analyses an experiment on every verdict
// request; without the cache each page view would hash the whole experiment
// again. An entry is reused only while the seal hash and the stamp of every
// file are unchanged. Writing a file moves its change time, which user space
// cannot set back, so a reused result describes the same bytes unless the
// system clock was turned back. The seals.log membership check is cheap and
// is never cached.
type verifyCache struct {
	mu      sync.Mutex
	entries map[string]verifyEntry
}

// newVerifyCache returns an empty cache.
func newVerifyCache() *verifyCache {
	return &verifyCache{entries: map[string]verifyEntry{}}
}

// lookup returns the cached comparison of dir when its seal hash and stamps
// still match.
func (c *verifyCache) lookup(dir, sealHash string, stamps map[string]fileStamp) (verifyEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[dir]
	if !ok || e.sealHash != sealHash || !maps.Equal(e.stamps, stamps) {
		return verifyEntry{}, false
	}
	return e, true
}

// put records a comparison.
func (c *verifyCache) put(dir string, e verifyEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[dir] = e
}

// verifyRunFiles compares the files of a sealed run folder with rec, through
// the cache. Only a clean result or a seal mismatch is cached, and only when
// no file changed while it was hashed; an I/O error is always retried.
func (s *FSStore) verifyRunFiles(dir string, rec *schemas.SealRecord) error {
	before, err := stampRunFiles(dir)
	if err != nil {
		return err
	}
	if cached, ok := s.verified.lookup(dir, rec.Hash, before); ok {
		return cached.err
	}
	cmpErr := compareFiles(dir, rec.Files)
	if cmpErr != nil && !errors.Is(cmpErr, schemas.ErrSealMismatch) {
		return cmpErr
	}
	after, err := stampRunFiles(dir)
	if err == nil && maps.Equal(before, after) {
		s.verified.put(dir, verifyEntry{sealHash: rec.Hash, stamps: before, err: cmpErr})
	}
	return cmpErr
}

// verifySealedRun checks one sealed run folder against its seal: seal.json
// names the run, every file matches, and the seal hash is the one seals.log
// recorded for the run (logged maps run id to logged seal hash). The error
// wraps schemas.ErrSealMismatch for any difference.
func (s *FSStore) verifySealedRun(dir, runID string, logged map[string]string) (*schemas.SealRecord, error) {
	rec, err := readSeal(dir)
	if err != nil {
		return nil, err
	}
	if rec.RunID != runID {
		return rec, fmt.Errorf("seal.json names run %s: %w", rec.RunID, schemas.ErrSealMismatch)
	}
	if err := s.verifyRunFiles(dir, rec); err != nil {
		return rec, err
	}
	if logged[runID] != rec.Hash {
		return rec, fmt.Errorf("seal hash %s not in seals.log: %w", rec.Hash, schemas.ErrSealMismatch)
	}
	return rec, nil
}

// loggedSeals returns run id -> seal hash from an experiment's seals.log. A run
// logged twice keeps its last hash; VerifyChain is where a double entry fails.
func (s *FSStore) loggedSeals(expID string) (map[string]string, error) {
	entries, err := s.readSealsLog(expID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(entries))
	for _, e := range entries {
		out[e.RunID] = e.Hash
	}
	return out, nil
}

// stampRunFiles stamps every entry of a run folder that is not a folder,
// symlinks included, by relative slash path.
func stampRunFiles(dir string) (map[string]fileStamp, error) {
	out := map[string]fileStamp{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		out[filepath.ToSlash(rel)] = fileStamp{
			size: info.Size(), mtime: info.ModTime().UnixNano(), ctime: changeTime(info), mode: info.Mode(),
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("stamp run files: %w", err)
	}
	return out, nil
}
