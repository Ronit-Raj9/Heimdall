// Package store owns the run folder, the bench's only source of truth
// (DESIGN.md 16.1). Every path is built from validated ids and fixed names, so
// nothing an agent or a client sends can steer a write outside the folder.
// Writes are atomic (temp file, fsync, rename), sealed runs are hashed,
// chained in an append-only seals.log and made read-only, and regrades and
// analyses are separate child artifacts that never touch a sealed run.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// StoreTypeFS is the name of the filesystem store.
const StoreTypeFS = "fs"

// storeVersion is the version of the on-disk layout and seal format.
const storeVersion = "1.0.0"

// Fixed file and folder names of the layout (DESIGN.md 16.1).
const (
	manifestFile    = "manifest.json"
	slotFile        = "slot.json"
	runFile         = "run.json"
	sealFile        = "seal.json"
	transitionsFile = "transitions.jsonl"
	gradeFile       = "grade.json"
	metricsFile     = "metrics.json"
	gatesFile       = "gates.json"
	receiptsFile    = "receipts.json"
	evidenceFile    = "evidence.json"
	leaseFile       = "lease.json"
	leaseLockFile   = "lease.lock"
	sealsLogFile    = "seals.log"
	regradeFile     = "regrade.json"
	verdictFile     = "verdict.json"
	slotsDir        = "slots"
	runsDir         = "runs"
	regradesDir     = "regrades"
	analysisDir     = "analysis"
)

const (
	dirPerm  fs.FileMode = 0o755
	filePerm fs.FileMode = 0o644
	maxIDLen             = 128
)

var idPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// FSStore implements schemas.Store on a local folder laid out as DESIGN.md
// 16.1: <root>/<exp_id>/manifest.json, slots/<slot_id>/slot.json,
// slots/<slot_id>/runs/<run_id>/..., regrades/<regrade_id>/... and
// analysis/<analysis_id>/....
type FSStore struct {
	root     string
	logger   schemas.Logger
	now      func() time.Time
	runLocks keyedMutex // run id -> serializes writes, transitions and seal of one run
	expLocks keyedMutex // exp id -> serializes seals.log
	slotLock keyedMutex // slot id -> serializes lease changes in this process
	slotDirs sync.Map   // slot id -> slot folder, filled on lookup
}

var _ schemas.Store = (*FSStore)(nil)

// NewFSStore opens (and creates when missing) a store rooted at root.
func NewFSStore(root string, logger schemas.Logger) (*FSStore, error) {
	if root == "" {
		return nil, fmt.Errorf("new fs store: empty root")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("new fs store: resolve root %s: %w", root, err)
	}
	if err := os.MkdirAll(abs, dirPerm); err != nil {
		return nil, fmt.Errorf("new fs store: create root %s: %w", abs, err)
	}
	if logger == nil {
		logger = schemas.NoopLogger{}
	}
	return &FSStore{root: abs, logger: logger, now: time.Now}, nil
}

// GetName implements schemas.Part.
func (s *FSStore) GetName() string { return StoreTypeFS }

// GetVersion implements schemas.Part.
func (s *FSStore) GetVersion() string { return storeVersion }

// Root returns the absolute store root.
func (s *FSStore) Root() string { return s.root }

