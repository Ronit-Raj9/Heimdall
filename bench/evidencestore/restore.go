package evidencestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/store"
	"github.com/maximhq/bifrost/bench/utils"
)

// RestoreOptions changes where Restore puts an experiment.
type RestoreOptions struct {
	// Target is the store root to restore into. Empty means the
	// organization's own root, where the experiment moves into place. Any
	// other folder gets a verified copy for inspection (an auditor's copy)
	// and leaves the live store alone.
	Target string
}

// RestoreResult says what Restore did.
type RestoreResult struct {
	OrgID         string   `json:"org_id"`
	ExpID         string   `json:"exp_id"`
	Path          string   `json:"path"` // the restored experiment folder
	Files         int      `json:"files"`
	Bytes         int64    `json:"bytes"`
	Seals         int      `json:"seals"`
	LastSeal      string   `json:"last_seal"`
	Generation    int      `json:"generation"`
	ListingSHA256 string   `json:"listing_sha256"`
	ReplacedStub  bool     `json:"replaced_stub"` // an archive stub was replaced by the restored folder
	Warnings      []string `json:"warnings,omitempty"`
}

// Restore downloads an experiment from the mirror into a staging folder,
// checks every file against the listing's sha256, then checks the staged
// copy as the analysis would: store.VerifyChain, store.Verify for every run
// in seals.log, the manifest hash and every seal's manifest hash. Only a copy
// that passes every check is renamed into place; on any mismatch nothing
// moves, the staging folder is removed and the error wraps ErrRefused (or
// ErrChecksum for a changed object).
//
// The target may hold nothing for the experiment or its archive stub; a
// stub's recorded listing hash, seal count and last seal must match the
// mirror, so a mirror rewritten after archiving is refused. A live
// experiment folder is never replaced: compare it with `evidence verify`.
func (m *Mirror) Restore(ctx context.Context, org Org, expID string, opts RestoreOptions) (*RestoreResult, error) {
	unlock, err := m.lockExp(org, expID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := m.recoverExp(org, expID); err != nil {
		return nil, err
	}
	root := org.root()
	if opts.Target != "" {
		if root, err = filepath.Abs(opts.Target); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(root, 0o755); err != nil {
			return nil, err
		}
	}
	dest := filepath.Join(root, expID)
	stub, err := targetState(dest)
	if err != nil {
		return nil, fmt.Errorf("restore %s/%s: %w", org.ID, expID, err)
	}
	// The organization's own stub anchors a copy restored elsewhere too: a
	// mirror rewritten after archiving is refused for an auditor as well.
	anchor := stub
	if anchor == nil && opts.Target != "" {
		if s, err := ReadStub(org.root(), expID); err == nil && !isLive(org.expDir(expID)) {
			anchor = s
		}
	}
	st, err := m.stage(ctx, org, expID, root, anchor)
	if st != nil {
		defer removeTree(st.dir)
	}
	if err != nil {
		return nil, fmt.Errorf("restore %s/%s: %w", org.ID, expID, err)
	}
	res := &RestoreResult{OrgID: org.ID, ExpID: expID, Path: dest, Files: len(st.listing.Files), Bytes: st.bytes, Seals: st.listing.Seals,
		LastSeal: st.listing.LastSeal, Generation: st.listing.Generation, ListingSHA256: st.listingSHA, Warnings: st.warnings}
	if err := sealRunsReadOnly(st.staged()); err != nil {
		return nil, fmt.Errorf("restore %s/%s: %w", org.ID, expID, err)
	}
	if err := moveIntoPlace(root, st.staged(), dest, stub != nil); err != nil {
		return nil, fmt.Errorf("restore %s/%s: %w", org.ID, expID, err)
	}
	res.ReplacedStub = stub != nil
	m.logger().Info("experiment restored", "org_id", org.ID, "exp_id", expID, "files", res.Files, "seals", res.Seals, "path", dest)
	return res, nil
}

// staged is a verified copy of an experiment in a staging folder.
type staged struct {
	dir, expID string
	listing    *Listing
	listingSHA string
	bytes      int64
	warnings   []string
}

func (s *staged) staged() string { return filepath.Join(s.dir, s.expID) }

// stage downloads the listed files into a fresh staging folder under root
// and runs every check of verifyStaged on the copy. A non-nil anchor (an
// archive stub) must match the listing first. The caller removes st.dir,
// which is set whenever st is non-nil, also on error.
func (m *Mirror) stage(ctx context.Context, org Org, expID, root string, anchor *Stub) (*staged, error) {
	listing, listingSHA, err := readListing(ctx, m.Blob, org.ID, expID)
	if err != nil {
		return nil, err
	}
	if anchor != nil {
		if err := anchor.matches(listing, listingSHA); err != nil {
			return nil, fmt.Errorf("%w: %w", err, ErrRefused)
		}
	}
	dir, err := os.MkdirTemp(stateSubdir(root, "staging"), expID+".")
	if err != nil {
		return nil, err
	}
	st := &staged{dir: dir, expID: expID, listing: listing, listingSHA: listingSHA}
	if st.bytes, err = m.fetchAll(ctx, org.ID, expID, listing, st.staged()); err != nil {
		return st, err
	}
	if st.warnings, err = verifyStaged(ctx, dir, expID, listing, m.logger()); err != nil {
		return st, fmt.Errorf("staged copy failed verification, nothing moved: %w: %w", err, ErrRefused)
	}
	return st, nil
}

// isLive reports whether dir holds a live experiment (a manifest.json).
func isLive(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, manifestFile))
	return err == nil
}

