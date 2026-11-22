package statuspage

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Incident statuses.
const (
	StatusInvestigating = "investigating"
	StatusIdentified    = "identified"
	StatusMonitoring    = "monitoring"
	StatusResolved      = "resolved"
	StatusScheduled     = "scheduled" // planned maintenance, announced ahead
)

var statuses = map[string]bool{StatusInvestigating: true, StatusIdentified: true, StatusMonitoring: true, StatusResolved: true, StatusScheduled: true}

// Impact is how bad an incident is for the components it names.
type Impact string

// Impacts.
const (
	ImpactNone        Impact = "none"
	ImpactMinor       Impact = "minor"
	ImpactMajor       Impact = "major"
	ImpactCritical    Impact = "critical"
	ImpactMaintenance Impact = "maintenance"
)

// Level is the component level an open incident of this impact sets.
func (i Impact) Level() Level {
	switch i {
	case ImpactMinor:
		return Degraded
	case ImpactMajor:
		return PartialOutage
	case ImpactCritical:
		return MajorOutage
	case ImpactMaintenance:
		return Maintenance
	}
	return Operational
}

func (i Impact) valid() bool {
	switch i {
	case ImpactNone, ImpactMinor, ImpactMajor, ImpactCritical, ImpactMaintenance:
		return true
	}
	return false
}

// Update is one note on an incident's timeline.
type Update struct {
	At      time.Time `json:"at"`
	Status  string    `json:"status"`
	Message string    `json:"message"`
}

// Incident is an operator's note about a problem or planned maintenance.
type Incident struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Status       string     `json:"status"`
	Impact       Impact     `json:"impact"`
	Components   []string   `json:"components,omitempty"` // component ids; none means the whole platform
	StartedAt    time.Time  `json:"started_at"`
	ScheduledFor *time.Time `json:"scheduled_for,omitempty"` // maintenance start, when announced ahead
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	Updates      []Update   `json:"updates"` // oldest first
}

// Limits on operator text, which is shown publicly as plain text.
const (
	maxTitle     = 160
	maxMessage   = 2000
	maxIncidents = 500
)

// ErrNotFound is an unknown incident id.
var ErrNotFound = errors.New("no such incident")

// Store keeps incidents in one JSON file. Writes go through a temp file and
// a rename; reads pick up edits from another process (the CLI) by mtime.
type Store struct {
	path string
	now  func() time.Time

	mu    sync.Mutex
	mtime time.Time
	size  int64
	cache []Incident
}

// NewStore returns a store over path; the file may not exist yet.
func NewStore(path string) *Store { return &Store{path: path, now: time.Now} }

// Path is the file the store reads.
func (s *Store) Path() string { return s.path }

type fileDoc struct {
	Incidents []Incident `json:"incidents"`
}

// List returns every incident, newest first.
func (s *Store) List() ([]Incident, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return nil, err
	}
	return cloneAll(s.cache), nil
}

func (s *Store) loadLocked() error {
	st, err := os.Stat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.cache, s.mtime, s.size = nil, time.Time{}, 0
		return nil
	}
	if err != nil {
		return err
	}
	if s.cache != nil && st.ModTime().Equal(s.mtime) && st.Size() == s.size {
		return nil
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var doc fileDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("read %s: %w", s.path, err)
	}
	if doc.Incidents == nil {
		doc.Incidents = []Incident{}
	}
	s.cache, s.mtime, s.size = doc.Incidents, st.ModTime(), st.Size()
	return nil
}

func (s *Store) saveLocked(list []Incident) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(fileDoc{Incidents: list}, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".incidents-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	s.cache = nil // reread on next load (mtime may not move within its resolution)
	return nil
}

// Open is a new incident.
type Open struct {
	Title        string
	Impact       Impact
	Status       string // default investigating, or scheduled
	Components   []string
	Message      string
	ScheduledFor *time.Time
}