// CreateExperiment writes every slot.json and then manifest.json, so an
// experiment folder without a manifest is an interrupted create and is not
// listed. The experiment folder is created with mkdir, which is what makes a
// second create of the same id fail with schemas.ErrExists.
func (s *FSStore) CreateExperiment(ctx context.Context, m *schemas.Manifest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if m == nil {
		return fmt.Errorf("create experiment: nil manifest")
	}
	if err := validateID("exp_id", m.ExpID); err != nil {
		return fmt.Errorf("create experiment: %w", err)
	}
	seen := make(map[string]bool, len(m.Slots))
	for i := range m.Slots {
		sl := &m.Slots[i]
		if err := validateID("slot_id", sl.SlotID); err != nil {
			return fmt.Errorf("create experiment %s: slot %d: %w", m.ExpID, i, err)
		}
		if sl.ExpID != "" && sl.ExpID != m.ExpID {
			return fmt.Errorf("create experiment %s: slot %s belongs to %s", m.ExpID, sl.SlotID, sl.ExpID)
		}
		if seen[sl.SlotID] {
			return fmt.Errorf("create experiment %s: duplicate slot %s", m.ExpID, sl.SlotID)
		}
		seen[sl.SlotID] = true
	}
	expDir := s.expDir(m.ExpID)
	if err := os.Mkdir(expDir, dirPerm); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create experiment %s: %w", m.ExpID, schemas.ErrExists)
		}
		return fmt.Errorf("create experiment %s: %w", m.ExpID, err)
	}
	for i := range m.Slots {
		sl := m.Slots[i]
		sl.ExpID = m.ExpID
		dir := filepath.Join(expDir, slotsDir, sl.SlotID)
		if err := os.MkdirAll(filepath.Join(dir, runsDir), dirPerm); err != nil {
			return fmt.Errorf("create experiment %s: slot %s: %w", m.ExpID, sl.SlotID, err)
		}
		if err := writeJSONAtomic(filepath.Join(dir, slotFile), sl); err != nil {
			return fmt.Errorf("create experiment %s: slot %s: %w", m.ExpID, sl.SlotID, err)
		}
		s.slotDirs.Store(sl.SlotID, dir)
	}
	if err := writeJSONAtomic(filepath.Join(expDir, manifestFile), m); err != nil {
		return fmt.Errorf("create experiment %s: %w", m.ExpID, err)
	}
	s.logger.Info("experiment created", "exp_id", m.ExpID, "slots", len(m.Slots))
	return nil
}

// LoadManifest reads an experiment's manifest.json.
func (s *FSStore) LoadManifest(ctx context.Context, expID string) (*schemas.Manifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateID("exp_id", expID); err != nil {
		return nil, fmt.Errorf("load manifest: %w", err)
	}
	var m schemas.Manifest
	if err := readJSON(filepath.Join(s.expDir(expID), manifestFile), &m); err != nil {
		return nil, fmt.Errorf("load manifest %s: %w", expID, err)
	}
	return &m, nil
}

// ListExperiments returns the ids of every complete experiment (one with a
// manifest.json), sorted.
func (s *FSStore) ListExperiments(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("list experiments: %w", err)
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || validateID("exp_id", e.Name()) != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(s.root, e.Name(), manifestFile)); err != nil {
			continue
		}
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out, nil
}

// NewRun creates the run folder of a slot and writes the initial run.json, so
// SlotRuns can order attempts even when a run dies before its first stage.
// Blank ExpID, SlotID and PairID on run are taken from slot; set ones must
// match it.
func (s *FSStore) NewRun(ctx context.Context, slot *schemas.Slot, run *schemas.Run) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if slot == nil || run == nil {
		return "", fmt.Errorf("new run: nil slot or run")
	}
	r := *run
	if err := fillRunFromSlot(slot, &r); err != nil {
		return "", fmt.Errorf("new run %s: %w", run.RunID, err)
	}
	if err := validateRunIDs(&r); err != nil {
		return "", fmt.Errorf("new run: %w", err)
	}
	unlock := s.runLocks.lock(r.RunID)
	defer unlock()
	slotPath := filepath.Join(s.expDir(r.ExpID), slotsDir, r.SlotID)
	if _, err := os.Stat(filepath.Join(slotPath, slotFile)); err != nil {
		return "", fmt.Errorf("new run %s: slot %s: %w", r.RunID, r.SlotID, schemas.ErrNotFound)
	}
	if err := os.MkdirAll(filepath.Join(slotPath, runsDir), dirPerm); err != nil {
		return "", fmt.Errorf("new run %s: %w", r.RunID, err)
	}
	dir := s.runDir(&r)
	if err := os.Mkdir(dir, dirPerm); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("new run %s: %w", r.RunID, schemas.ErrExists)
		}
		return "", fmt.Errorf("new run %s: %w", r.RunID, err)
	}
	if err := writeJSONAtomic(filepath.Join(dir, runFile), &r); err != nil {
		return "", fmt.Errorf("new run %s: %w", r.RunID, err)
	}
	return dir, nil
}