// targetState returns the archive stub at dest, nil when dest is absent, and
// an error when dest is a live experiment folder or anything else.
func targetState(dest string) (*Stub, error) {
	entries, err := os.ReadDir(dest)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(entries) == 1 && entries[0].Name() == stubFile && entries[0].Type().IsRegular() {
		return readStubFile(filepath.Join(dest, stubFile))
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%s exists and is empty; remove it first", dest)
	}
	return nil, fmt.Errorf("%s already holds the experiment; compare it with `evidence verify` instead: %w", dest, schemas.ErrExists)
}

// fetchAll downloads every listed file into dir, each through a
// VerifyingReader against the listing, and returns the bytes written.
func (m *Mirror) fetchAll(ctx context.Context, orgID, expID string, l *Listing, dir string) (int64, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan FileEntry)
	var mu sync.Mutex
	var firstErr error
	var total int64
	var wg sync.WaitGroup
	for range min(m.workers(), max(1, len(l.Files))) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for e := range jobs {
				err := m.fetchOne(ctx, expPrefix(orgID, expID)+e.Path, e, filepath.Join(dir, filepath.FromSlash(e.Path)))
				mu.Lock()
				if err != nil && firstErr == nil {
					firstErr = fmt.Errorf("fetch %s: %w", e.Path, err)
					cancel()
				}
				if err == nil {
					total += e.Size
				}
				mu.Unlock()
			}
		}()
	}
	for _, e := range l.Files {
		select {
		case jobs <- e:
		case <-ctx.Done():
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		if errors.Is(firstErr, ErrChecksum) || isNotFound(firstErr) {
			return total, fmt.Errorf("%w: %w", firstErr, ErrRefused)
		}
		return total, firstErr
	}
	return total, ctx.Err()
}

