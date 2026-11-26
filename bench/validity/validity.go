// Package validity keeps the validity ledger (W4-49 E): for every re-score
// of an experiment, the result before and after it, and whether the result
// flipped. It also keeps the list of experiments a check update could
// change if re-scored (the stale list). Everything lives in files under the
// project's validity/ folder: ledger.jsonl (one Entry per re-score,
// rewritten under a lock) and stale.json (replaced by each scan).
package validity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// Dir is the ledger folder under the project root.
const Dir = "validity"

// File names under Dir.
const (
	LedgerFile = "ledger.jsonl"
	StaleFile  = "stale.json"
)

// MaxFillPerRequest caps the re-scores one read compares.
const MaxFillPerRequest = 20

// Side is one result of an experiment: its grade source and verdict.
type Side struct {
	Source         string   `json:"source"` // "original" or a regrade id
	Decision       string   `json:"decision"`
	Action         string   `json:"action,omitempty"`
	InvalidReasons []string `json:"invalid_reasons,omitempty"`
}

// Entry is one re-score of one experiment.
type Entry struct {
	ID        string            `json:"id"` // exp id + ":" + regrade id
	ExpID     string            `json:"exp_id"`
	RegradeID string            `json:"regrade_id"`
	Note      string            `json:"note,omitempty"`  // the regrade's note
	Gates     map[string]string `json:"gates,omitempty"` // gate versions the regrade used
	Before    Side              `json:"before"`          // the previous regrade of this experiment, or "original"
	After     Side              `json:"after"`
	Flipped   bool              `json:"flipped"` // Decision or Action differ
	At        time.Time         `json:"at"`      // the regrade's CreatedAt
	SeenBy    []string          `json:"seen_by,omitempty"`
}

// Stale is an experiment whose sealed runs used a gate version other than
// the current one.
type Stale struct {
	ExpID   string               `json:"exp_id"`
	Gates   map[string][2]string `json:"gates"` // gate -> [sealed version, current version]
	Checked time.Time            `json:"checked"`
}

// RegradeMeta is what the ledger records of a regrade.
type RegradeMeta struct {
	ID        string
	CreatedAt time.Time
	Note      string
	Gates     map[string]string
}

// EntryID is the id of one experiment's re-score.
func EntryID(expID, regradeID string) string { return expID + ":" + regradeID }

// sideOf reads the verdict of an analysis.
func sideOf(source string, a *schemas.Analysis) Side {
	s := Side{Source: source}
	if a == nil {
		return s
	}
	s.Decision, s.Action = string(a.Verdict.Decision), string(a.Verdict.Action)
	s.InvalidReasons = slices.Clone(a.Verdict.InvalidReasons)
	return s
}

// Diff compares two analyses of one experiment: before is the analysis of
// the previous source, after the one of the regrade.
func Diff(expID, regradeID string, before, after *schemas.Analysis, info RegradeMeta) Entry {
	bs := schemas.GradeSourceOriginal
	if before != nil && before.GradeSource != "" {
		bs = before.GradeSource
	}
	e := Entry{ID: EntryID(expID, regradeID), ExpID: expID, RegradeID: regradeID, Note: info.Note, Gates: info.Gates,
		Before: sideOf(bs, before), After: sideOf(regradeID, after), At: info.CreatedAt.UTC()}
	e.Flipped = e.Before.Decision != e.After.Decision || e.Before.Action != e.After.Action
	return e
}

// Store keeps the ledger and the stale list.
type Store interface {
	Entries() ([]Entry, error)
	// Add stores entries whose id is not there yet and returns the ones it
	// stored.
	Add(es []Entry) ([]Entry, error)
	// MarkSeen adds user to an entry's seen list; false when no entry has
	// that id.
	MarkSeen(id, user string) (bool, error)
	Stale() ([]Stale, error)
	SetStale(s []Stale) error
}

// Open returns the file store under projectRoot/validity.
func Open(projectRoot string) Store { return &fileStore{dir: filepath.Join(projectRoot, Dir)} }

type fileStore struct{ dir string }

var locks sync.Map // dir -> *sync.Mutex