// Open records a new incident and returns it.
func (s *Store) Open(o Open) (Incident, error) {
	if o.Status == "" {
		o.Status = StatusInvestigating
	}
	if o.Impact == "" {
		o.Impact = ImpactMinor
	}
	if o.Status == StatusResolved {
		return Incident{}, errors.New("a new incident cannot start resolved")
	}
	if err := checkText(o.Title, maxTitle, "title"); err != nil {
		return Incident{}, err
	}
	if err := checkText(o.Message, maxMessage, "message"); err != nil {
		return Incident{}, err
	}
	if !statuses[o.Status] {
		return Incident{}, fmt.Errorf("status %q: use investigating, identified, monitoring or scheduled", o.Status)
	}
	if !o.Impact.valid() {
		return Incident{}, fmt.Errorf("impact %q: use none, minor, major, critical or maintenance", o.Impact)
	}
	comps, err := checkComponents(o.Components)
	if err != nil {
		return Incident{}, err
	}
	now := s.now().UTC()
	in := Incident{ID: newID(), Title: strings.TrimSpace(o.Title), Status: o.Status, Impact: o.Impact, Components: comps,
		StartedAt: now, Updates: []Update{{At: now, Status: o.Status, Message: strings.TrimSpace(o.Message)}}}
	if o.ScheduledFor != nil {
		t := o.ScheduledFor.UTC()
		in.ScheduledFor = &t
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Incident{}, err
	}
	list := append([]Incident{in}, s.cache...)
	if len(list) > maxIncidents {
		list = list[:maxIncidents]
	}
	if err := s.saveLocked(list); err != nil {
		return Incident{}, err
	}
	return in, nil
}

// Change is an update to an incident; empty fields keep their value.
type Change struct {
	Status  string
	Impact  Impact
	Message string
}

// Update adds a timeline note and changes the status or impact. Status
// resolved sets the resolution time.
func (s *Store) Update(id string, c Change) (Incident, error) {
	if err := checkText(c.Message, maxMessage, "message"); err != nil {
		return Incident{}, err
	}
	if c.Status != "" && !statuses[c.Status] {
		return Incident{}, fmt.Errorf("status %q: use investigating, identified, monitoring, scheduled or resolved", c.Status)
	}
	if c.Impact != "" && !c.Impact.valid() {
		return Incident{}, fmt.Errorf("impact %q: use none, minor, major, critical or maintenance", c.Impact)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return Incident{}, err
	}
	list := cloneAll(s.cache)
	for i := range list {
		in := &list[i]
		if in.ID != id {
			continue
		}
		now := s.now().UTC()
		if c.Status != "" {
			in.Status = c.Status
		}
		if c.Impact != "" {
			in.Impact = c.Impact
		}
		if in.Status == StatusResolved {
			if in.ResolvedAt == nil {
				in.ResolvedAt = &now
			}
		} else {
			in.ResolvedAt = nil
		}
		in.Updates = append(in.Updates, Update{At: now, Status: in.Status, Message: strings.TrimSpace(c.Message)})
		if err := s.saveLocked(list); err != nil {
			return Incident{}, err
		}
		return clone(*in), nil
	}
	return Incident{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Remove deletes an incident (a note posted by mistake).
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadLocked(); err != nil {
		return err
	}
	list := cloneAll(s.cache)
	for i := range list {
		if list[i].ID == id {
			return s.saveLocked(append(list[:i:i], list[i+1:]...))
		}
	}
	return fmt.Errorf("%w: %s", ErrNotFound, id)
}

func checkText(s string, max int, what string) error {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return fmt.Errorf("%s is required", what)
	case utf8.RuneCountInString(s) > max:
		return fmt.Errorf("%s is longer than %d characters", what, max)
	case !utf8.ValidString(s):
		return fmt.Errorf("%s is not valid UTF-8", what)
	}
	return nil
}

func checkComponents(ids []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if len(id) > 32 || strings.Trim(id, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
			return nil, fmt.Errorf("component %q: ids are lower case letters, digits and dashes", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

func newID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "inc_" + hex.EncodeToString(b[:])
}

func clone(in Incident) Incident {
	in.Components = append([]string(nil), in.Components...)
	in.Updates = append([]Update(nil), in.Updates...)
	return in
}

func cloneAll(list []Incident) []Incident {
	out := make([]Incident, len(list))
	for i, in := range list {
		out[i] = clone(in)
	}
	return out
}
