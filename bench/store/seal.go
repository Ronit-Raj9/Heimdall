package store

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
	"github.com/maximhq/bifrost/bench/utils"
)

const (
	sealedFilePerm fs.FileMode = 0o444
	sealedDirPerm  fs.FileMode = 0o555
)

// sealEntry is one line of an experiment's seals.log: "<run_id> <seal hash>".
type sealEntry struct {
	RunID string
	Hash  string
}

// Seal hashes every file of the run folder except seal.json into seal.json,
// chains it to the previous seal of the experiment, appends "<run_id> <seal
// hash>" to <exp>/seals.log with fsync, and makes the folder read-only (files
// 0444, folders 0555). The seal hash is the sha256 of the seal.json bytes.
// The engine writes the final run.json (stage, sealed time) before calling
// Seal: nothing in the folder can change after it.
//
// The whole operation holds the experiment's seals.log lock, so the chain is
// linear even when many runs seal at once. If a previous Seal of this run
// crashed after writing seal.json, Seal completes it when its files and its
// prev_seal still match; a fully sealed run returns schemas.ErrSealed.
func (s *FSStore) Seal(ctx context.Context, run *schemas.Run) (*schemas.SealRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if run == nil {
		return nil, fmt.Errorf("seal: nil run")
	}
	if err := validateRunIDs(run); err != nil {
		return nil, fmt.Errorf("seal: %w", err)
	}
	unlockRun := s.runLocks.lock(run.RunID)
	defer unlockRun()
	dir := s.runDir(run)
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, schemas.ErrNotFound)
	}
	log, unlockLog, err := s.openSealsLog(run.ExpID)
	if err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	defer unlockLog()
	entries, err := s.readSealsLogFrom(log, run.ExpID)
	if err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	sealed, err := isSealed(dir)
	if err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	if sealed {
		return s.resumeSeal(dir, run.RunID, log, entries)
	}
	if err := scanRunSecrets(dir); err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	files, err := hashRunFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	for name, h := range files {
		if strings.HasPrefix(h, specialPrefix) {
			return nil, fmt.Errorf("seal run %s: %s is not a regular file", run.RunID, name)
		}
	}
	rec := &schemas.SealRecord{
		RunID:        run.RunID,
		ManifestHash: run.ManifestHash,
		Files:        files,
		PrevSeal:     lastHash(entries),
		SealedAt:     s.now().UTC(),
	}
	b, err := marshalIndent(rec)
	if err != nil {
		return nil, fmt.Errorf("seal run %s: marshal: %w", run.RunID, err)
	}
	if err := writeFileAtomic(filepath.Join(dir, sealFile), b); err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	rec.Hash = utils.SHA256Bytes(b)
	if err := appendLineTo(log, []byte(run.RunID+" "+rec.Hash)); err != nil {
		return nil, fmt.Errorf("seal run %s: append seals.log: %w", run.RunID, err)
	}
	if err := makeReadOnly(dir); err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	s.logger.Info("run sealed", "run_id", run.RunID, "files", len(files), "seal", rec.Hash)
	return rec, nil
}

// scanRunSecrets checks text artifacts before the first seal. Existing sealed
// runs are never rescanned during reads. Binary files are ignored because
// their bytes are evidence, not text that can be safely redacted here.
func scanRunSecrets(dir string) error {
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Base(path) == sealFile {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan artifacts: %w", err)
	}
	sort.Strings(paths)
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("scan %s: %w", filepath.Base(path), err)
		}
		if !utf8.Valid(b) {
			continue
		}
		if utils.HasUnredactedSecrets(string(b)) {
			return fmt.Errorf("unredacted secret in %s: %w", filepath.Base(path), schemas.ErrSealMismatch)
		}
	}
	return nil
}

// Verify recomputes a run's seal: every file hash, no file added or removed,
// and the seal hash present in seals.log. A difference returns
// schemas.ErrSealMismatch wrapped with the first one found, in path order.
func (s *FSStore) Verify(ctx context.Context, expID, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := s.findRunDir(expID, runID)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	rec, err := readSeal(dir)
	if err != nil {
		return fmt.Errorf("verify run %s: %w", runID, err)
	}
	if rec.RunID != runID {
		return fmt.Errorf("verify run %s: seal.json names run %s: %w", runID, rec.RunID, schemas.ErrSealMismatch)
	}
	if err := compareFiles(dir, rec.Files); err != nil {
		return fmt.Errorf("verify run %s: %w", runID, err)
	}
	var run schemas.Run
	if err := readJSON(filepath.Join(dir, runFile), &run); err != nil {
		return fmt.Errorf("verify run %s: read run: %w", runID, err)
	}
	if err := verifyCanonicalFile(dir, &run); err != nil {
		return fmt.Errorf("verify run %s: %w", runID, err)
	}
	entries, err := s.readSealsLog(expID)
	if err != nil {
		return fmt.Errorf("verify run %s: %w", runID, err)
	}
	for _, e := range entries {
		if e.RunID == runID && e.Hash == rec.Hash {
			return nil
		}
	}
	return fmt.Errorf("verify run %s: seal hash %s not in seals.log: %w", runID, rec.Hash, schemas.ErrSealMismatch)
}