func (m *Mirror) fetchOne(ctx context.Context, key string, e FileEntry, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, _, err := m.Blob.Get(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	var body io.Reader = rc
	if isAppendOnlyLog(e.Path) {
		// A pass that sent a longer log and then failed before its
		// listing leaves an object that only starts with the listed
		// bytes: take exactly those and check them.
		body = io.LimitReader(rc, e.Size)
	}
	_, err = io.Copy(f, VerifyingReader(body, e.Size, e.SHA256))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// verifyStaged runs the store's checks over a staged experiment. It returns
// warnings for things that do not make the copy untrustworthy.
func verifyStaged(ctx context.Context, root, expID string, l *Listing, logger schemas.Logger) ([]string, error) {
	st, err := store.NewFSStore(root, logger)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(root, expID)
	mb, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	manifestHash, err := checkManifest(mb)
	if err != nil {
		return nil, err
	}
	if l.ManifestHash != "" && manifestHash != l.ManifestHash {
		return nil, fmt.Errorf("manifest hash %s, listing says %s", short(manifestHash), short(l.ManifestHash))
	}
	sealsLog, err := os.ReadFile(filepath.Join(dir, sealsLogFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	entries, err := parseSealsLog(sealsLog)
	if err != nil {
		return nil, err
	}
	if len(entries) != l.Seals {
		return nil, fmt.Errorf("seals.log has %d lines, listing says %d", len(entries), l.Seals)
	}
	if len(entries) > 0 && entries[len(entries)-1].Hash != l.LastSeal {
		return nil, fmt.Errorf("seals.log ends at %s, listing says %s", short(entries[len(entries)-1].Hash), short(l.LastSeal))
	}
	if err := st.VerifyChain(expID); err != nil {
		return nil, err
	}
	logged := make(map[string]bool, len(entries))
	for _, e := range entries {
		if err := st.Verify(ctx, expID, e.RunID); err != nil {
			return nil, err
		}
		logged[e.RunID] = true
	}
	runDirs, err := filepath.Glob(filepath.Join(dir, slotsDir, "*", runsDir, "*"))
	if err != nil {
		return nil, err
	}
	for _, d := range runDirs {
		if !logged[filepath.Base(d)] {
			return nil, fmt.Errorf("run folder %s is not in seals.log; the mirror holds sealed runs only", filepath.Base(d))
		}
	}
	if err := checkSealsName(dir, entries, manifestHash); err != nil {
		return nil, err
	}
	var warnings []string
	if set, err := st.Reviews(ctx, expID); err != nil {
		warnings = append(warnings, "reviews not checked: "+err.Error())
	} else {
		for _, p := range set.Problems {
			warnings = append(warnings, "review: "+p)
		}
	}
	return warnings, nil
}

// sealRunsReadOnly gives every restored sealed run folder the modes Seal
// set: files 0444, folders 0555.
func sealRunsReadOnly(expDir string) error {
	runDirs, err := filepath.Glob(filepath.Join(expDir, slotsDir, "*", runsDir, "*"))
	if err != nil {
		return err
	}
	for _, run := range runDirs {
		var dirs []string
		err := filepath.WalkDir(run, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				dirs = append(dirs, p)
				return nil
			}
			return os.Chmod(p, 0o444)
		})
		if err != nil {
			return fmt.Errorf("make %s read-only: %w", filepath.Base(run), err)
		}
		for i := len(dirs) - 1; i >= 0; i-- {
			if err := os.Chmod(dirs[i], 0o555); err != nil {
				return fmt.Errorf("make %s read-only: %w", filepath.Base(run), err)
			}
		}
	}
	return nil
}

// moveIntoPlace renames the staged folder to dest. A stub at dest is first
// moved to the trash folder and put back if the second rename fails.
func moveIntoPlace(root, staged, dest string, hasStub bool) error {
	var trashed string
	if hasStub {
		t, err := trashPath(root, filepath.Base(dest))
		if err != nil {
			return err
		}
		if err := os.Rename(dest, t); err != nil {
			return fmt.Errorf("move stub aside: %w", err)
		}
		trashed = t
	}
	if err := os.Rename(staged, dest); err != nil {
		if trashed != "" {
			if rerr := os.Rename(trashed, dest); rerr != nil {
				return errors.Join(fmt.Errorf("move restored folder into place: %w", err), fmt.Errorf("put stub back from %s: %w", trashed, rerr))
			}
		}
		return fmt.Errorf("move restored folder into place: %w", err)
	}
	if err := syncDir(filepath.Dir(dest)); err != nil {
		return err
	}
	if trashed != "" {
		removeTree(trashed)
	}
	return nil
}

// stateSubdir returns (and creates) a working folder under root/.evidence.
func stateSubdir(root, name string) string {
	p := filepath.Join(root, stateDir, name)
	_ = os.MkdirAll(p, 0o755)
	return p
}

// trashPath returns a fresh path in the trash folder for an experiment's
// folder. The name is "<exp_id>.<random>", so recover can tell which
// experiment a leftover belongs to.
func trashPath(root, expID string) (string, error) {
	dir := stateSubdir(root, "trash")
	p := filepath.Join(dir, expID+"."+strings.TrimPrefix(utils.NewID("t", 8), "t_"))
	if _, err := os.Lstat(p); err == nil {
		return "", fmt.Errorf("trash path %s exists", p)
	}
	return p, nil
}

// recover runs recoverExp for every experiment with leftovers in the trash
// or staging folder, each under its experiment lock.
func (m *Mirror) recover(org Org) error {
	ids := map[string]bool{}
	for _, sub := range []string{"trash", "staging"} {
		entries, err := os.ReadDir(filepath.Join(org.root(), stateDir, sub))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		for _, e := range entries {
			if expID, _, ok := strings.Cut(e.Name(), "."); ok && validID("exp_id", expID) == nil {
				ids[expID] = true
			}
		}
	}
	var errs []error
	for expID := range ids {
		unlock, err := m.lockExp(org, expID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, m.recoverExp(org, expID))
		unlock()
	}
	return errors.Join(errs...)
}

// recoverExp finishes or undoes an archive or restore of one experiment a
// crash interrupted. Both move a folder into <root>/.evidence/trash/ before
// they put its replacement in place, so a trashed folder is decided by what
// stands at <root>/<exp_id>:
//
//   - nothing: the operation did not finish; the trashed folder is moved
//     back (undone);
//   - a valid archive stub: an archive finished (the stub goes in only after
//     the mirror copy verified), or a restore moved a stub aside; the trash
//     is deleted;
//   - a live experiment while the trash holds only a stub: a restore
//     finished; the trash is deleted;
//   - anything else: left alone and reported, never deleted.
//
// The experiment's staging folders (restore copies, stubs being built) are
// deleted. The caller holds the experiment lock.
func (m *Mirror) recoverExp(org Org, expID string) error {
	root := org.root()
	dest := filepath.Join(root, expID)
	var errs []error
	for _, sub := range []string{"staging", "trash"} {
		dir := filepath.Join(root, stateDir, sub)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		for _, e := range entries {
			if id, _, ok := strings.Cut(e.Name(), "."); !ok || id != expID {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if sub == "staging" {
				removeTree(p)
				continue
			}
			switch {
			case !exists(dest):
				if err := os.Rename(p, dest); err != nil {
					errs = append(errs, err)
					continue
				}
				m.logger().Warn("evidence: interrupted operation undone, folder put back", "org_id", org.ID, "exp_id", expID)
			case isStubFolder(dest) || isLive(dest) && isStubFolder(p):
				removeTree(p)
			default:
				m.logger().Error("evidence: interrupted operation left two folders; keeping both for a manual check",
					"org_id", org.ID, "exp_id", expID, "folder", dest, "trashed", p)
			}
		}
	}
	return errors.Join(errs...)
}

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// isStubFolder reports whether dir holds exactly one valid archive stub.
func isStubFolder(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != stubFile || !entries[0].Type().IsRegular() {
		return false
	}
	_, err = readStubFile(filepath.Join(dir, stubFile))
	return err == nil
}

// removeTree deletes a folder that may hold read-only sealed runs.
func removeTree(p string) {
	_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			_ = os.Chmod(q, 0o755)
		}
		return nil
	})
	_ = os.RemoveAll(p)
}
