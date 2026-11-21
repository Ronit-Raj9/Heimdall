// Package runctl holds the live run controls of one experiment and their
// audit log (run control, audit/RESEARCH_R1.md items 1, 2, 7 and 8).
//
// The controls live next to the experiment's manifest, in the store folder:
//
//   - .controls.json is the current state: paused or running, a live
//     concurrency, a live spend cap, paused models, runs an operator
//     cancelled and runs an operator superseded by a requeue.
//   - controls.jsonl is the append-only, hash-chained log of every change:
//     who, why, when, and the settings before and after. Verify recomputes
//     the chain and checks that its last entry matches controls.json, so an
//     edited or truncated log is detected the same way seals.log is. The
//     log is mirrored with the experiment's other append-only logs; the
//     state file is a dot file (never mirrored) and is rebuilt from the
//     log's last entry when it is missing, for example after a restore.
//
// Every writer (the API, `heimdall ctl`, the scheduler applying the error
// policy) goes through Apply, which takes a process lock and an flock on
// .controls.lock, so two processes on one store never interleave. The
// scheduler only reads: it polls Read between leases, so a change made by
// any process reaches a running experiment within one poll.
package runctl

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Files in the experiment's store folder.
const (
	StateFile = ".controls.json"
	LogFile   = "controls.jsonl"
	lockName  = ".controls.lock"
)

// Modes of an experiment's controls.
const (
	ModeRunning = "running"
	ModePaused  = "paused" // drain: in-flight runs finish, nothing new starts
)

// Actions recorded in the log.
const (
	ActionPause     = "pause"
	ActionResume    = "resume"
	ActionSet       = "set"        // live concurrency, spend cap, paused models
	ActionCancelRun = "cancel_run" // stop one run; its slot waits for a requeue
	ActionRequeue   = "requeue_run"
	ActionSupersede = "supersede" // the error policy retried an infra_error run
)

// Limits of the live settings.
const (
	MaxConcurrency = 64
	MaxReasonLen   = 500
	maxModels      = 32
)

// ErrInvalid marks a control request that breaks a rule; the API answers 400.
var ErrInvalid = errors.New("invalid run control")

// ErrConflict marks a request the current state refuses (resume of a running
// experiment, requeue of a counted pass); the API answers 409.
var ErrConflict = errors.New("run control conflict")

// ErrBrokenLog marks a controls.jsonl whose chain does not verify.
var ErrBrokenLog = errors.New("controls log does not verify")

// SlotHold is a run an operator cancelled: the slot waits until requeued.
type SlotHold struct {
	RunID  string    `json:"run_id,omitempty"`
	By     string    `json:"by"`
	Reason string    `json:"reason,omitempty"`
	At     time.Time `json:"at"`
}

// Supersede is a sealed run replaced by a requeue: kept on disk, never
// counted, and it uses no attempt.
type Supersede struct {
	SlotID  string    `json:"slot_id"`
	Outcome string    `json:"outcome"`
	By      string    `json:"by"`
	Reason  string    `json:"reason,omitempty"`
	At      time.Time `json:"at"`
}

// Settings are the operator-set fields of the state: what each log entry
// records before and after.
type Settings struct {
	Mode        string     `json:"mode"`
	PausedBy    string     `json:"paused_by,omitempty"`
	PauseReason string     `json:"pause_reason,omitempty"`
	PausedAt    *time.Time `json:"paused_at,omitempty"`
	// Concurrency, when > 0, replaces the experiment's concurrency for local
	// workers from the next lease on.
	Concurrency int `json:"concurrency,omitempty"`
	// TotalUSD, when > 0, replaces the frozen spend cap. A run that started
	// with no cap takes it from its next start (resume).
	TotalUSD float64 `json:"total_usd,omitempty"`
	// PausedModels holds the slots whose arm runs one of these models.
	PausedModels   []string             `json:"paused_models,omitempty"`
	CancelledSlots map[string]SlotHold  `json:"cancelled_slots,omitempty"`
	Superseded     map[string]Supersede `json:"superseded,omitempty"`
}