func (s *fileStore) lock() func() {
	m, _ := locks.LoadOrStore(s.dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *fileStore) read() ([]Entry, error) {
	b, err := os.ReadFile(filepath.Join(s.dir, LedgerFile))
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e Entry
		if json.Unmarshal([]byte(line), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *fileStore) write(es []Entry) error {
	var b strings.Builder
	for _, e := range es {
		line, err := json.Marshal(e)
		if err != nil {
			return err
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return writeAtomic(filepath.Join(s.dir, LedgerFile), []byte(b.String()))
}

// writeAtomic replaces path through a temporary file in the same folder.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (s *fileStore) Entries() ([]Entry, error) {
	defer s.lock()()
	return s.read()
}

func (s *fileStore) Add(add []Entry) ([]Entry, error) {
	if len(add) == 0 {
		return nil, nil
	}
	defer s.lock()()
	es, err := s.read()
	if err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, e := range es {
		have[e.ID] = true
	}
	var stored []Entry
	for _, e := range add {
		if have[e.ID] {
			continue
		}
		have[e.ID] = true
		es = append(es, e)
		stored = append(stored, e)
	}
	if len(stored) == 0 {
		return nil, nil
	}
	return stored, s.write(es)
}

func (s *fileStore) MarkSeen(id, user string) (bool, error) {
	defer s.lock()()
	es, err := s.read()
	if err != nil {
		return false, err
	}
	for i := range es {
		if es[i].ID != id {
			continue
		}
		if !slices.Contains(es[i].SeenBy, user) {
			es[i].SeenBy = append(es[i].SeenBy, user)
		}
		return true, s.write(es)
	}
	return false, nil
}

func (s *fileStore) Stale() ([]Stale, error) {
	defer s.lock()()
	b, err := os.ReadFile(filepath.Join(s.dir, StaleFile))
	if errors.Is(err, os.ErrNotExist) {
		return []Stale{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Stale
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("parse %s: %w", StaleFile, err)
	}
	return out, nil
}

func (s *fileStore) SetStale(st []Stale) error {
	if st == nil {
		st = []Stale{}
	}
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	defer s.lock()()
	return writeAtomic(filepath.Join(s.dir, StaleFile), b)
}

// Seen says whether user dismissed the entry.
func (e Entry) Seen(user string) bool { return slices.Contains(e.SeenBy, user) }

// Analyze reads one analysis of an experiment with an explicit source.
type Analyze func(ctx context.Context, expID, gradeSource string) (*schemas.Analysis, error)

// Fill compares the re-scores of the given experiments that have no entry
// yet, at most max of them, oldest first, and stores the entries. regrades
// lists one experiment's regrades in any order. It returns the entries it
// stored; the caller tells people about the flipped ones.
func Fill(ctx context.Context, st Store, expIDs []string, regrades func(ctx context.Context, expID string) ([]RegradeMeta, error), analyze Analyze, max int) ([]Entry, error) {
	have, err := st.Entries()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, e := range have {
		known[e.ID] = true
	}
	var made []Entry
	for _, expID := range expIDs {
		if len(made) >= max {
			break
		}
		rs, err := regrades(ctx, expID)
		if err != nil || len(rs) == 0 {
			continue
		}
		sort.SliceStable(rs, func(i, j int) bool {
			if !rs[i].CreatedAt.Equal(rs[j].CreatedAt) {
				return rs[i].CreatedAt.Before(rs[j].CreatedAt)
			}
			return rs[i].ID < rs[j].ID
		})
		prev := schemas.GradeSourceOriginal
		for _, r := range rs {
			if len(made) >= max {
				break
			}
			if known[EntryID(expID, r.ID)] {
				prev = r.ID
				continue
			}
			before, err := analyze(ctx, expID, prev)
			if err != nil {
				break
			}
			after, err := analyze(ctx, expID, r.ID)
			if err != nil {
				break
			}
			e := Diff(expID, r.ID, before, after, r)
			e.Before.Source = prev
			made = append(made, e)
			known[e.ID] = true
			prev = r.ID
		}
	}
	return st.Add(made)
}

// StaleOf compares the gate versions of up to three sealed runs with the
// current ones. ok is false when every version matches.
func StaleOf(expID string, current map[string]string, sealed [][]schemas.GateResult, now time.Time) (Stale, bool) {
	s := Stale{ExpID: expID, Gates: map[string][2]string{}, Checked: now.UTC()}
	for i, gates := range sealed {
		if i >= 3 {
			break
		}
		for _, g := range gates {
			cur, ok := current[g.Gate]
			if !ok || g.Version == "" || cur == g.Version {
				continue
			}
			if _, seen := s.Gates[g.Gate]; !seen {
				s.Gates[g.Gate] = [2]string{g.Version, cur}
			}
		}
	}
	return s, len(s.Gates) > 0
}
