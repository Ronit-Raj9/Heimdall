// Package issuesync keeps the repositories an organization connected for a
// weekly issue sync and runs one sync: scan newly merged fixes, skip the ones
// already seen or already a task, and build the rest under a weekly spending
// cap. Records live in issuesync/repos.json under the org project root; no
// record ever holds a token.
package issuesync

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench/pipeline"
)

// File is the store, under the org project root.
const File = "issuesync/repos.json"

// Repo is one connected repository.
type Repo struct {
	ID           string               `json:"id"`        // "sync-" + 8 hex
	Repo         string               `json:"repo"`      // owner/name
	Benchmark    string               `json:"benchmark"` // suite id the tasks join
	Weekday      int                  `json:"weekday"`   // 0 Sunday .. 6
	HourUTC      int                  `json:"hour_utc"`
	MaxIssues    int                  `json:"max_issues"`    // per week
	PerIssueUSD  float64              `json:"per_issue_usd"` // builder cap per issue
	WeeklyUSD    float64              `json:"weekly_usd"`    // hard cap per run
	Model        pipeline.ModelChoice `json:"model"`
	Private      bool                 `json:"private"` // set by the first run
	Paused       bool                 `json:"paused"`
	PausedReason string               `json:"paused_reason,omitempty"`
	MergedAfter  time.Time            `json:"merged_after"` // newest merge time already scanned
	SeenPRs      []int                `json:"seen_prs"`     // last 2000 PR numbers ever started
	Runs         []RunRef             `json:"runs"`         // last 20
	CreatedBy    string               `json:"created_by"`
	CreatedAt    time.Time            `json:"created_at"`
}

// RunRef is one sync run of a repository.
type RunRef struct {
	At       time.Time `json:"at"`
	RunID    string    `json:"run_id,omitempty"` // pipeline State.ID
	Started  int       `json:"started"`
	Skipped  int       `json:"skipped"`
	SpentUSD float64   `json:"spent_usd"`
	Status   string    `json:"status"` // RunStarted, RunNothingNew, RunRefused or RunFailed
	Reason   string    `json:"reason,omitempty"`
}

// Run statuses.
const (
	RunStarted    = "started"
	RunNothingNew = "nothing_new"
	RunRefused    = "refused"
	RunFailed     = "failed"
)

// Limits and defaults of a Repo.
const (
	DefaultWeekday     = 1
	DefaultHourUTC     = 3
	DefaultMaxIssues   = 10
	MaxMaxIssues       = 30
	DefaultPerIssueUSD = 1.0
	MaxPerIssueUSD     = 5.0
	DefaultWeeklyUSD   = 10.0
	MaxWeeklyUSD       = 50.0
	maxSeen            = 2000
	maxRuns            = 20
)

// ErrNotFound is a sync id the store does not hold.
var ErrNotFound = errors.New("no weekly sync with this id")

// Store keeps the connected repositories.
type Store interface {
	List() ([]Repo, error)
	Get(id string) (*Repo, error)
	Update(fn func([]Repo) ([]Repo, error)) error
}

// Open returns the file store under projectRoot.
func Open(projectRoot string) Store {
	path := filepath.Join(projectRoot, File)
	v, _ := stores.LoadOrStore(path, &fileStore{path: path})
	return v.(*fileStore)
}

var stores sync.Map // path -> *fileStore, so one process shares one mutex per file

type fileStore struct {
	mu   sync.Mutex
	path string
}

func (st *fileStore) List() ([]Repo, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.read()
}

func (st *fileStore) Get(id string) (*Repo, error) {
	list, err := st.List()
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrNotFound
}

func (st *fileStore) Update(fn func([]Repo) ([]Repo, error)) error {
	unlock, err := st.lock()
	if err != nil {
		return err
	}
	defer unlock()
	list, err := st.read()
	if err != nil {
		return err
	}
	out, err := fn(list)
	if err != nil {
		return err
	}
	return st.write(out)
}

func (st *fileStore) read() ([]Repo, error) {
	b, err := os.ReadFile(st.path)
	if errors.Is(err, os.ErrNotExist) {
		return []Repo{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read weekly syncs: %w", err)
	}
	var list []Repo
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("parse weekly syncs: %w", err)
	}
	if list == nil {
		list = []Repo{}
	}
	return list, nil
}

func (st *fileStore) lock() (func(), error) {
	st.mu.Lock()
	if err := os.MkdirAll(filepath.Dir(st.path), 0o755); err != nil {
		st.mu.Unlock()
		return nil, err
	}
	f, err := os.OpenFile(st.path+".lock", os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		st.mu.Unlock()
		return nil, fmt.Errorf("open weekly sync lock: %w", err)
	}
	release, err := flock(f)
	if err != nil {
		_ = f.Close()
		st.mu.Unlock()
		return nil, err
	}
	return func() { _ = release(); st.mu.Unlock() }, nil
}

// write rewrites the file atomically: a temp file in the same folder, then
// a rename.
func (st *fileStore) write(list []Repo) error {
	if list == nil {
		list = []Repo{}
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(st.path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(b)
	if werr == nil {
		werr = tmp.Sync()
	}
	if cerr := tmp.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp.Name())
		return werr
	}
	if err := os.Rename(tmp.Name(), st.path); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return nil
}

// NewID returns "sync-" and 8 hex characters.
func NewID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "sync-" + hex.EncodeToString(b[:])
}

// Defaults fills zero fields with the defaults.
func (r *Repo) Defaults() {
	if r.MaxIssues == 0 {
		r.MaxIssues = DefaultMaxIssues
	}
	if r.PerIssueUSD == 0 {
		r.PerIssueUSD = DefaultPerIssueUSD
	}
	if r.WeeklyUSD == 0 {
		r.WeeklyUSD = DefaultWeeklyUSD
	}
}

// FieldError is a field a person must change, with its sentence.
type FieldError struct {
	Field string
	Text  string
}

func (e *FieldError) Error() string { return e.Text }

// Validate checks the fields a person sets.
func (r *Repo) Validate() error {
	switch {
	case !pipeline.ValidRepo(r.Repo):
		return &FieldError{"repo", "Enter the repository as owner/name."}
	case r.Weekday < 0 || r.Weekday > 6:
		return &FieldError{"weekday", "Pick a day of the week."}
	case r.HourUTC < 0 || r.HourUTC > 23:
		return &FieldError{"hour_utc", "Pick an hour from 0 to 23."}
	case r.MaxIssues < 1 || r.MaxIssues > MaxMaxIssues:
		return &FieldError{"max_issues", fmt.Sprintf("Most new tasks a week must be from 1 to %d.", MaxMaxIssues)}
	case !(r.PerIssueUSD > 0 && r.PerIssueUSD <= MaxPerIssueUSD):
		return &FieldError{"per_issue_usd", fmt.Sprintf("The cap per issue must be above $0 and at most $%.0f.", MaxPerIssueUSD)}
	case !(r.WeeklyUSD > 0 && r.WeeklyUSD <= MaxWeeklyUSD):
		return &FieldError{"weekly_usd", fmt.Sprintf("The spending cap per week must be above $0 and at most $%.0f.", MaxWeeklyUSD)}
	}
	if err := r.Model.Valid(); err != nil {
		return &FieldError{"model", "Pick both a provider and a model, or neither."}
	}
	return nil
}