// State is controls.json.
type State struct {
	Settings
	Seq       int       `json:"seq"`
	Head      string    `json:"head,omitempty"` // hash of the last log entry
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Paused reports a drained experiment.
func (s *State) Paused() bool { return s != nil && s.Mode == ModePaused }

// ModelPaused reports whether model is paused.
func (s *State) ModelPaused(model string) bool {
	return s != nil && model != "" && slices.Contains(s.PausedModels, model)
}

// SlotCancelled reports a slot an operator cancelled and did not requeue.
func (s *State) SlotCancelled(slotID string) bool {
	if s == nil {
		return false
	}
	_, ok := s.CancelledSlots[slotID]
	return ok
}

// IsSuperseded reports a run replaced by a requeue.
func (s *State) IsSuperseded(runID string) bool {
	if s == nil {
		return false
	}
	_, ok := s.Superseded[runID]
	return ok
}

// Entry is one line of controls.jsonl.
type Entry struct {
	Seq    int             `json:"seq"`
	At     time.Time       `json:"at"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	Target string          `json:"target,omitempty"` // slot or run id
	Reason string          `json:"reason,omitempty"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
	Prev   string          `json:"prev"`
	Hash   string          `json:"hash"`
}

var expIDPattern = regexp.MustCompile(`^[a-z0-9_]{1,128}$`)

func expDir(root, expID string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("%w: no store folder", ErrInvalid)
	}
	if !expIDPattern.MatchString(expID) {
		return "", fmt.Errorf("%w: experiment id %q", ErrInvalid, expID)
	}
	return filepath.Join(root, expID), nil
}

// Read returns the experiment's controls; an experiment with none is running
// with no live settings.
func Read(root, expID string) (*State, error) {
	dir, err := expDir(root, expID)
	if err != nil {
		return nil, err
	}
	return readState(dir)
}