// verifyCanonicalFile checks the canonical record when a run was written by
// the current format. Older sealed runs do not have canonical.json or a
// canonical hash and remain readable.
func verifyCanonicalFile(dir string, run *schemas.Run) error {
	if run == nil || run.CanonicalHash == "" {
		return nil
	}
	path := filepath.Join(dir, canonicalFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read canonical.json: %w", err)
	}
	want := "sha256:" + utils.SHA256Bytes(b)
	if want != run.CanonicalHash {
		return fmt.Errorf("canonical hash %s, run.json has %s: %w", want, run.CanonicalHash, schemas.ErrSealMismatch)
	}
	return nil
}

// VerifyChain checks that seals.log is one linear chain: every line is well
// formed, names a run once, matches the sha256 of that run's seal.json, and
// each seal's prev_seal is the hash on the line before. It also checks that
// no sealed run of the experiment is missing from the log.
func (s *FSStore) VerifyChain(expID string) error {
	if err := validateID("exp_id", expID); err != nil {
		return fmt.Errorf("verify chain: %w", err)
	}
	b, err := os.ReadFile(filepath.Join(s.expDir(expID), sealsLogFile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("verify chain %s: %w", expID, err)
	}
	entries, bad := parseSealsLog(b)
	if len(bad) > 0 {
		return fmt.Errorf("verify chain %s: seals.log line %d malformed: %w", expID, bad[0], schemas.ErrSealMismatch)
	}
	prev := ""
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if seen[e.RunID] {
			return fmt.Errorf("verify chain %s: line %d: run %s sealed twice: %w", expID, i+1, e.RunID, schemas.ErrSealMismatch)
		}
		seen[e.RunID] = true
		dir, err := s.findRunDir(expID, e.RunID)
		if err != nil {
			return fmt.Errorf("verify chain %s: line %d: %w: %w", expID, i+1, err, schemas.ErrSealMismatch)
		}
		rec, err := readSeal(dir)
		if err != nil {
			return fmt.Errorf("verify chain %s: line %d: %w", expID, i+1, err)
		}
		if rec.Hash != e.Hash {
			return fmt.Errorf("verify chain %s: line %d: seal.json of run %s hashes to %s, log says %s: %w",
				expID, i+1, e.RunID, rec.Hash, e.Hash, schemas.ErrSealMismatch)
		}
		if rec.PrevSeal != prev {
			return fmt.Errorf("verify chain %s: line %d: run %s prev_seal %q, want %q: %w",
				expID, i+1, e.RunID, rec.PrevSeal, prev, schemas.ErrSealMismatch)
		}
		prev = e.Hash
	}
	sealedDirs, err := filepath.Glob(filepath.Join(s.expDir(expID), slotsDir, "*", runsDir, "*", sealFile))
	if err != nil {
		return fmt.Errorf("verify chain %s: %w", expID, err)
	}
	for _, p := range sealedDirs {
		runID := filepath.Base(filepath.Dir(p))
		if !seen[runID] {
			return fmt.Errorf("verify chain %s: sealed run %s missing from seals.log: %w", expID, runID, schemas.ErrSealMismatch)
		}
	}
	return nil
}

// resumeSeal handles Seal on a folder that already has seal.json.
func (s *FSStore) resumeSeal(dir, runID string, log *os.File, entries []sealEntry) (*schemas.SealRecord, error) {
	rec, err := readSeal(dir)
	if err != nil {
		return nil, fmt.Errorf("seal run %s: %w", runID, err)
	}
	for _, e := range entries {
		if e.RunID == runID && e.Hash == rec.Hash {
			if err := makeReadOnly(dir); err != nil {
				return nil, fmt.Errorf("seal run %s: %w", runID, err)
			}
			return nil, fmt.Errorf("seal run %s: %w", runID, schemas.ErrSealed)
		}
	}
	if rec.RunID != runID || rec.PrevSeal != lastHash(entries) {
		return nil, fmt.Errorf("seal run %s: seal.json is not in seals.log and does not chain to its tail: %w", runID, schemas.ErrSealMismatch)
	}
	if err := compareFiles(dir, rec.Files); err != nil {
		return nil, fmt.Errorf("seal run %s: %w", runID, err)
	}
	if err := appendLineTo(log, []byte(runID+" "+rec.Hash)); err != nil {
		return nil, fmt.Errorf("seal run %s: append seals.log: %w", runID, err)
	}
	if err := makeReadOnly(dir); err != nil {
		return nil, fmt.Errorf("seal run %s: %w", runID, err)
	}
	s.logger.Warn("completed interrupted seal", "run_id", runID, "seal", rec.Hash)
	return rec, nil
}

