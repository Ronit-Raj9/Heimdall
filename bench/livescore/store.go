package livescore

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// State is state.json: how far pulls got, the running comparison and which
// alerts are open.
type State struct {
	Cursor  Cursor                `json:"cursor"`
	Seq     int64                 `json:"seq"` // the last session sequence number given out
	Compare *CompareState         `json:"compare,omitempty"`
	Alerts  map[string]AlertState `json:"alerts,omitempty"`
}

// Cursor is how far pulls got. Rows before Since minus the overlap are
// done; Scored holds the ids of rows at or after that already scored (or
// skipped by sampling), so the overlap never scores a row twice.
type Cursor struct {
	Since      time.Time            `json:"since,omitzero"`
	Scored     map[string]time.Time `json:"scored,omitempty"`
	LastPull   time.Time            `json:"last_pull,omitzero"`
	LastError  string               `json:"last_error,omitempty"`
	LastScored int                  `json:"last_scored,omitempty"`
	Warnings   []string             `json:"warnings,omitempty"`
}

// AlertState is one alert rule's last state: open alerts are not sent
// again until they close.
type AlertState struct {
	Open  bool      `json:"open"`
	Since time.Time `json:"since"`
	Value float64   `json:"value,omitempty"`
}

// LogEvent is one line of log.jsonl.
type LogEvent struct {
	At     time.Time `json:"at"`
	Event  string    `json:"event"` // pull | alert | decision | restart | prune
	Detail string    `json:"detail,omitempty"`
}

// Store keeps live scores as files under one folder. A flock on .lock
// serializes writers across processes, a mutex within one.
type Store struct {
	dir string
	mu  sync.Mutex
}

// NewStore returns a store rooted at dir (the project's livescore folder).
func NewStore(dir string) *Store { return &Store{dir: dir} }

// Dir is the folder.
func (st *Store) Dir() string { return st.dir }

// lock takes the store's mutex and flock.
func (st *Store) lock() (func(), error) {
	st.mu.Lock()
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		st.mu.Unlock()
		return nil, fmt.Errorf("create live scoring folder: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(st.dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		st.mu.Unlock()
		return nil, fmt.Errorf("open live scoring lock: %w", err)
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

// State reads state.json; a missing file is an empty state.
func (st *Store) State() (*State, error) {
	data, err := os.ReadFile(filepath.Join(st.dir, StateFile))
	if errors.Is(err, os.ErrNotExist) {
		return &State{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read live scoring state: %w", err)
	}
	s := &State{}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("read live scoring state: %w", err)
	}
	return s, nil
}

func (st *Store) saveState(s *State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(st.dir, StateFile), append(data, '\n'))
}

// Sessions reads every scored session, oldest first. A session id recorded
// more than once (an append that a crashed pull never checkpointed, then
// repeated) is returned once, its first record.
func (st *Store) Sessions() ([]SessionRecord, error) {
	f, err := os.Open(filepath.Join(st.dir, SessionsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read scored sessions: %w", err)
	}
	defer f.Close()
	var out []SessionRecord
	sc := bufio.NewScanner(io.LimitReader(f, maxSessionsFileLen))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	n := 0
	var bad error
	seen := map[string]bool{}
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if bad != nil {
			return nil, bad // a damaged line that is not the last one
		}
		var r SessionRecord
		if err := json.Unmarshal(line, &r); err != nil {
			// A reader can meet the last line half written by a pull that
			// is appending; that line is skipped. Any other damage is an
			// error.
			bad = fmt.Errorf("read scored sessions: line %d: %w", n, err)
			continue
		}
		if seen[r.ID] {
			continue
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	return out, sc.Err()
}

// appendSessions appends records to sessions.jsonl.
func (st *Store) appendSessions(recs []SessionRecord) error {
	if len(recs) == 0 {
		return nil
	}
	var buf bytes.Buffer
	for i := range recs {
		b, err := json.Marshal(&recs[i])
		if err != nil {
			return err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	f, err := os.OpenFile(filepath.Join(st.dir, SessionsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(buf.Bytes())
	return errors.Join(werr, f.Close())
}

// prune rewrites sessions.jsonl without sessions that ended before cut and
// beyond MaxSessions. It returns how many it dropped.
func (st *Store) prune(cut time.Time) (int, error) {
	all, err := st.Sessions()
	if err != nil {
		return 0, err
	}
	keep := all[:0:0]
	for _, r := range all {
		if !r.Ended.Before(cut) {
			keep = append(keep, r)
		}
	}
	if len(keep) > MaxSessions {
		keep = keep[len(keep)-MaxSessions:]
	}
	dropped := len(all) - len(keep)
	if dropped == 0 {
		return 0, nil
	}
	var buf bytes.Buffer
	for i := range keep {
		b, err := json.Marshal(&keep[i])
		if err != nil {
			return 0, err
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	return dropped, writeAtomic(filepath.Join(st.dir, SessionsFile), buf.Bytes())
}

// Log appends one event. A line is capped.
func (st *Store) Log(ev LogEvent) error {
	if len(ev.Detail) > maxLogLineBytes-200 {
		ev.Detail = ev.Detail[:maxLogLineBytes-200] + "..."
	}
	line, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(st.dir, LogFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(append(line, '\n'))
	return errors.Join(werr, f.Close())
}

// Events reads the newest n log events, newest last.
func (st *Store) Events(n int) ([]LogEvent, error) {
	data, err := os.ReadFile(filepath.Join(st.dir, LogFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []LogEvent
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var ev LogEvent
		if json.Unmarshal(line, &ev) == nil {
			out = append(out, ev)
		}
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
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