func readState(dir string) (*State, error) {
	st := &State{Settings: Settings{Mode: ModeRunning}}
	b, err := os.ReadFile(filepath.Join(dir, StateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return stateFromLog(dir, st)
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", StateFile, err)
	}
	if err := json.Unmarshal(b, st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", StateFile, err)
	}
	if st.Mode == "" {
		st.Mode = ModeRunning
	}
	return st, nil
}

// stateFromLog rebuilds the state from the log's last entry when the state
// file is missing (a restored mirror); no log means no controls.
func stateFromLog(dir string, st *State) (*State, error) {
	log, err := readLog(dir)
	if err != nil {
		return nil, err
	}
	if len(log) == 0 {
		return st, nil
	}
	last := log[len(log)-1]
	if err := json.Unmarshal(last.After, &st.Settings); err != nil {
		return nil, fmt.Errorf("%w: entry %d: %v", ErrBrokenLog, last.Seq, err)
	}
	st.Seq, st.Head, st.UpdatedAt = last.Seq, last.Hash, last.At
	if st.Mode == "" {
		st.Mode = ModeRunning
	}
	return st, nil
}

// procLocks serializes Apply per experiment folder within one process; the
// flock does it across processes.
var procLocks sync.Map // dir -> *sync.Mutex

// Apply changes the experiment's controls under its lock: mutate edits a
// copy of the state (and may refuse with ErrInvalid or ErrConflict), then
// the new state is written and one log entry appended, chained to the last.
// The experiment folder must exist. A mutate that changes nothing writes
// nothing and returns the state as it is.
func Apply(root, expID, actor, action, target, reason string, mutate func(*State) error) (*State, error) {
	dir, err := expDir(root, expID)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("experiment %s has no store folder: %w", expID, fs.ErrNotExist)
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > MaxReasonLen {
		return nil, fmt.Errorf("%w: reason is longer than %d characters", ErrInvalid, MaxReasonLen)
	}
	if strings.TrimSpace(actor) == "" {
		actor = "system"
	}
	mu, _ := procLocks.LoadOrStore(dir, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	defer mu.(*sync.Mutex).Unlock()
	lf, err := os.OpenFile(filepath.Join(dir, lockName), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", lockName, err)
	}
	unlock, err := lockFile(lf)
	if err != nil {
		lf.Close()
		return nil, err
	}
	defer unlock()

	cur, err := readState(dir)
	if err != nil {
		return nil, err
	}
	before, err := json.Marshal(cur.Settings)
	if err != nil {
		return nil, err
	}
	next := cloneState(cur)
	if err := mutate(next); err != nil {
		return nil, err
	}
	normalize(&next.Settings)
	after, err := json.Marshal(next.Settings)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(before, after) {
		return cur, nil
	}
	now := time.Now().UTC()
	e := Entry{Seq: cur.Seq + 1, At: now, Actor: actor, Action: action, Target: target, Reason: reason,
		Before: before, After: after, Prev: cur.Head}
	if e.Hash, err = entryHash(&e); err != nil {
		return nil, err
	}
	line, err := json.Marshal(&e)
	if err != nil {
		return nil, err
	}
	if err := appendSynced(filepath.Join(dir, LogFile), append(line, '\n')); err != nil {
		return nil, err
	}
	next.Seq, next.Head, next.UpdatedAt = e.Seq, e.Hash, now
	b, err := json.MarshalIndent(next, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(dir, StateFile), append(b, '\n')); err != nil {
		return nil, err
	}
	invalidate(dir)
	return next, nil
}

func cloneState(s *State) *State {
	b, _ := json.Marshal(s)
	out := &State{}
	_ = json.Unmarshal(b, out)
	if out.Mode == "" {
		out.Mode = ModeRunning
	}
	return out
}

// normalize sorts and de-duplicates lists and drops empty maps, so equal
// settings always marshal to equal bytes.
func normalize(s *Settings) {
	if s.Mode == "" {
		s.Mode = ModeRunning
	}
	if s.Mode == ModeRunning {
		s.PausedBy, s.PauseReason, s.PausedAt = "", "", nil
	}
	models := s.PausedModels[:0:0]
	for _, m := range s.PausedModels {
		if m = strings.TrimSpace(m); m != "" && !slices.Contains(models, m) {
			models = append(models, m)
		}
	}
	sort.Strings(models)
	s.PausedModels = models
	if len(s.PausedModels) == 0 {
		s.PausedModels = nil
	}
	if len(s.CancelledSlots) == 0 {
		s.CancelledSlots = nil
	}
	if len(s.Superseded) == 0 {
		s.Superseded = nil
	}
}

// entryHash is the sha256 of the entry's JSON with Hash empty: it covers the
// previous hash, so the log is a chain.
func entryHash(e *Entry) (string, error) {
	c := *e
	c.Hash = ""
	b, err := json.Marshal(&c)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Log returns every entry of controls.jsonl, oldest first, verified: a
// broken chain is ErrBrokenLog (the entries read so far are returned too).
func Log(root, expID string) ([]Entry, error) {
	dir, err := expDir(root, expID)
	if err != nil {
		return nil, err
	}
	return readLog(dir)
}

func readLog(dir string) ([]Entry, error) {
	b, err := os.ReadFile(filepath.Join(dir, LogFile))
	if errors.Is(err, fs.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", LogFile, err)
	}
	out := []Entry{}
	prev := ""
	for i, line := range bytes.Split(b, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return out, fmt.Errorf("%w: line %d: %v", ErrBrokenLog, i+1, err)
		}
		h, err := entryHash(&e)
		if err != nil {
			return out, err
		}
		switch {
		case h != e.Hash:
			return out, fmt.Errorf("%w: entry %d hash %s, computed %s", ErrBrokenLog, e.Seq, e.Hash, h)
		case e.Prev != prev:
			return out, fmt.Errorf("%w: entry %d links to %q, the entry before is %q", ErrBrokenLog, e.Seq, e.Prev, prev)
		case e.Seq != len(out)+1:
			return out, fmt.Errorf("%w: entry %d is at position %d", ErrBrokenLog, e.Seq, len(out)+1)
		}
		prev = e.Hash
		out = append(out, e)
	}
	return out, nil
}

// Verify checks the log's chain and that controls.json is the state its last
// entry produced.
func Verify(root, expID string) error {
	dir, err := expDir(root, expID)
	if err != nil {
		return err
	}
	log, err := readLog(dir)
	if err != nil {
		return err
	}
	st, err := readState(dir)
	if err != nil {
		return err
	}
	if len(log) == 0 {
		if st.Seq != 0 || st.Head != "" {
			return fmt.Errorf("%w: %s is at entry %d but the log is empty", ErrBrokenLog, StateFile, st.Seq)
		}
		return nil
	}
	last := log[len(log)-1]
	if st.Seq != last.Seq || st.Head != last.Hash {
		return fmt.Errorf("%w: %s is at entry %d (%s), the log ends at %d (%s)", ErrBrokenLog, StateFile, st.Seq, st.Head, last.Seq, last.Hash)
	}
	got, err := json.Marshal(st.Settings)
	if err != nil {
		return err
	}
	var want Settings
	if err := json.Unmarshal(last.After, &want); err != nil {
		return fmt.Errorf("%w: entry %d: %v", ErrBrokenLog, last.Seq, err)
	}
	wb, _ := json.Marshal(want)
	if !bytes.Equal(got, wb) {
		return fmt.Errorf("%w: %s differs from the settings entry %d recorded", ErrBrokenLog, StateFile, last.Seq)
	}
	return nil
}

// supersededCache keeps each folder's superseded run ids by the state file's
// size and mtime, so the store can ask once per slot without re-reading.
var supersededCache sync.Map // dir -> cachedSet

type cachedSet struct {
	size int64
	mod  time.Time
	set  map[string]bool
}

func invalidate(dir string) { supersededCache.Delete(dir) }

// SupersededRuns returns the run ids requeued in the experiment folder dir
// (the store's <root>/<exp_id>). A folder with no controls has none. An
// unreadable state is an error: a reader must not count a superseded run.
func SupersededRuns(dir string) (map[string]bool, error) {
	fi, err := os.Stat(filepath.Join(dir, StateFile))
	if errors.Is(err, fs.ErrNotExist) {
		fi, err = os.Stat(filepath.Join(dir, LogFile))
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	if c, ok := supersededCache.Load(dir); ok {
		cs := c.(cachedSet)
		if cs.size == fi.Size() && cs.mod.Equal(fi.ModTime()) {
			return cs.set, nil
		}
	}
	st, err := readState(dir)
	if err != nil {
		return nil, err
	}
	set := make(map[string]bool, len(st.Superseded))
	for id := range st.Superseded {
		set[id] = true
	}
	supersededCache.Store(dir, cachedSet{size: fi.Size(), mod: fi.ModTime(), set: set})
	return set, nil
}

// Patch is a live settings change (PATCH /experiments/{id}/controls). A nil
// field is left as it is; Concurrency 0 or TotalUSD 0 clears the override.
type Patch struct {
	Concurrency  *int      `json:"concurrency,omitempty"`
	TotalUSD     *float64  `json:"total_usd,omitempty"`
	PausedModels *[]string `json:"paused_models,omitempty"`
	Reason       string    `json:"reason,omitempty"`
}

// Empty reports a patch that sets nothing.
func (p *Patch) Empty() bool {
	return p == nil || (p.Concurrency == nil && p.TotalUSD == nil && p.PausedModels == nil)
}

// Validate checks the patch's values. models, when not nil, lists the models
// the experiment runs; a paused model must be one of them.
func (p *Patch) Validate(models []string) error {
	if p.Empty() {
		return fmt.Errorf("%w: set at least one of concurrency, total_usd, paused_models", ErrInvalid)
	}
	if c := p.Concurrency; c != nil && (*c < 0 || *c > MaxConcurrency) {
		return fmt.Errorf("%w: concurrency must be 0 (use the experiment's) to %d, got %d", ErrInvalid, MaxConcurrency, *c)
	}
	if u := p.TotalUSD; u != nil && (*u < 0 || math.IsNaN(*u) || math.IsInf(*u, 0) || *u > 1e6) {
		return fmt.Errorf("%w: total_usd must be 0 (use the frozen cap) or a positive amount up to 1000000", ErrInvalid)
	}
	if pm := p.PausedModels; pm != nil {
		if len(*pm) > maxModels {
			return fmt.Errorf("%w: at most %d paused models", ErrInvalid, maxModels)
		}
		for _, m := range *pm {
			if models != nil && !slices.Contains(models, strings.TrimSpace(m)) {
				return fmt.Errorf("%w: model %q is not run by this experiment (it runs %s)", ErrInvalid, m, strings.Join(models, ", "))
			}
		}
	}
	return nil
}

// ApplyTo sets the patch's fields on s.
func (p *Patch) ApplyTo(s *State) {
	if p.Concurrency != nil {
		s.Concurrency = *p.Concurrency
	}
	if p.TotalUSD != nil {
		s.TotalUSD = *p.TotalUSD
	}
	if p.PausedModels != nil {
		s.PausedModels = slices.Clone(*p.PausedModels)
	}
}

// Pause drains the experiment. Pausing a paused experiment is a no-op.
func Pause(root, expID, actor, reason string) (*State, error) {
	return Apply(root, expID, actor, ActionPause, "", reason, func(s *State) error {
		if s.Mode == ModePaused {
			return nil
		}
		now := time.Now().UTC()
		s.Mode, s.PausedBy, s.PauseReason, s.PausedAt = ModePaused, actor, strings.TrimSpace(reason), &now
		return nil
	})
}

// Resume clears a pause. Resuming a running experiment is a no-op.
func Resume(root, expID, actor, reason string) (*State, error) {
	return Apply(root, expID, actor, ActionResume, "", reason, func(s *State) error {
		s.Mode = ModeRunning
		return nil
	})
}
