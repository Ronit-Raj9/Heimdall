package onboard

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/schemas"
)

// DraftsArea is the project folder that holds draft sets, beside
// benchmarks/: the planner never reads it.
const DraftsArea = "drafts"

// setFile is a draft set's record inside its folder.
const setFile = "set.json"

// DraftSet is one staged batch of tasks: drafts/<id>/set.json plus a full
// suite folder at drafts/<id>/benchmarks/<suite>/ that readiness runs on.
type DraftSet struct {
	ID        string    `json:"id"`
	Suite     string    `json:"suite"`     // target benchmark id
	NewSuite  bool      `json:"new_suite"` // the benchmark does not exist yet; publish creates it
	Title     string    `json:"title"`
	Kind      Kind      `json:"kind"`
	Source    string    `json:"source"`
	Input     Input     `json:"input"` // data and connection cleared
	Plan      Plan      `json:"plan"`
	Grader    string    `json:"grader"` // the draft suite's grader
	Public    bool      `json:"public"` // the data is public (a public dataset): likely seen in training
	License   string    `json:"license,omitempty"`
	Created   time.Time `json:"created"`
	Updated   time.Time `json:"updated"`
	CreatedBy string    `json:"created_by,omitempty"`

	Tasks    []DraftTask `json:"tasks"`
	Problems []Problem   `json:"problems"` // units that could not become a task
	Notes    []string    `json:"notes,omitempty"`

	Readiness *Readiness `json:"readiness,omitempty"`
	Published *Published `json:"published,omitempty"`

	dir string
}

// Problem is a unit that could not become a task, and why.
type Problem struct {
	Ref     string `json:"ref"`
	Message string `json:"message"`
}

// DraftTask is one staged task and the proof gathered on it.
type DraftTask struct {
	ID          string           `json:"id"`
	Origin      Origin           `json:"origin"`
	Title       string           `json:"title"`
	Instruction string           `json:"instruction"` // exactly as the agent sees it (cut to 4000 characters)
	Checks      []string         `json:"checks"`      // plain sentences rendered from the task's files, never by a model
	Controls    []ControlInfo    `json:"controls"`
	Generated   []GeneratedField `json:"generated,omitempty"`
	Gates       []GateResult     `json:"gates"`
	HeldOut     bool             `json:"held_out"`
	Review      string           `json:"review"`
	// Readiness is the admission record of the last readiness run on this
	// task; CatchRate counts the negative controls its check failed.
	Readiness *schemas.AdmissionRecord `json:"readiness,omitempty"`
	CatchRate *CatchRate               `json:"catch_rate,omitempty"`
	Reason    string                   `json:"reason,omitempty"` // why readiness refused it
}

// CatchRate is how many of a task's negative controls (empty answer, wrong
// answers, exploit, noop) its check failed, as it must.
type CatchRate struct {
	Caught int `json:"caught"`
	Total  int `json:"total"`
}

// Ready reports whether the last readiness run admitted the task.
func (t *DraftTask) Ready() bool { return t.Readiness != nil && t.Readiness.Admitted }

// Readiness is the state of the readiness run on a set.
type Readiness struct {
	JobID    string    `json:"job_id,omitempty"`
	Status   string    `json:"status"` // queued, running, done, failed
	Where    string    `json:"where"`  // "this server" or "a worker"
	Done     int       `json:"done"`
	Total    int       `json:"total"`
	Admitted int       `json:"admitted"`
	Refused  int       `json:"refused"`
	Error    string    `json:"error,omitempty"`
	Problems []Problem `json:"problems,omitempty"` // static validation findings
	At       time.Time `json:"at"`
	ExpID    string    `json:"exp_id,omitempty"` // hosted: the experiment that ran the controls
}

// Readiness statuses.
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
)

// Published is what publish did.
type Published struct {
	Suite   string    `json:"suite"`
	Tasks   []string  `json:"tasks"`
	Created bool      `json:"created"` // the benchmark was created by this publish
	HeldOut int       `json:"held_out"`
	Canary  string    `json:"canary,omitempty"`
	Carried []string  `json:"carried,omitempty"` // readiness records carried into the benchmark's lock
	Recheck []string  `json:"recheck,omitempty"` // published tasks whose readiness must run again on the benchmark
	At      time.Time `json:"at"`
	By      string    `json:"by,omitempty"`
	JobID   string    `json:"job_id,omitempty"`
}

var setIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{2,80}$`)

// ValidSetID reports whether id can name a draft set.
func ValidSetID(id string) bool { return setIDRE.MatchString(id) }

// DraftsDir is the drafts folder of a project.
func DraftsDir(root string) string { return filepath.Join(root, DraftsArea) }

// newSetID is a sortable, unguessable set id.
func newSetID() string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return now().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// NewSet starts an empty draft set for suite.
func NewSet(root, suite string) (*DraftSet, error) {
	if !ValidSuiteID(suite) {
		return nil, fmt.Errorf("benchmark id %q: use 1 to 64 lowercase letters, digits, '.', '_' or '-'", suite)
	}
	id := newSetID()
	s := &DraftSet{ID: id, Suite: suite, Created: now(), Updated: now(), Tasks: []DraftTask{}, Problems: []Problem{},
		dir: filepath.Join(DraftsDir(root), id)}
	if err := os.MkdirAll(s.SuiteDir(), 0o755); err != nil {
		return nil, err
	}
	return s, nil
}

// Dir is the set's folder.
func (s *DraftSet) Dir() string { return s.dir }

// BenchRoot is the benchmarks root readiness loads the draft suite from.
func (s *DraftSet) BenchRoot() string { return filepath.Join(s.dir, "benchmarks") }

// SuiteDir is the draft suite folder.
func (s *DraftSet) SuiteDir() string { return filepath.Join(s.BenchRoot(), s.Suite) }

// Task returns the draft task id.
func (s *DraftSet) Task(id string) *DraftTask {
	for i := range s.Tasks {
		if s.Tasks[i].ID == id {
			return &s.Tasks[i]
		}
	}
	return nil
}

var setLocks sync.Map // dir -> *sync.Mutex

// Lock serialises changes to one set inside this process.
func (s *DraftSet) Lock() func() {
	m, _ := setLocks.LoadOrStore(s.dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// Save writes set.json atomically.
func (s *DraftSet) Save() error {
	s.Updated = now()
	if s.Tasks == nil {
		s.Tasks = []DraftTask{}
	}
	if s.Problems == nil {
		s.Problems = []Problem{}
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".set-*.json")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), filepath.Join(s.dir, setFile))
}

// ErrSetNotFound is a missing draft set.
var ErrSetNotFound = errors.New("draft set not found")

// LoadSet reads drafts/<id>/set.json.
func LoadSet(root, id string) (*DraftSet, error) {
	if !ValidSetID(id) {
		return nil, ErrSetNotFound
	}
	dir := filepath.Join(DraftsDir(root), id)
	data, err := os.ReadFile(filepath.Join(dir, setFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrSetNotFound
		}
		return nil, err
	}
	var s DraftSet
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("draft set %s: %w", id, err)
	}
	if s.ID != id || !ValidSuiteID(s.Suite) {
		return nil, fmt.Errorf("draft set %s: set.json names another set", id)
	}
	s.dir = dir
	return &s, nil
}

// SetSummary is one row of the draft list.
type SetSummary struct {
	ID        string    `json:"id"`
	Suite     string    `json:"suite"`
	Title     string    `json:"title"`
	Kind      Kind      `json:"kind"`
	Tasks     int       `json:"tasks"`
	Ready     int       `json:"ready"`
	Readiness string    `json:"readiness,omitempty"`
	Published bool      `json:"published"`
	Updated   time.Time `json:"updated"`
	Input     string    `json:"input"`
}

// ListSets lists the project's draft sets, newest first.
func ListSets(root string) ([]SetSummary, error) {
	ents, err := os.ReadDir(DraftsDir(root))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []SetSummary{}, nil
		}
		return nil, err
	}
	out := []SetSummary{}
	for _, e := range ents {
		if !e.IsDir() || !ValidSetID(e.Name()) {
			continue
		}
		s, err := LoadSet(root, e.Name())
		if err != nil {
			continue
		}
		row := SetSummary{ID: s.ID, Suite: s.Suite, Title: s.Title, Kind: s.Kind, Tasks: len(s.Tasks), Published: s.Published != nil,
			Updated: s.Updated, Input: s.Input.Label()}
		for i := range s.Tasks {
			if s.Tasks[i].Ready() {
				row.Ready++
			}
		}
		if s.Readiness != nil {
			row.Readiness = s.Readiness.Status
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })
	return out, nil
}

// DeleteSet removes a draft set.
func DeleteSet(root, id string) error {
	if !ValidSetID(id) {
		return ErrSetNotFound
	}
	dir := filepath.Join(DraftsDir(root), id)
	if _, err := os.Stat(filepath.Join(dir, setFile)); err != nil {
		return ErrSetNotFound
	}
	return os.RemoveAll(dir)
}
