package shadowreplay

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/maximhq/bifrost/bench/replayrec"
)

// Dir is the folder under the org project root.
const Dir = "replays"

// Limits of the settings.
const (
	DefaultMaxSessions = 50
	DefaultMaxUSD      = 10.0
	CapMaxSessions     = replayrec.MaxSessions
	CapMaxUSD          = 100.0
	ListMax            = 50
)

// OffText is the refusal while shadow replay is off.
const OffText = "Shadow replay is off for this organization. An admin turns it on on the Replays page."

// ErrOff is returned while the org has not opted in.
var ErrOff = errors.New(OffText)

// ErrNotFound is returned for a missing report or recording.
var ErrNotFound = errors.New("not found")

// Settings is replays/settings.json: the org's opt-in and caps.
type Settings struct {
	Enabled           bool       `json:"enabled"`
	EnabledBy         string     `json:"enabled_by"`
	EnabledAt         *time.Time `json:"enabled_at"`
	MaxSessionsPerRun int        `json:"max_sessions_per_run"`
	MaxUSDPerRun      float64    `json:"max_usd_per_run"`
}

// DefaultSettings is the opt-in before an admin changes it: off.
func DefaultSettings() Settings {
	return Settings{MaxSessionsPerRun: DefaultMaxSessions, MaxUSDPerRun: DefaultMaxUSD}
}

func settingsPath(root string) string { return filepath.Join(root, Dir, "settings.json") }

// LoadSettings reads the settings; a missing or broken file is the default
// (off).
func LoadSettings(root string) Settings {
	s := DefaultSettings()
	b, err := os.ReadFile(settingsPath(root))
	if err != nil {
		return s
	}
	var got Settings
	if json.Unmarshal(b, &got) != nil {
		return s
	}
	if got.MaxSessionsPerRun <= 0 {
		got.MaxSessionsPerRun = DefaultMaxSessions
	}
	if got.MaxUSDPerRun <= 0 {
		got.MaxUSDPerRun = DefaultMaxUSD
	}
	return got
}

// CheckEnabled refuses while the org has not opted in.
func CheckEnabled(root string) error {
	if !LoadSettings(root).Enabled {
		return ErrOff
	}
	return nil
}

// SaveSettings validates and writes the settings. by is the person's name
// or email, recorded when the opt-in turns on.
func SaveSettings(root string, s Settings, by string, now time.Time) (Settings, error) {
	if s.MaxSessionsPerRun <= 0 || s.MaxSessionsPerRun > CapMaxSessions {
		return s, fmt.Errorf("max_sessions_per_run must be between 1 and %d", CapMaxSessions)
	}
	if s.MaxUSDPerRun <= 0 || s.MaxUSDPerRun > CapMaxUSD {
		return s, fmt.Errorf("max_usd_per_run must be more than 0 and at most %g", CapMaxUSD)
	}
	old := LoadSettings(root)
	switch {
	case s.Enabled && !old.Enabled:
		t := now.UTC()
		s.EnabledBy, s.EnabledAt = by, &t
	case s.Enabled:
		s.EnabledBy, s.EnabledAt = old.EnabledBy, old.EnabledAt
	default:
		s.EnabledBy, s.EnabledAt = "", nil
	}
	return s, writeJSON(settingsPath(root), s)
}

var idRE = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

// ValidID says whether an id may name a file.
func ValidID(id string) bool { return idRE.MatchString(id) }

// SaveReport writes replays/<id>.json.
func SaveReport(root string, rep *RunReport) error {
	if !ValidID(rep.ID) {
		return fmt.Errorf("bad replay id %q", rep.ID)
	}
	return writeJSON(filepath.Join(root, Dir, rep.ID+".json"), rep)
}

// LoadReport reads replays/<id>.json.
func LoadReport(root, id string) (*RunReport, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(filepath.Join(root, Dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var rep RunReport
	if err := json.Unmarshal(b, &rep); err != nil {
		return nil, err
	}
	return &rep, nil
}

// ReportRow is one entry of the replay list.
type ReportRow struct {
	ID        string         `json:"id"`
	Mode      string         `json:"mode"`
	Candidate string         `json:"candidate"`
	Sessions  int            `json:"sessions"`
	Counts    map[string]int `json:"counts"`
	SpentUSD  float64        `json:"spent_usd"`
	CreatedAt time.Time      `json:"created_at"`
}

// ListReports lists the reports newest first, at most ListMax.
func ListReports(root string) ([]ReportRow, error) {
	ents, err := os.ReadDir(filepath.Join(root, Dir))
	if errors.Is(err, os.ErrNotExist) {
		return []ReportRow{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []ReportRow{}
	for _, e := range ents {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".json" || name == "settings.json" {
			continue
		}
		rep, err := LoadReport(root, name[:len(name)-len(".json")])
		if err != nil || rep.ID == "" {
			continue
		}
		out = append(out, ReportRow{ID: rep.ID, Mode: rep.Mode, Candidate: rep.Candidate, Sessions: len(rep.Sessions), Counts: rep.Counts,
			SpentUSD: rep.SpentUSD, CreatedAt: rep.CreatedAt})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if len(out) > ListMax {
		out = out[:ListMax]
	}
	return out, nil
}

func recordingPath(root, id string) string {
	return filepath.Join(root, Dir, "recordings", id+".json")
}

// SaveRecording writes replays/recordings/<id>.json once; a later fetch of
// the same session replaces it only when it has more calls. A recording
// over replayrec.MaxRecordingBytes is refused.
func SaveRecording(root string, rec *replayrec.Recording) error {
	if !ValidID(rec.ID) {
		return fmt.Errorf("bad recording id %q", rec.ID)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if len(b) > replayrec.MaxRecordingBytes {
		return fmt.Errorf("the recording of session %s is over %d MiB and is not kept", rec.SessionID, replayrec.MaxRecordingBytes>>20)
	}
	if old, err := LoadRecording(root, rec.ID); err == nil && len(old.Calls) >= len(rec.Calls) {
		return nil
	}
	return writeFile(recordingPath(root, rec.ID), b)
}

// LoadRecording reads replays/recordings/<id>.json.
func LoadRecording(root, id string) (*replayrec.Recording, error) {
	if !ValidID(id) {
		return nil, ErrNotFound
	}
	b, err := os.ReadFile(recordingPath(root, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var rec replayrec.Recording
	if err := json.Unmarshal(b, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, b)
}

// writeFile replaces a file atomically.
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return nil
}
