// Package resultsindex keeps a derived, searchable index of results across
// experiments: one row per verified sealed run and one row per analysis.
//
// Sealed run folders stay the only source of truth. The index is a cache of
// facts read from them through the store's verified path (store.SealedRuns),
// written to a single JSON-lines file under the runs root
// (<runs root>/.index/results.jsonl). It can be deleted at any time and
// rebuilt with `bifrost-bench index rebuild`.
//
// The index is never read by the verdict, the leaderboard or a release
// decision: those keep reading sealed files and verifying their seals. It
// serves trends (package trends), search and the query command only, so a
// stale or damaged index can mislead a chart but never a release.
//
// Format: a JSON-lines file with an in-memory loader, chosen over SQLite
// because the module ships no SQLite driver, the binary is built with
// CGO_ENABLED=0, and the bench adds no third-party dependency without the
// lead. Reader is the interface a SQLite store would implement too.
package resultsindex

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Location of the index under the runs root. The dot keeps it out of
// store.ListExperiments, which only lists folders named like an experiment
// id, and bench/.gitignore already ignores the runs root.
const (
	DirName  = ".index"
	FileName = "results.jsonl"
)

// FormatVersion is written in the meta line. A file with another version is
// ignored on load (treated as empty) so an old binary never misreads a newer
// layout; the next rebuild rewrites it.
const FormatVersion = 1

// FormatName says which index implementation is in use, for `index status`.
const FormatName = "jsonl"

// RunRow is one verified sealed run, counted in its experiment. Numeric
// fields are null when the run did not observe them, never zero.
type RunRow struct {
	Experiment        string             `json:"experiment"`
	ExperimentCreated time.Time          `json:"experiment_created"`
	RunID             string             `json:"run_id"`
	Suite             string             `json:"suite"` // id@version
	Task              string             `json:"task"`
	Arm               string             `json:"arm"`
	Agent             string             `json:"agent"` // agent config id
	ConfigHash        string             `json:"config_hash"`
	Model             string             `json:"model"`
	Condition         string             `json:"condition"`
	Trial             int                `json:"trial"`
	Outcome           string             `json:"outcome"`
	Score             *float64           `json:"score"`   // the run's Y
	Success           *float64           `json:"success"` // verified success 0 or 1; null when the outcome does not score
	Metrics           map[string]float64 `json:"metrics"` // observed metric values only
	CostUSD           *float64           `json:"cost_usd"`
	WallTimeS         *float64           `json:"wall_time_s"`
	Started           time.Time          `json:"started"`
	SealHash          string             `json:"seal_hash"`
	Synthetic         bool               `json:"synthetic"`
	GradeSource       string             `json:"grade_source"`
	NotRepeatable     bool               `json:"not_repeatable,omitempty"` // the experiment called real external hosts (external: live or record)
	Problems          []string           `json:"problems,omitempty"`       // evidence problems other than a seal failure, e.g. a slot with two counted runs
}

// AnalysisRow is one analysis of an experiment (analysis/<id>/verdict.json).
type AnalysisRow struct {
	Experiment  string    `json:"experiment"`
	AnalysisID  string    `json:"analysis_id"`
	Decision    string    `json:"decision"`
	Action      string    `json:"action"`
	Policy      string    `json:"policy"` // policy decision: allow, warn, block, or "" with no policy
	GradeSource string    `json:"grade_source"`
	CreatedAt   time.Time `json:"created_at"`
}

// Rejected is a sealed run, or a whole experiment when RunID is empty, that
// was not indexed because its evidence did not verify.
type Rejected struct {
	Experiment string `json:"experiment"`
	RunID      string `json:"run_id,omitempty"`
	SealHash   string `json:"seal_hash,omitempty"`
	Reason     string `json:"reason"`
}

// Status describes the index file.
type Status struct {
	Format      string    `json:"format"`
	Path        string    `json:"path"`
	Exists      bool      `json:"exists"`
	BuiltAt     time.Time `json:"built_at"`   // last full rebuild
	UpdatedAt   time.Time `json:"updated_at"` // last write of any kind
	Experiments int       `json:"experiments"`
	Runs        int       `json:"runs"`
	Analyses    int       `json:"analyses"`
	Rejected    int       `json:"rejected"`
}

// Reader is what trends, search and the query command read. A SQLite-backed
// index would implement the same methods.
type Reader interface {
	Runs(f Filter) []RunRow
	Analyses(expID string) []AnalysisRow
	Rejected() []Rejected
	Status() Status
}

// Snapshot is the whole index in memory. It is replaced, never mutated, so a
// reader holding one sees a consistent state.
type Snapshot struct {
	BuiltAt      time.Time
	UpdatedAt    time.Time
	Experiments  map[string]string // exp id -> fingerprint of what the rows were built from
	RunRows      []RunRow
	AnalysisRows []AnalysisRow
	Rejects      []Rejected
	path         string
	exists       bool
	format       string // Database area (P1): "" is jsonl; set by NewSnapshot for the database index
}

// line is one line of the index file.
type line struct {
	Kind        string       `json:"kind"` // meta | experiment | run | analysis | rejected
	Version     int          `json:"version,omitempty"`
	BuiltAt     *time.Time   `json:"built_at,omitempty"`
	UpdatedAt   *time.Time   `json:"updated_at,omitempty"`
	Experiment  string       `json:"experiment,omitempty"`
	Fingerprint string       `json:"fingerprint,omitempty"`
	Run         *RunRow      `json:"run,omitempty"`
	Analysis    *AnalysisRow `json:"analysis,omitempty"`
	Rejected    *Rejected    `json:"rejected,omitempty"`
}

