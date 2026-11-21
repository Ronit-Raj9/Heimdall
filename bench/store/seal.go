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
	logInfo, err := log.Stat()
	if err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	logSize := logInfo.Size()
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
	format, err := requireArtifacts(run, files)
	if err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	rec := &schemas.SealRecord{
		RunID:        run.RunID,
		ManifestHash: run.ManifestHash,
		Files:        files,
		PrevSeal:     lastHash(entries),
		SealedAt:     s.now().UTC(),
		Format:       format,
	}
	b, err := marshalIndent(rec)
	if err != nil {
		return nil, fmt.Errorf("seal run %s: marshal: %w", run.RunID, err)
	}
	if err := writeFileAtomic(filepath.Join(dir, sealFile), b); err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	rec.Hash = utils.SHA256Bytes(b)
	if err := appendSealLine(log, []byte(run.RunID+" "+rec.Hash)); err != nil {
		// QC2-06-04: roll the half-made seal back while the log lock is still
		// held. A seal.json left without its log line would let a successor
		// seal chain past it, and the stranded run could then never be
		// finished; without seal.json the run stays an ordinary unsealed run.
		return nil, errors.Join(fmt.Errorf("seal run %s: append seals.log: %w", run.RunID, err), s.rollbackSeal(dir, log, logSize))
	}
	if err := makeReadOnly(dir); err != nil {
		return nil, fmt.Errorf("seal run %s: %w", run.RunID, err)
	}
	s.logger.Info("run sealed", "run_id", run.RunID, "files", len(files), "seal", rec.Hash)
	return rec, nil
}

// appendSealLine appends a new seal's seals.log line; a variable so a test
// can make the append fail.
var appendSealLine = appendLineTo

// rollbackSeal undoes a Seal whose seals.log append failed: the log is cut
// back to its size before the append (dropping a torn line) and seal.json is
// removed, so no successor can chain past a seal the log never recorded.
func (s *FSStore) rollbackSeal(dir string, log *os.File, size int64) error {
	var errs []error
	if err := log.Truncate(size); err != nil {
		errs = append(errs, fmt.Errorf("roll back seals.log: %w", err))
	}
	if err := os.Remove(filepath.Join(dir, sealFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("roll back seal.json: %w", err))
	}
	return errors.Join(errs...)
}

// SealFormatV1 is the seal format of a current-format run: one with a
// canonical record, sealed only once every file of mandatoryV1 is present.
const SealFormatV1 = "run/1"

// mandatoryV1 is the artifact set the engine writes for every terminal
// outcome, aborted_infra included, once the run has a canonical record
// (engine Pipeline.writeArtifacts). Missing one means the run directory is
// incomplete evidence, not a run that merely produced little.
var mandatoryV1 = []string{runFile, canonicalFile, evidenceFile, gatesFile, gradeFile, "plan.json"}

// requireArtifacts returns the seal format for run and refuses a
// current-format run whose mandatory artifacts are missing. A run without a
// canonical hash predates the canonical record and seals as legacy (empty
// format), as before.
func requireArtifacts(run *schemas.Run, files map[string]string) (string, error) {
	if run.CanonicalHash == "" {
		return "", nil
	}
	if missing := missingArtifacts(SealFormatV1, files); len(missing) > 0 {
		return "", fmt.Errorf("mandatory artifacts missing for %s: %s: %w", SealFormatV1, strings.Join(missing, ", "), schemas.ErrSealMismatch)
	}
	return SealFormatV1, nil
}