// WriteRunFile writes one file into an unsealed run folder atomically. name is
// a relative slash path inside the folder; seal.json and transitions.jsonl are
// reserved for Seal and AppendTransition.
func (s *FSStore) WriteRunFile(ctx context.Context, run *schemas.Run, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if run == nil {
		return fmt.Errorf("write run file: nil run")
	}
	if err := validateRunIDs(run); err != nil {
		return fmt.Errorf("write run file %s: %w", name, err)
	}
	if err := validateName(name); err != nil {
		return fmt.Errorf("write run file for run %s: %w", run.RunID, err)
	}
	if name == sealFile || name == transitionsFile {
		return fmt.Errorf("write run file %s for run %s: reserved name", name, run.RunID)
	}
	unlock := s.runLocks.lock(run.RunID)
	defer unlock()
	dir, err := s.openRunDir(run)
	if err != nil {
		return fmt.Errorf("write run file %s: %w", name, err)
	}
	target := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(target), dirPerm); err != nil {
		return fmt.Errorf("write run file %s for run %s: %w", name, run.RunID, err)
	}
	if err := writeFileAtomic(target, data); err != nil {
		return fmt.Errorf("write run file %s for run %s: %w", name, run.RunID, err)
	}
	return nil
}

// openRunDir returns the folder of an existing, unsealed run.
func (s *FSStore) openRunDir(run *schemas.Run) (string, error) {
	dir := s.runDir(run)
	if _, err := os.Stat(dir); err != nil {
		return "", fmt.Errorf("run %s: %w", run.RunID, schemas.ErrNotFound)
	}
	sealed, err := isSealed(dir)
	if err != nil {
		return "", fmt.Errorf("run %s: %w", run.RunID, err)
	}
	if sealed {
		return "", fmt.Errorf("run %s: %w", run.RunID, schemas.ErrSealed)
	}
	return dir, nil
}

func (s *FSStore) expDir(expID string) string {
	return filepath.Join(s.root, expID)
}

func (s *FSStore) runDir(run *schemas.Run) string {
	return filepath.Join(s.root, run.ExpID, slotsDir, run.SlotID, runsDir, run.RunID)
}

// findRunDir locates a run folder from its experiment and run id alone. Run
// ids are unique, so more than one match means the folder was tampered with.
func (s *FSStore) findRunDir(expID, runID string) (string, error) {
	if err := validateID("exp_id", expID); err != nil {
		return "", err
	}
	if err := validateID("run_id", runID); err != nil {
		return "", err
	}
	matches, err := filepath.Glob(filepath.Join(s.expDir(expID), slotsDir, "*", runsDir, runID))
	if err != nil {
		return "", fmt.Errorf("find run %s: %w", runID, err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("run %s in %s: %w", runID, expID, schemas.ErrNotFound)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("run %s in %s: found in %d slots", runID, expID, len(matches))
	}
}

// slotDir locates a slot folder from its id alone, which is all Lease gets.
// Slot ids are unique across experiments.
func (s *FSStore) slotDir(slotID string) (string, error) {
	if err := validateID("slot_id", slotID); err != nil {
		return "", err
	}
	if v, ok := s.slotDirs.Load(slotID); ok {
		return v.(string), nil
	}
	matches, err := filepath.Glob(filepath.Join(s.root, "*", slotsDir, slotID))
	if err != nil {
		return "", fmt.Errorf("find slot %s: %w", slotID, err)
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("slot %s: %w", slotID, schemas.ErrNotFound)
	case 1:
		s.slotDirs.Store(slotID, matches[0])
		return matches[0], nil
	default:
		return "", fmt.Errorf("slot %s: found in %d experiments", slotID, len(matches))
	}
}

func fillRunFromSlot(slot *schemas.Slot, r *schemas.Run) error {
	fill := func(field string, dst *string, want string) error {
		if *dst == "" {
			*dst = want
			return nil
		}
		if *dst != want {
			return fmt.Errorf("%s %q does not match slot %q", field, *dst, want)
		}
		return nil
	}
	if err := fill("exp_id", &r.ExpID, slot.ExpID); err != nil {
		return err
	}
	if err := fill("slot_id", &r.SlotID, slot.SlotID); err != nil {
		return err
	}
	return fill("pair_id", &r.PairID, slot.PairID)
}

func validateRunIDs(r *schemas.Run) error {
	if err := validateID("exp_id", r.ExpID); err != nil {
		return err
	}
	if err := validateID("slot_id", r.SlotID); err != nil {
		return err
	}
	return validateID("run_id", r.RunID)
}

// validateID keeps every path component built from an id inside the store.
func validateID(kind, id string) error {
	if len(id) == 0 || len(id) > maxIDLen || !idPattern.MatchString(id) {
		return fmt.Errorf("invalid %s %q: want ^[a-z0-9_]+$", kind, id)
	}
	return nil
}

// validateName accepts a clean relative slash path with no "." or ".."
// segment, so a file name can never leave its folder.
func validateName(name string) error {
	if name == "" || strings.ContainsAny(name, "\\\x00") || path.Clean(name) != name || !filepath.IsLocal(name) {
		return fmt.Errorf("invalid file name %q", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("invalid file name %q", name)
		}
	}
	return nil
}

func isSealed(runDir string) (bool, error) {
	_, err := os.Lstat(filepath.Join(runDir, sealFile))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("stat seal: %w", err)
}

// writeJSONAtomic writes v as indented JSON, readable by people and by the
// UI; hashes that matter are computed over canonical JSON or file bytes.
func writeJSONAtomic(target string, v any) error {
	b, err := marshalIndent(v)
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(target), err)
	}
	return writeFileAtomic(target, b)
}