// Runs implements Reader.
func (s *Snapshot) Runs(f Filter) []RunRow {
	var out []RunRow
	for _, r := range s.RunRows {
		if !f.Match(&r) {
			continue
		}
		out = append(out, r)
		if f.Limit > 0 && len(out) == f.Limit {
			break
		}
	}
	return out
}

// Analyses implements Reader: an experiment's analyses oldest first, or every
// analysis when expID is empty.
func (s *Snapshot) Analyses(expID string) []AnalysisRow {
	var out []AnalysisRow
	for _, a := range s.AnalysisRows {
		if expID == "" || a.Experiment == expID {
			out = append(out, a)
		}
	}
	return out
}

// Rejected implements Reader.
func (s *Snapshot) Rejected() []Rejected { return slices.Clone(s.Rejects) }

// Status implements Reader.
func (s *Snapshot) Status() Status {
	// Database area (P1): cmp.Or gives a database snapshot its own format.
	return Status{Format: cmp.Or(s.format, FormatName), Path: s.path, Exists: s.exists, BuiltAt: s.BuiltAt, UpdatedAt: s.UpdatedAt,
		Experiments: len(s.Experiments), Runs: len(s.RunRows), Analyses: len(s.AnalysisRows), Rejected: len(s.Rejects)}
}

// readSnapshot loads the index file. A missing file, or one of another
// format version, is an empty snapshot: the index is derived data.
func readSnapshot(path string) (*Snapshot, error) {
	s := &Snapshot{Experiments: map[string]string{}, path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read results index: %w", err)
	}
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var l line
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("read results index line %d: %w", n, err)
		}
		if n == 1 && (l.Kind != "meta" || l.Version != FormatVersion) {
			return &Snapshot{Experiments: map[string]string{}, path: path}, nil
		}
		s.apply(&l)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read results index: %w", err)
	}
	s.exists = true
	return s, nil
}

// apply adds one decoded line to the snapshot.
func (s *Snapshot) apply(l *line) {
	switch l.Kind {
	case "meta":
		if l.BuiltAt != nil {
			s.BuiltAt = *l.BuiltAt
		}
		if l.UpdatedAt != nil {
			s.UpdatedAt = *l.UpdatedAt
		}
	case "experiment":
		s.Experiments[l.Experiment] = l.Fingerprint
	case "run":
		if l.Run != nil {
			s.RunRows = append(s.RunRows, *l.Run)
		}
	case "analysis":
		if l.Analysis != nil {
			s.AnalysisRows = append(s.AnalysisRows, *l.Analysis)
		}
	case "rejected":
		if l.Rejected != nil {
			s.Rejects = append(s.Rejects, *l.Rejected)
		}
	}
}

// writeSnapshot writes the snapshot to path atomically (temp file and
// rename), so a reader in another process sees the old file or the new one,
// never half of one. Two writers race only to the last rename; the loser's
// rows are rebuilt on the next update.
func writeSnapshot(path string, s *Snapshot) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	builtAt, updatedAt := s.BuiltAt, s.UpdatedAt
	lines := []line{{Kind: "meta", Version: FormatVersion, BuiltAt: &builtAt, UpdatedAt: &updatedAt}}
	ids := make([]string, 0, len(s.Experiments))
	for id := range s.Experiments {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		lines = append(lines, line{Kind: "experiment", Experiment: id, Fingerprint: s.Experiments[id]})
	}
	for i := range s.RunRows {
		lines = append(lines, line{Kind: "run", Run: &s.RunRows[i]})
	}
	for i := range s.AnalysisRows {
		lines = append(lines, line{Kind: "analysis", Analysis: &s.AnalysisRows[i]})
	}
	for i := range s.Rejects {
		lines = append(lines, line{Kind: "rejected", Rejected: &s.Rejects[i]})
	}
	for i := range lines {
		if err := enc.Encode(&lines[i]); err != nil {
			return fmt.Errorf("encode results index: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("write results index: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), FileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("write results index: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		return errors.Join(fmt.Errorf("write results index: %w", err), tmp.Close(), os.Remove(tmp.Name()))
	}
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		return errors.Join(fmt.Errorf("write results index: %w", err), tmp.Close(), os.Remove(tmp.Name()))
	}
	if err := tmp.Close(); err != nil {
		return errors.Join(fmt.Errorf("write results index: %w", err), os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(fmt.Errorf("write results index: %w", err), os.Remove(tmp.Name()))
	}
	return nil
}

// sortRows puts every row list in its fixed order, so a rebuild and a chain
// of incremental updates over the same folders write the same file.
func (s *Snapshot) sortRows() {
	slices.SortFunc(s.RunRows, func(a, b RunRow) int {
		return cmpStrings(a.Experiment, b.Experiment, a.RunID, b.RunID)
	})
	slices.SortFunc(s.AnalysisRows, func(a, b AnalysisRow) int {
		if c := strings.Compare(a.Experiment, b.Experiment); c != 0 {
			return c
		}
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.AnalysisID, b.AnalysisID)
	})
	slices.SortFunc(s.Rejects, func(a, b Rejected) int {
		return cmpStrings(a.Experiment, b.Experiment, a.RunID, b.RunID)
	})
}

// cmpStrings compares (a1, a2) with (b1, b2) in order.
func cmpStrings(a1, b1, a2, b2 string) int {
	if c := strings.Compare(a1, b1); c != 0 {
		return c
	}
	return strings.Compare(a2, b2)
}
