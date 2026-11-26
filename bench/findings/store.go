// Package findings keeps an organization's findings: a failure someone
// decided to track, the tasks that show it, and its life from open to fixed.
// A finding is marked fixed only by a rerun that proves it (Judge), and a
// fixed finding reopens by itself when one of its tasks fails again
// (Refresh). Findings live in findings/findings.jsonl under the project
// root, rewritten atomically under findings/.lock.
package findings

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	StateOpen     = "open"
	StateExpected = "expected" // a person said this is known and accepted; never reopens by itself
	StateFixed    = "fixed"    // set only by a proven rerun
	StateReopened = "reopened"
)

// Dir is the folder under the project root that holds findings.
const Dir = "findings"

const (
	itemsFile   = "findings.jsonl"
	lockFile    = ".lock"
	MaxFindings = 2000
	MaxEvents   = 200
	MaxTasks    = 50
	MaxRuns     = 20
	maxFileSize = 64 << 20
)

// ErrNotFound is returned for an unknown finding id.
var ErrNotFound = errors.New("finding not found")

// ErrInvalid marks a request the rules refuse; its text is for people.
var ErrInvalid = errors.New("invalid")

type Finding struct {
	ID         string    `json:"id"`     // "fnd-" + first 12 hex of sha256(Key)
	Key        string    `json:"key"`    // "inbox:<item>" | "cluster:<signature>" | "investigation:<job>:<group index>" | "check:<rule id>:<exp id>"
	Title      string    `json:"title"`  //
	Source     string    `json:"source"` // inbox | cluster | investigation | check
	Origin     string    `json:"origin"` // verified | rule | model (investigation titles are model)
	State      string    `json:"state"`
	Tasks      []TaskRef `json:"tasks"`                // the tasks that show it; at most 50
	Runs       []string  `json:"runs"`                 // example failed runs; at most 20
	Hypothesis string    `json:"hypothesis,omitempty"` // model text, shown as a hypothesis
	Quotes     []Quote   `json:"quotes,omitempty"`     // verified quotes copied from the investigation
	Fix        *FixProof `json:"fix,omitempty"`
	Reruns     []string  `json:"reruns,omitempty"` // experiment ids started from this finding
	History    []Event   `json:"history"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type TaskRef struct {
	Suite string `json:"suite"`
	Task  string `json:"task"`
}

// String is "suite/task".
func (t TaskRef) String() string { return t.Suite + "/" + t.Task }

// Quote has the same JSON as investigator.Quote; a local type so findings
// does not import investigator.
type Quote struct {
	RunID string `json:"run_id"`
	Seq   int    `json:"seq"`
	Text  string `json:"text"`
}

type FixProof struct {
	Experiment  string    `json:"experiment"` // the rerun
	Agent       string    `json:"agent"`      // agent config id
	FixedHash   string    `json:"fixed_hash"` // config hash of the fixed arm
	OldHash     string    `json:"old_hash"`
	FixedPassed int       `json:"fixed_passed"` // runs that passed, of
	FixedRuns   int       `json:"fixed_runs"`
	OldPassed   int       `json:"old_passed"`
	OldRuns     int       `json:"old_runs"`
	At          time.Time `json:"at"`
}

type Event struct {
	At         time.Time `json:"at"`
	By         string    `json:"by"` // a person's display name, or "Heimdall"
	From       string    `json:"from"`
	To         string    `json:"to"`
	Reason     string    `json:"reason"`
	Experiment string    `json:"experiment,omitempty"`
	RunID      string    `json:"run_id,omitempty"`
}

// Store keeps the findings of one project.
type Store interface {
	List() ([]Finding, error)
	Get(id string) (*Finding, error)
	Update(fn func([]Finding) ([]Finding, error)) error
}

// IDOf is the finding id of a key.
func IDOf(key string) string {
	sum := sha256.Sum256([]byte(key))
	return "fnd-" + hex.EncodeToString(sum[:])[:12]
}

// ValidID reports whether id has the shape of a finding id.
func ValidID(id string) bool {
	if len(id) != 16 || id[:4] != "fnd-" {
		return false
	}
	_, err := hex.DecodeString(id[4:])
	return err == nil
}

type fileStore struct {
	dir string
	mu  sync.Mutex
}

var (
	storesMu sync.Mutex
	stores   = map[string]*fileStore{}
)

// Open returns the store of the project at projectRoot. Nothing is written
// until the first update. Stores are shared per folder so that writers in
// one process also serialize on the mutex.
func Open(projectRoot string) Store {
	dir := filepath.Join(projectRoot, Dir)
	storesMu.Lock()
	defer storesMu.Unlock()
	if st, ok := stores[dir]; ok {
		return st
	}
	st := &fileStore{dir: dir}
	stores[dir] = st
	return st
}

func (st *fileStore) List() ([]Finding, error) { return st.read() }

func (st *fileStore) Get(id string) (*Finding, error) {
	all, err := st.read()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, ErrNotFound
}

func (st *fileStore) read() ([]Finding, error) {
	f, err := os.Open(filepath.Join(st.dir, itemsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read findings: %w", err)
	}
	defer f.Close()
	var out []Finding
	sc := bufio.NewScanner(io.LimitReader(f, maxFileSize))
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var x Finding
		if err := json.Unmarshal(line, &x); err != nil {
			return nil, fmt.Errorf("read findings: line %d: %w", n, err)
		}
		out = append(out, x)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read findings: %w", err)
	}
	return out, nil
}

func (st *fileStore) Update(fn func([]Finding) ([]Finding, error)) error {
	unlock, err := st.lock()
	if err != nil {
		return err
	}
	defer unlock()
	all, err := st.read()
	if err != nil {
		return err
	}
	out, err := fn(all)
	if err != nil {
		return err
	}
	if len(out) > MaxFindings {
		return fmt.Errorf("%w: this organization already tracks %d findings, the most it can keep", ErrInvalid, MaxFindings)
	}
	var buf bytes.Buffer
	for i := range out {
		if n := len(out[i].History); n > MaxEvents {
			out[i].History = out[i].History[n-MaxEvents:]
		}
		line, err := json.Marshal(&out[i])
		if err != nil {
			return fmt.Errorf("encode finding %s: %w", out[i].ID, err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return writeAtomic(filepath.Join(st.dir, itemsFile), buf.Bytes())
}

func (st *fileStore) lock() (func(), error) {
	st.mu.Lock()
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		st.mu.Unlock()
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(st.dir, lockFile), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		st.mu.Unlock()
		return nil, err
	}
	release, err := flock(f)
	if err != nil {
		f.Close()
		st.mu.Unlock()
		return nil, err
	}
	return func() {
		_ = release()
		st.mu.Unlock()
	}, nil
}

// writeAtomic writes data through a temporary file and a rename.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}
