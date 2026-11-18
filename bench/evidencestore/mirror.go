package evidencestore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

// Mirror copies sealed experiments to a Blob and back.
type Mirror struct {
	Blob        Blob
	Logger      schemas.Logger
	Concurrency int              // parallel transfers; 4 when zero
	Now         func() time.Time // time.Now when nil

	locks keyedMutex
}

// NewMirror returns a Mirror over b.
func NewMirror(b Blob, logger schemas.Logger) *Mirror {
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	return &Mirror{Blob: b, Logger: logger}
}

func (m *Mirror) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Mirror) logger() schemas.Logger {
	if m.Logger == nil {
		return schemas.NoopLogger{}
	}
	return m.Logger
}

func (m *Mirror) workers() int {
	if m.Concurrency > 0 {
		return m.Concurrency
	}
	return 4
}

// MirrorResult says what one mirror pass did.
type MirrorResult struct {
	OrgID         string   `json:"org_id"`
	ExpID         string   `json:"exp_id"`
	Files         int      `json:"files"`          // files in the listing after the pass
	Uploaded      int      `json:"uploaded"`       // files sent in this pass
	Resumed       int      `json:"resumed"`        // files already in the mirror from an interrupted pass
	Bytes         int64    `json:"bytes"`          // bytes sent in this pass
	Seals         int      `json:"seals"`          // sealed runs mirrored
	LastSeal      string   `json:"last_seal"`      // the mirrored seals.log's last hash
	Pending       []string `json:"pending"`        // run folders not sealed yet, left for a later pass
	Skipped       []string `json:"skipped"`        // entries never mirrored (dot names, non-regular files, a stray stub)
	Repaired      int      `json:"repaired"`       // listed files sent again because their object was gone or differed
	Generation    int      `json:"generation"`     // listing generation after the pass
	ListingSHA256 string   `json:"listing_sha256"` // sha256 of the listing bytes after the pass
	Unchanged     bool     `json:"unchanged"`      // nothing new: the listing was not rewritten
}

// Experiment mirrors one experiment: it verifies every seal and the chain
// (takeSnapshot), checks that nothing already mirrored changed and that
// seals.log and reviews.log only grew, sends the files the mirror does not
// have yet, and writes the listing last. It is idempotent (a second pass
// with nothing new sends nothing and leaves the listing alone) and
// resumable (a file an interrupted pass already sent is found with Head and
// not sent again). An unsealed run is left for a later pass; a tampered run,
// a broken chain or a rewritten log refuses the whole experiment with
// ErrRefused, and the mirror keeps its last good copy.
func (m *Mirror) Experiment(ctx context.Context, org Org, expID string) (*MirrorResult, error) {
	return m.ExperimentWith(ctx, org, expID, MirrorOptions{})
}

// MirrorOptions changes a mirror pass.
type MirrorOptions struct {
	// Repair also checks, with Head, every file the listing already names
	// and sends again any whose object is gone or differs (a bucket that
	// lost an object). A normal pass trusts the listing for those files.
	Repair bool
}