// openSealsLog opens <exp>/seals.log for read and append under the
// experiment's mutex and flock. The returned function unlocks and closes it.
func (s *FSStore) openSealsLog(expID string) (*os.File, func(), error) {
	unlockMu := s.expLocks.lock(expID)
	f, err := os.OpenFile(filepath.Join(s.expDir(expID), sealsLogFile), os.O_RDWR|os.O_APPEND|os.O_CREATE, filePerm)
	if err != nil {
		unlockMu()
		return nil, nil, fmt.Errorf("open seals.log: %w", err)
	}
	unlockFile, err := lockFile(f)
	if err != nil {
		unlockMu()
		return nil, nil, errors.Join(err, f.Close())
	}
	return f, func() {
		if err := unlockFile(); err != nil {
			s.logger.Warn("seals.log unlock failed", "exp_id", expID, "error", err.Error())
		}
		unlockMu()
	}, nil
}

// readSealsLog reads seals.log without locking: it is append-only, so a
// reader sees a prefix, at worst with a torn last line.
func (s *FSStore) readSealsLog(expID string) ([]sealEntry, error) {
	b, err := os.ReadFile(filepath.Join(s.expDir(expID), sealsLogFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read seals.log: %w", err)
	}
	return s.lenientSealsLog(b, expID), nil
}

func (s *FSStore) readSealsLogFrom(f *os.File, expID string) ([]sealEntry, error) {
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read seals.log: %w", err)
	}
	return s.lenientSealsLog(b, expID), nil
}

// lenientSealsLog parses seals.log for sealing and verifying one run. A
// malformed line is reported, not fatal; VerifyChain is where it fails.
func (s *FSStore) lenientSealsLog(b []byte, expID string) []sealEntry {
	entries, bad := parseSealsLog(b)
	for _, n := range bad {
		s.logger.Warn("malformed seals.log line ignored", "exp_id", expID, "line", n)
	}
	return entries
}

// parseSealsLog returns the well-formed entries and the 1-based numbers of
// malformed lines.
func parseSealsLog(b []byte) ([]sealEntry, []int) {
	var entries []sealEntry
	var bad []int
	for i, line := range bytes.Split(b, []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		fields := strings.Fields(string(line))
		if len(fields) != 2 || validateID("run_id", fields[0]) != nil || !isHexSHA256(fields[1]) {
			bad = append(bad, i+1)
			continue
		}
		entries = append(entries, sealEntry{RunID: fields[0], Hash: fields[1]})
	}
	return entries, bad
}

func lastHash(entries []sealEntry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].Hash
}

// readSeal reads seal.json and fills Hash with the sha256 of its bytes.
func readSeal(dir string) (*schemas.SealRecord, error) {
	b, err := os.ReadFile(filepath.Join(dir, sealFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("not sealed: %w", schemas.ErrNotFound)
		}
		return nil, fmt.Errorf("read seal.json: %w", err)
	}
	var rec schemas.SealRecord
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, fmt.Errorf("decode seal.json: %w: %w", err, schemas.ErrSealMismatch)
	}
	rec.Hash = utils.SHA256Bytes(b)
	return &rec, nil
}

// specialPrefix marks a non-regular entry in a file listing. Seal refuses
// one; Verify reports one as a changed or added file.
const specialPrefix = "special:"

// hashRunFiles returns relative slash path -> sha256 for every file of the
// run folder except seal.json at its top. Symlinks are never followed.
func hashRunFiles(dir string) (map[string]string, error) {
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == sealFile {
			return nil
		}
		if !d.Type().IsRegular() {
			out[rel] = specialPrefix + d.Type().String()
			return nil
		}
		sum, err := utils.SHA256File(p)
		if err != nil {
			return err
		}
		out[rel] = sum
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hash run files: %w", err)
	}
	return out, nil
}

// compareFiles reports the first difference between the folder and want, in
// path order, as schemas.ErrSealMismatch.
func compareFiles(dir string, want map[string]string) error {
	got, err := hashRunFiles(dir)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(want)+len(got))
	for p := range want {
		paths = append(paths, p)
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		w, inWant := want[p]
		g, inGot := got[p]
		switch {
		case !inGot:
			return fmt.Errorf("file %s removed: %w", p, schemas.ErrSealMismatch)
		case !inWant:
			return fmt.Errorf("file %s added: %w", p, schemas.ErrSealMismatch)
		case w != g:
			return fmt.Errorf("file %s changed: %w", p, schemas.ErrSealMismatch)
		}
	}
	return nil
}

// makeReadOnly sets every file to 0444 and every folder, deepest first, to
// 0555.
func makeReadOnly(dir string) error {
	var dirs []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		return os.Chmod(p, sealedFilePerm)
	})
	if err != nil {
		return fmt.Errorf("make read-only: %w", err)
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(dirs[i], sealedDirPerm); err != nil {
			return fmt.Errorf("make read-only: %w", err)
		}
	}
	return nil
}

func isHexSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