// missingArtifacts lists the mandatory files of format absent from files.
// An unknown non-empty format is reported as such so a newer seal is never
// silently accepted by an older reader.
func missingArtifacts(format string, files map[string]string) []string {
	switch format {
	case "":
		return nil
	case SealFormatV1:
	default:
		return []string{"(unknown seal format " + format + ")"}
	}
	var out []string
	for _, name := range mandatoryV1 {
		if _, ok := files[name]; !ok {
			out = append(out, name)
		}
	}
	return out
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
		// Classify before any open: a symlink could read outside the run
		// folder and a pipe could block while the chain lock is held.
		if !d.Type().IsRegular() {
			rel, _ := filepath.Rel(dir, path)
			return fmt.Errorf("%s is not a regular file (%s)", filepath.ToSlash(rel), d.Type())
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
		if agentOutput(dir, path) {
			// The agent's own work is sealed byte for byte so it can be
			// regraded; code like `password = "x"` is not a leak, so only
			// real credential formats stop the seal here.
			if utils.HasLiveCredentials(string(b)) {
				return fmt.Errorf("credential in agent output %s: %w", filepath.Base(path), schemas.ErrSealMismatch)
			}
			continue
		}
		if utils.HasUnredactedSecrets(string(b)) {
			return fmt.Errorf("unredacted secret in %s: %w", filepath.Base(path), schemas.ErrSealMismatch)
		}
	}
	return nil
}

// agentOutput reports whether path holds the agent's own output in run
// folder dir: the workspace diff, exported artifacts and exported state.
func agentOutput(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "workspace.diff" || strings.HasPrefix(rel, "artifacts/") || strings.HasPrefix(rel, "state/")
}

// Verify recomputes a run's seal: every file hash, no file added or removed,
// and the seal hash present in seals.log. A difference returns
// schemas.ErrSealMismatch wrapped with the first one found, in path order.
// The file comparison goes through the store's verification cache
// (verifycache.go), so verifying an unchanged run again costs one stat per
// file.
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
	if err := s.verifyRunFiles(dir, rec); err != nil {
		return fmt.Errorf("verify run %s: %w", runID, err)
	}
	if missing := missingArtifacts(rec.Format, rec.Files); len(missing) > 0 {
		return fmt.Errorf("verify run %s: seal format %s lacks %s: %w", runID, rec.Format, strings.Join(missing, ", "), schemas.ErrSealMismatch)
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
	dirs, err := s.runDirsOf(expID)
	if err != nil {
		return fmt.Errorf("verify chain %s: %w", expID, err)
	}
	prev := ""
	seen := make(map[string]bool, len(entries))
	for i, e := range entries {
		if seen[e.RunID] {
			return fmt.Errorf("verify chain %s: line %d: run %s sealed twice: %w", expID, i+1, e.RunID, schemas.ErrSealMismatch)
		}
		seen[e.RunID] = true
		var dir string
		switch found := dirs[e.RunID]; len(found) {
		case 0:
			return fmt.Errorf("verify chain %s: line %d: run %s has no run folder: %w", expID, i+1, e.RunID, schemas.ErrSealMismatch)
		case 1:
			dir = found[0]
		default:
			return fmt.Errorf("verify chain %s: line %d: run %s found in %d slots: %w", expID, i+1, e.RunID, len(found), schemas.ErrSealMismatch)
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
	for _, runID := range sortedRunIDs(dirs) {
		if seen[runID] {
			continue
		}
		for _, dir := range dirs[runID] {
			sealed, err := isSealed(dir)
			if err != nil {
				return fmt.Errorf("verify chain %s: %w", expID, err)
			}
			if sealed {
				return fmt.Errorf("verify chain %s: sealed run %s missing from seals.log: %w", expID, runID, schemas.ErrSealMismatch)
			}
		}
	}
	return nil
}

// runDirsOf lists every run folder of an experiment in one pass, run id ->
// folders (more than one only when the same id sits in two slots), so
// VerifyChain looks each logged run up instead of globbing the slots once
// per line.
func (s *FSStore) runDirsOf(expID string) (map[string][]string, error) {
	matches, err := filepath.Glob(filepath.Join(s.expDir(expID), slotsDir, "*", runsDir, "*"))
	if err != nil {
		return nil, fmt.Errorf("list run folders: %w", err)
	}
	out := make(map[string][]string, len(matches))
	for _, p := range matches {
		info, err := os.Lstat(p)
		if err != nil || !info.IsDir() {
			continue
		}
		id := filepath.Base(p)
		out[id] = append(out[id], p)
	}
	return out, nil
}

// sortedRunIDs returns the keys of dirs in order, so the first problem
// reported is always the same one.
func sortedRunIDs(dirs map[string][]string) []string {
	ids := make([]string, 0, len(dirs))
	for id := range dirs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
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