// marshalIndent is the one JSON encoding of files the store writes itself.
func marshalIndent(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// writeFileAtomic writes a temp file in the target's folder, fsyncs it and
// renames it over target, so readers see the old or the new file, never half.
func writeFileAtomic(target string, data []byte) error {
	tmp, err := writeTemp(target, data)
	if err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return errors.Join(fmt.Errorf("rename %s: %w", filepath.Base(target), err), os.Remove(tmp))
	}
	return syncDir(filepath.Dir(target))
}

// writeFileExclusive is writeFileAtomic that refuses to replace an existing
// file: the hard link fails when target exists.
func writeFileExclusive(target string, data []byte) error {
	tmp, err := writeTemp(target, data)
	if err != nil {
		return err
	}
	linkErr := os.Link(tmp, target)
	if rmErr := os.Remove(tmp); rmErr != nil {
		return fmt.Errorf("remove temp for %s: %w", filepath.Base(target), rmErr)
	}
	if linkErr != nil {
		if errors.Is(linkErr, fs.ErrExist) {
			return fmt.Errorf("%s: %w", filepath.Base(target), schemas.ErrExists)
		}
		return fmt.Errorf("link %s: %w", filepath.Base(target), linkErr)
	}
	return syncDir(filepath.Dir(target))
}

func writeTemp(target string, data []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("create temp for %s: %w", filepath.Base(target), err)
	}
	name := f.Name()
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if werr == nil {
		werr = f.Chmod(filePerm)
	}
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		return "", errors.Join(fmt.Errorf("write temp for %s: %w", filepath.Base(target), err), os.Remove(name))
	}
	return name, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", dir, err)
	}
	serr := d.Sync()
	cerr := d.Close()
	if err := errors.Join(serr, cerr); err != nil {
		return fmt.Errorf("sync dir %s: %w", dir, err)
	}
	return nil
}

// readJSON decodes a file, mapping a missing file to schemas.ErrNotFound.
func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%s: %w", filepath.Base(p), schemas.ErrNotFound)
		}
		return fmt.Errorf("read %s: %w", filepath.Base(p), err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode %s: %w", filepath.Base(p), err)
	}
	return nil
}

// readOptionalJSON is readJSON that reports a missing file as found=false.
func readOptionalJSON(p string, v any) (bool, error) {
	err := readJSON(p, v)
	if errors.Is(err, schemas.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// keyedMutex hands out one mutex per key. Entries are never removed; there is
// one per run, slot or experiment touched, which stays small.
type keyedMutex struct {
	m sync.Map
}

func (k *keyedMutex) lock(key string) func() {
	v, _ := k.m.LoadOrStore(key, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}