// ExperimentWith is Experiment with options.
func (m *Mirror) ExperimentWith(ctx context.Context, org Org, expID string, opts MirrorOptions) (*MirrorResult, error) {
	unlock, err := m.lockExp(org, expID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	return m.experimentLocked(ctx, org, expID, opts)
}

func (m *Mirror) experimentLocked(ctx context.Context, org Org, expID string, opts MirrorOptions) (*MirrorResult, error) {
	snap, err := takeSnapshot(ctx, org, expID)
	if err != nil {
		return nil, err
	}
	prev, prevSHA, err := readListing(ctx, m.Blob, org.ID, expID)
	if err != nil && !isNotFound(err) {
		return nil, err
	}
	if err := checkAgainstListing(snap, prev); err != nil {
		return nil, fmt.Errorf("experiment %s/%s: %w: %w", org.ID, expID, err, ErrRefused)
	}
	res := &MirrorResult{OrgID: org.ID, ExpID: expID, Seals: snap.Seals, LastSeal: snap.LastSeal, Pending: snap.Pending, Skipped: snap.Skipped}
	var todo, logs []localFile
	for _, f := range snap.Files {
		if prev != nil {
			if e, ok := prev.file(f.Path); ok && e.SHA256 == f.SHA256 && e.Size == f.Size {
				if !opts.Repair {
					continue
				}
				ok, err := m.objectMatches(ctx, expPrefix(org.ID, expID)+f.Path, f)
				if err != nil {
					return nil, fmt.Errorf("experiment %s/%s: repair: %w", org.ID, expID, err)
				}
				if ok {
					continue
				}
				res.Repaired++
			}
		}
		if isAppendOnlyLog(f.Path) {
			logs = append(logs, f)
			continue
		}
		todo = append(todo, f)
	}
	// Data first, then the logs that name it, then the listing: a pass cut
	// short anywhere leaves the previous listing describing a complete copy.
	if err := m.sendAll(ctx, org.ID, expID, todo, res); err != nil {
		return nil, err
	}
	if err := m.sendAll(ctx, org.ID, expID, logs, res); err != nil {
		return nil, err
	}
	next := mergeListing(prev, snap)
	if prev != nil && sameListing(prev, next) {
		res.Files, res.Generation, res.ListingSHA256, res.Unchanged = len(prev.Files), prev.Generation, prevSHA, true
		return res, nil
	}
	next.UpdatedAt = m.now().UTC()
	b, err := encodeListing(next)
	if err != nil {
		return nil, err
	}
	sum := utils.SHA256Bytes(b)
	if err := m.Blob.Put(ctx, listingKey(org.ID, expID), PutInput{Body: bytes.NewReader(b), Size: int64(len(b)), SHA256: sum, ContentType: "application/json"}); err != nil {
		return nil, fmt.Errorf("experiment %s/%s: write listing: %w", org.ID, expID, err)
	}
	res.Files, res.Generation, res.ListingSHA256 = len(next.Files), next.Generation, sum
	m.logger().Info("experiment mirrored", "org_id", org.ID, "exp_id", expID, "files", res.Files, "uploaded", res.Uploaded,
		"resumed", res.Resumed, "seals", res.Seals, "generation", res.Generation, "mirror", m.Blob.String())
	return res, nil
}

// checkAgainstListing refuses a snapshot that contradicts what is already
// mirrored: a mirrored file (other than the append-only logs) whose bytes changed, a
// manifest that changed, or a log that is not the mirrored log plus new
// lines. A mirrored file missing locally is not an error here: the mirror
// keeps it, and `evidence verify` reports it.
func checkAgainstListing(snap *snapshot, prev *Listing) error {
	if prev == nil {
		return nil
	}
	if prev.ManifestHash != "" && prev.ManifestHash != snap.ManifestHash {
		return fmt.Errorf("manifest hash %s differs from the mirrored %s", short(snap.ManifestHash), short(prev.ManifestHash))
	}
	for _, e := range prev.Files {
		f, ok := snap.file(e.Path)
		switch {
		case isAppendOnlyLog(e.Path):
			if !ok {
				return fmt.Errorf("%s is mirrored (%d bytes) but missing locally", e.Path, e.Size)
			}
			if f.Size < e.Size || utils.SHA256Bytes(f.data[:e.Size]) != e.SHA256 {
				return fmt.Errorf("local %s does not extend the mirrored one: lines were changed or removed", e.Path)
			}
		case ok && (f.SHA256 != e.SHA256 || f.Size != e.Size):
			return fmt.Errorf("file %s changed since it was mirrored (sha256 %s, mirrored %s)", e.Path, short(f.SHA256), short(e.SHA256))
		}
	}
	return nil
}

// mergeListing is the previous listing with the snapshot's files added or
// replaced. Files only the mirror has stay: the mirror never forgets
// evidence.
func mergeListing(prev *Listing, snap *snapshot) *Listing {
	byPath := map[string]FileEntry{}
	gen := 1
	if prev != nil {
		for _, e := range prev.Files {
			byPath[e.Path] = e
		}
		gen = prev.Generation + 1
	}
	for _, f := range snap.Files {
		byPath[f.Path] = FileEntry{Path: f.Path, Size: f.Size, SHA256: f.SHA256}
	}
	files := make([]FileEntry, 0, len(byPath))
	for _, e := range byPath {
		files = append(files, e)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return &Listing{
		Format: ListingFormat, OrgID: snap.OrgID, ExpID: snap.ExpID, ManifestHash: snap.ManifestHash,
		Seals: snap.Seals, LastSeal: snap.LastSeal, Generation: gen, Files: files,
	}
}

func sameListing(a, b *Listing) bool {
	if a.ManifestHash != b.ManifestHash || a.Seals != b.Seals || a.LastSeal != b.LastSeal || len(a.Files) != len(b.Files) {
		return false
	}
	for i := range a.Files {
		if a.Files[i] != b.Files[i] {
			return false
		}
	}
	return true
}

// sendAll sends files with a few workers. A file the mirror already holds
// with the same size and sha256 (left by an interrupted pass) is not sent
// again. The first error stops the rest.
func (m *Mirror) sendAll(ctx context.Context, orgID, expID string, files []localFile, res *MirrorResult) error {
	if len(files) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan localFile)
	var mu sync.Mutex
	var firstErr error
	var wg sync.WaitGroup
	for range min(m.workers(), len(files)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range jobs {
				sent, err := m.sendOne(ctx, expPrefix(orgID, expID)+f.Path, f)
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("experiment %s/%s: send %s: %w", orgID, expID, f.Path, err)
					cancel()
				}
				if err == nil && sent {
					res.Uploaded++
					res.Bytes += f.Size
				} else if err == nil {
					res.Resumed++
				}
				mu.Unlock()
			}
		}()
	}
	for _, f := range files {
		select {
		case jobs <- f:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		if errors.Is(firstErr, ErrChecksum) {
			return fmt.Errorf("%w: a file changed while it was being mirrored: %w", firstErr, ErrRefused)
		}
		return firstErr
	}
	return ctx.Err()
}

