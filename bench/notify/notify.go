// Package notify holds an organization's email notification state (lane
// Y05, audit G4-08): each member's own choice of which events to get by
// email and whether to get the weekly digest, the history of finished
// evaluations the digest summarises, and the Hub that renders and sends
// those emails through package mail.
//
// Files live under <project root>/.heimdall/notifications/ (0600), outside
// every area the project files API serves:
//
//	prefs.json     user id -> Prefs
//	history.jsonl  one Record per finished evaluation, newest last
//	digest.json    the ISO week the last digest went out
package notify

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/webhooks"
)

// EmailEvents are the events a member can get by email.
var EmailEvents = []string{webhooks.EventEvaluationFinished, webhooks.EventEvaluationRegressed, webhooks.EventReleaseBlocked,
	webhooks.EventScheduleFailed, webhooks.EventWorkerOffline}

// Prefs is one member's email choice. A member with no saved choice gets
// the weekly digest and no event email.
type Prefs struct {
	Events    []string  `json:"events"`
	Digest    bool      `json:"digest"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// DefaultPrefs is the choice of a member who never saved one.
func DefaultPrefs() Prefs { return Prefs{Events: []string{}, Digest: true} }

// ErrInvalid wraps every refused input.
var ErrInvalid = errors.New("invalid notification settings")

// Store is one project's notification state.
type Store struct{ dir string }

var mu sync.Mutex

// Open returns the store of a project root. Nothing is created until a write.
func Open(projectRoot string) *Store {
	return &Store{dir: filepath.Join(projectRoot, ".heimdall", "notifications")}
}

func (s *Store) path(name string) string { return filepath.Join(s.dir, name) }

func (s *Store) readJSON(name string, v any) error {
	b, err := os.ReadFile(s.path(name))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s is not valid JSON: %w", name, err)
	}
	return nil
}

func (s *Store) writeFile(name string, b []byte) error {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.path(name))
}

func (s *Store) writeJSON(name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.writeFile(name, b)
}

// AllPrefs returns every saved choice by user id.
func (s *Store) AllPrefs() (map[string]Prefs, error) {
	mu.Lock()
	defer mu.Unlock()
	m := map[string]Prefs{}
	return m, s.readJSON("prefs.json", &m)
}

// Prefs returns one member's choice (DefaultPrefs when none is saved).
func (s *Store) Prefs(userID string) (Prefs, error) {
	all, err := s.AllPrefs()
	if err != nil {
		return Prefs{}, err
	}
	if p, ok := all[userID]; ok {
		if p.Events == nil {
			p.Events = []string{}
		}
		return p, nil
	}
	return DefaultPrefs(), nil
}

// SetPrefs saves one member's choice.
func (s *Store) SetPrefs(userID string, p Prefs) (Prefs, error) {
	if userID == "" {
		return Prefs{}, fmt.Errorf("%w: email settings belong to a signed-in member", ErrInvalid)
	}
	evs := []string{}
	for _, e := range p.Events {
		if !slices.Contains(EmailEvents, e) {
			return Prefs{}, fmt.Errorf("%w: unknown event %q (one of %s)", ErrInvalid, e, strings.Join(EmailEvents, ", "))
		}
		if !slices.Contains(evs, e) {
			evs = append(evs, e)
		}
	}
	p.Events, p.UpdatedAt = evs, time.Now().UTC()
	mu.Lock()
	defer mu.Unlock()
	all := map[string]Prefs{}
	if err := s.readJSON("prefs.json", &all); err != nil {
		return Prefs{}, err
	}
	all[userID] = p
	return p, s.writeJSON("prefs.json", all)
}

// Record is one finished evaluation, as the weekly digest reads it.
type Record struct {
	At           time.Time `json:"at"`
	EvaluationID string    `json:"evaluation_id"`
	Name         string    `json:"name,omitempty"`
	Agent        string    `json:"agent,omitempty"`
	Version      int       `json:"version,omitempty"`
	Status       string    `json:"status"`
	Score        *float64  `json:"score,omitempty"`
	Change       *float64  `json:"change,omitempty"`
	Regressed    bool      `json:"regressed,omitempty"`
	Blocked      bool      `json:"blocked,omitempty"`
	CostUSD      *float64  `json:"cost_usd,omitempty"`
	Schedule     string    `json:"schedule,omitempty"`
}

// maxHistory is how many records history.jsonl keeps.
const maxHistory = 2000

// AppendHistory adds one record, keeping the newest maxHistory.
func (s *Store) AppendHistory(r Record) error {
	mu.Lock()
	defer mu.Unlock()
	old, err := s.readHistory()
	if err != nil {
		return err
	}
	all := append(old, r)
	if len(all) > maxHistory {
		all = all[len(all)-maxHistory:]
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, x := range all {
		_ = enc.Encode(x)
	}
	return s.writeFile("history.jsonl", buf.Bytes())
}

// History returns the records at or after since, oldest first.
func (s *Store) History(since time.Time) ([]Record, error) {
	mu.Lock()
	defer mu.Unlock()
	all, err := s.readHistory()
	if err != nil {
		return nil, err
	}
	out := []Record{}
	for _, r := range all {
		if !r.At.Before(since) {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b Record) int { return a.At.Compare(b.At) })
	return out, nil
}

func (s *Store) readHistory() ([]Record, error) {
	f, err := os.Open(s.path("history.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out, sc.Err()
}

type digestState struct {
	Week   string    `json:"week"`
	SentAt time.Time `json:"sent_at"`
	Sent   int       `json:"sent"`
}

// DigestWeek is the ISO week ("2026-W47") the last digest went out for.
func (s *Store) DigestWeek() (string, error) {
	mu.Lock()
	defer mu.Unlock()
	var st digestState
	return st.Week, s.readJSON("digest.json", &st)
}

// SetDigestWeek records a sent digest.
func (s *Store) SetDigestWeek(week string, sent int, at time.Time) error {
	mu.Lock()
	defer mu.Unlock()
	return s.writeJSON("digest.json", digestState{Week: week, SentAt: at.UTC(), Sent: sent})
}