// objectMatches reports whether the object holds the file: same size and,
// when the backend records one, the same sha256.
func (m *Mirror) objectMatches(ctx context.Context, key string, f localFile) (bool, error) {
	info, err := m.Blob.Head(ctx, key)
	if isNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return info.Size == f.Size && (info.SHA256 == "" || info.SHA256 == f.SHA256), nil
}

// sendOne puts one file unless the mirror already has it. It reports
// whether bytes were sent.
func (m *Mirror) sendOne(ctx context.Context, key string, f localFile) (bool, error) {
	if info, err := m.Blob.Head(ctx, key); err == nil && info.Size == f.Size && info.SHA256 == f.SHA256 {
		return false, nil
	} else if err != nil && !isNotFound(err) {
		return false, err
	}
	in := PutInput{Size: f.Size, SHA256: f.SHA256, ContentType: contentType(f.Path)}
	if f.data != nil || f.abs == "" {
		in.Body = bytes.NewReader(f.data)
		return true, m.Blob.Put(ctx, key, in)
	}
	fh, err := os.Open(f.abs)
	if err != nil {
		return false, err
	}
	defer fh.Close()
	in.Body = fh
	return true, m.Blob.Put(ctx, key, in)
}

func contentType(p string) string {
	switch filepath.Ext(p) {
	case ".json":
		return "application/json"
	case ".jsonl", ".log", ".txt", ".md", ".diff":
		return "text/plain; charset=utf-8"
	}
	return "application/octet-stream"
}

// SweepItem is the outcome of one experiment in a sweep.
type SweepItem struct {
	Result *MirrorResult `json:"result,omitempty"`
	OrgID  string        `json:"org_id"`
	ExpID  string        `json:"exp_id"`
	Error  string        `json:"error,omitempty"`
}

// Sweep mirrors every experiment of every organization given, so a seal the
// hook missed (a crash, a server that was down, a CLI run) still reaches the
// mirror. Archived experiments are skipped. One experiment's or one
// organization's failure does not stop the others: it becomes a failed
// item, and the returned error says how many failed.
func (m *Mirror) Sweep(ctx context.Context, orgs []Org) ([]SweepItem, error) {
	return m.SweepWith(ctx, orgs, MirrorOptions{})
}

// SweepWith is Sweep with options.
func (m *Mirror) SweepWith(ctx context.Context, orgs []Org, opts MirrorOptions) ([]SweepItem, error) {
	var out []SweepItem
	failed := 0
	for _, org := range orgs {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if err := m.recover(org); err != nil {
			m.logger().Warn("evidence: interrupted archive or restore not recovered", "org_id", org.ID, "error", err.Error())
		}
		ids, err := org.Store.ListExperiments(ctx)
		if err != nil {
			failed++
			out = append(out, SweepItem{OrgID: org.ID, Error: "list experiments: " + err.Error()})
			m.logger().Error("organization not swept", "org_id", org.ID, "error", err.Error())
			continue
		}
		for _, id := range ids {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			res, err := m.ExperimentWith(ctx, org, id, opts)
			item := SweepItem{OrgID: org.ID, ExpID: id, Result: res}
			if err != nil {
				failed++
				item.Error = err.Error()
				m.logger().Error("experiment not mirrored", "org_id", org.ID, "exp_id", id, "error", err.Error())
			}
			out = append(out, item)
		}
	}
	if failed > 0 {
		return out, fmt.Errorf("sweep: %d of %d items not mirrored", failed, len(out))
	}
	return out, nil
}

// keyedMutex serializes passes over one experiment inside this process; the
// lock file serializes them across processes (a server and a cron job).
type keyedMutex struct {
	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

func (k *keyedMutex) lock(key string) func() {
	k.mu.Lock()
	if k.locks == nil {
		k.locks = map[string]*sync.Mutex{}
	}
	l, ok := k.locks[key]
	if !ok {
		l = &sync.Mutex{}
		k.locks[key] = l
	}
	k.mu.Unlock()
	l.Lock()
	return l.Unlock
}

func (m *Mirror) lockExp(org Org, expID string) (func(), error) {
	if err := validID("org_id", org.ID); err != nil {
		return nil, err
	}
	if err := validID("exp_id", expID); err != nil {
		return nil, err
	}
	unlockMu := m.locks.lock(org.root() + "\x00" + expID)
	dir := filepath.Join(org.root(), stateDir, "locks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		unlockMu()
		return nil, fmt.Errorf("lock %s: %w", expID, err)
	}
	f, err := os.OpenFile(filepath.Join(dir, expID+".lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		unlockMu()
		return nil, fmt.Errorf("lock %s: %w", expID, err)
	}
	if err := flock(f); err != nil {
		_ = f.Close()
		unlockMu()
		return nil, fmt.Errorf("lock %s: %w", expID, err)
	}
	return func() {
		_ = funlock(f)
		_ = f.Close()
		unlockMu()
	}, nil
}
