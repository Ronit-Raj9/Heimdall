// Package tracebulk turns a team's production failures into regression
// tests in bulk (lane Z06). It reads a Maxim log repository (bench/maxim)
// for failed, errored or low-scored traces, records each one in the failure
// inbox (bench/traceinbox: redaction, signatures and dedupe happen there),
// groups the inbox items into failure clusters (same failing tool and error
// line, or the same failing evaluator), and drafts one task per cluster
// with the inbox's accept machinery.
//
// Every draft starts pending review. Heimdall writes a declarative check
// file (verifier/checks.yaml in the format of package taskchecks, Wave A
// lane Y14) and a scenario row (verifier/scenario.json) from what the trace
// shows, all labelled generated. Nothing generated is trusted: a person
// says what should have happened (the right answer or a rubric), which
// replaces the generated judgement, and only then does the readiness check
// (the suite.admit job) run. A draft a person never accepts is never
// admitted, so it never reaches an evaluation, a gate or a report card.
//
// Batches are plain JSON files under inbox/bulk/ in the project.
package tracebulk

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
	"strings"
	"sync"
	"time"
)

// Task review states.
const (
	StatePending   = "pending_review" // drafted; generated checks wait for a person
	StateNeedFiles = "needs_files"    // a person accepted it, but files still hold EDIT ME lines
	StateAccepted  = "accepted"       // a person accepted it; readiness can run
	StateRejected  = "rejected"       // a person rejected it; the task folder was removed
)

// Readiness statuses (TaskDraft.Readiness).
const (
	ReadyNotRun   = "not_run"
	ReadyQueued   = "queued"
	ReadyPassed   = "ready"
	ReadyNotReady = "not_ready"
)

// Tags a drafted task carries.
const (
	TagPendingReview = "pending-review"
	TagMaxim         = "maxim"
)

// Caps.
const (
	DefaultDays       = 7
	MaxDays           = 90
	DefaultLimit      = 200  // traces fetched per batch
	MaxLimit          = 1000 // traces fetched per batch
	MaxScan           = 2000 // search results read per batch when scores are filtered
	MaxPerCluster     = 5    // tasks drafted per cluster
	MaxDraftsPerCall  = 25   // tasks drafted per Draft call
	MaxBatches        = 200  // batch files kept; the oldest go first
	maxBatchFileBytes = 16 << 20
	maxNameBytes      = 128
	maxClusterLinks   = 3
	maxClusterReasons = 5
)

// ErrNotFound marks an unknown batch, cluster or task.
var ErrNotFound = errors.New("not found")

// ErrConflict marks an action the current state does not allow.
var ErrConflict = errors.New("conflict")

// Filter says which traces of the repository are failures.
type Filter struct {
	// Evaluators are online evaluators whose failed traces are taken.
	Evaluators []string `json:"evaluators,omitempty"`
	// Errors takes traces Maxim marks as errored.
	Errors bool `json:"errors,omitempty"`
	// ScoreBelow takes traces with an evaluator score below this value
	// (of ScoreEvaluators, or of any evaluator when that is empty).
	ScoreBelow      *float64 `json:"score_below,omitempty"`
	ScoreEvaluators []string `json:"score_evaluators,omitempty"`
	// Days is how far back the window reaches (default 7, at most 90).
	Days int `json:"days,omitempty"`
	// Limit caps the traces fetched (default 200, at most 1000).
	Limit int `json:"limit,omitempty"`
}

var nameRE = regexp.MustCompile(`^[^\x00-\x1f]{1,128}$`)

// Normalize fills defaults and checks the filter.
func (f *Filter) Normalize() error {
	if f.Days == 0 {
		f.Days = DefaultDays
	}
	if f.Limit == 0 {
		f.Limit = DefaultLimit
	}
	if f.Days < 1 || f.Days > MaxDays {
		return fmt.Errorf("days must be 1 to %d", MaxDays)
	}
	if f.Limit < 1 || f.Limit > MaxLimit {
		return fmt.Errorf("limit must be 1 to %d traces", MaxLimit)
	}
	clean := func(list []string, what string) ([]string, error) {
		var out []string
		seen := map[string]bool{}
		for _, n := range list {
			n = strings.TrimSpace(n)
			if n == "" || seen[n] {
				continue
			}
			if !nameRE.MatchString(n) {
				return nil, fmt.Errorf("%s %q: 1 to %d printable characters", what, n, maxNameBytes)
			}
			seen[n] = true
			out = append(out, n)
		}
		if len(out) > 20 {
			return nil, fmt.Errorf("at most 20 %ss", what)
		}
		return out, nil
	}
	var err error
	if f.Evaluators, err = clean(f.Evaluators, "evaluator"); err != nil {
		return err
	}
	if f.ScoreEvaluators, err = clean(f.ScoreEvaluators, "score evaluator"); err != nil {
		return err
	}
	if len(f.ScoreEvaluators) > 0 && f.ScoreBelow == nil {
		return errors.New("score evaluators need score_below")
	}
	if !f.Errors && len(f.Evaluators) == 0 && f.ScoreBelow == nil {
		return errors.New("say which traces failed: evaluators, errors or score_below")
	}
	return nil
}

// Counts are what one pull saw.
type Counts struct {
	Scanned         int `json:"scanned"`          // search results read
	Failed          int `json:"failed"`           // distinct failing traces found
	EvaluatorFailed int `json:"evaluator_failed"` // of them, failed by a named evaluator
	Errored         int `json:"errored"`          // of them, errored
	LowScore        int `json:"low_score"`        // of them, scored below the threshold
	Fetched         int `json:"fetched"`          // traces read in full
	NewItems        int `json:"new_items"`        // new inbox items
	Repeats         int `json:"repeats"`          // occurrences of items the inbox already had
}

// Cluster is one failure mode: inbox items that failed the same way.
type Cluster struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Tool        string   `json:"tool,omitempty"`
	Error       string   `json:"error,omitempty"`
	Evaluator   string   `json:"evaluator,omitempty"`
	Items       []string `json:"items"` // inbox item ids, most occurrences first
	Occurrences int      `json:"occurrences"`
	Sample      string   `json:"sample"` // the top item's first request (redacted)
	Reasons     []string `json:"reasons,omitempty"`
	Links       []string `json:"links,omitempty"`
	Kind        string   `json:"kind"` // suggested draft kind
}

// Readiness is a drafted task's readiness check.
type Readiness struct {
	Status string     `json:"status"`
	JobID  string     `json:"job_id,omitempty"`
	At     *time.Time `json:"at,omitempty"`
}

// TaskDraft is one task drafted from a cluster.
type TaskDraft struct {
	Cluster    string     `json:"cluster"`
	Item       string     `json:"item"`
	Suite      string     `json:"suite"`
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	State      string     `json:"state"`
	Generated  []string   `json:"generated"` // ids of the generated checks in checks.yaml
	EditMe     []string   `json:"edit_me,omitempty"`
	Request    string     `json:"request"`          // the first request (redacted)
	Recorded   string     `json:"recorded"`         // the recorded bad answer (redacted)
	Suggested  string     `json:"suggested_rubric"` // the generated rubric a person edits
	Readiness  Readiness  `json:"readiness"`
	Drafted    time.Time  `json:"drafted"`
	Reviewed   *time.Time `json:"reviewed,omitempty"`
	ReviewedBy string     `json:"reviewed_by,omitempty"`
	Note       string     `json:"note,omitempty"`
}

// Key is the task's suite/id.
func (t *TaskDraft) Key() string { return t.Suite + "/" + t.ID }

// Batch is one bulk import.
type Batch struct {
	ID        string      `json:"id"`
	Source    string      `json:"source"` // maxim
	Workspace string      `json:"workspace"`
	Repo      string      `json:"repo"`
	Filter    Filter      `json:"filter"`
	Counts    Counts      `json:"counts"`
	Warnings  []string    `json:"warnings,omitempty"`
	Truncated bool        `json:"truncated,omitempty"`
	Clusters  []Cluster   `json:"clusters"`
	Tasks     []TaskDraft `json:"tasks"`
	Created   time.Time   `json:"created"`
	CreatedBy string      `json:"created_by,omitempty"`
	Updated   time.Time   `json:"updated"`
}

// Summary is a batch without its clusters and tasks, for lists.
type Summary struct {
	ID       string    `json:"id"`
	Source   string    `json:"source"`
	Repo     string    `json:"repo"`
	Counts   Counts    `json:"counts"`
	Clusters int       `json:"clusters"`
	Tasks    int       `json:"tasks"`
	Pending  int       `json:"pending"`
	Created  time.Time `json:"created"`
}

// Cluster finds a cluster by id.
func (b *Batch) Cluster(id string) (*Cluster, bool) {
	for i := range b.Clusters {
		if b.Clusters[i].ID == id {
			return &b.Clusters[i], true
		}
	}
	return nil, false
}

// Task finds a drafted task by suite/id.
func (b *Batch) Task(key string) (*TaskDraft, bool) {
	for i := range b.Tasks {
		if b.Tasks[i].Key() == key {
			return &b.Tasks[i], true
		}
	}
	return nil, false
}

// Store keeps batches as files in one folder.
type Store struct {
	dir string
	mu  *sync.Mutex // shared by every Store over the same folder
}

var storeLocks sync.Map // folder -> *sync.Mutex

// Dir is the batch folder under the inbox folder.
const Dir = "bulk"

// Open returns the store under the inbox folder.
func Open(inboxDir string) *Store {
	dir := filepath.Clean(filepath.Join(inboxDir, Dir))
	mu, _ := storeLocks.LoadOrStore(dir, &sync.Mutex{})
	return &Store{dir: dir, mu: mu.(*sync.Mutex)}
}

var batchIDRE = regexp.MustCompile(`^b_[0-9]{8}_[0-9a-f]{8}$`)

// ValidID reports whether id names a batch.
func ValidID(id string) bool { return batchIDRE.MatchString(id) }

// NewID returns a fresh batch id for now.
func NewID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "b_" + now.UTC().Format("20060102") + "_" + hex.EncodeToString(b[:])
}

// Get reads one batch.
func (s *Store) Get(id string) (*Batch, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("batch %q: %w", id, ErrNotFound)
	}
	return s.read(id)
}

func (s *Store) read(id string) (*Batch, error) {
	f, err := os.Open(filepath.Join(s.dir, id+".json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("batch %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var b Batch
	dec := json.NewDecoder(&capReader{r: f, n: maxBatchFileBytes})
	if err := dec.Decode(&b); err != nil {
		return nil, fmt.Errorf("batch %s: %w", id, err)
	}
	return &b, nil
}

// Save writes a batch atomically and drops the oldest files over MaxBatches.
func (s *Store) Save(b *Batch) error {
	if !ValidID(b.ID) {
		return fmt.Errorf("batch id %q is not valid", b.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.save(b)
}

func (s *Store) save(b *Batch) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".batch-*")
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
	if err := os.Rename(tmp.Name(), filepath.Join(s.dir, b.ID+".json")); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return s.prune()
}

// Update reads a batch, applies fn and saves it, under the store lock.
func (s *Store) Update(id string, fn func(*Batch) error) (*Batch, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("batch %q: %w", id, ErrNotFound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.read(id)
	if err != nil {
		return nil, err
	}
	if err := fn(b); err != nil {
		return nil, err
	}
	if err := s.save(b); err != nil {
		return nil, err
	}
	return b, nil
}

// List returns every batch, newest first, as summaries.
func (s *Store) List() ([]Summary, error) {
	ids, err := s.ids()
	if err != nil {
		return nil, err
	}
	out := []Summary{}
	for _, id := range ids {
		b, err := s.read(id)
		if err != nil {
			continue
		}
		sum := Summary{ID: b.ID, Source: b.Source, Repo: b.Repo, Counts: b.Counts, Clusters: len(b.Clusters), Tasks: len(b.Tasks), Created: b.Created}
		for _, t := range b.Tasks {
			if t.State == StatePending || t.State == StateNeedFiles {
				sum.Pending++
			}
		}
		out = append(out, sum)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

func (s *Store) ids() ([]string, error) {
	ents, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range ents {
		if id, ok := strings.CutSuffix(e.Name(), ".json"); ok && ValidID(id) && e.Type().IsRegular() {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids) // ids start with the date, so oldest first within a day's random order
	return ids, nil
}

func (s *Store) prune() error {
	ids, err := s.ids()
	if err != nil || len(ids) <= MaxBatches {
		return err
	}
	type aged struct {
		id string
		at time.Time
	}
	var list []aged
	for _, id := range ids {
		st, err := os.Stat(filepath.Join(s.dir, id+".json"))
		if err != nil {
			continue
		}
		list = append(list, aged{id, st.ModTime()})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].at.Before(list[j].at) })
	for _, a := range list[:max(0, len(list)-MaxBatches)] {
		_ = os.Remove(filepath.Join(s.dir, a.id+".json"))
	}
	return nil
}

type capReader struct {
	r interface{ Read([]byte) (int, error) }
	n int64
}

func (c *capReader) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, fmt.Errorf("batch file is over %d bytes", maxBatchFileBytes)
	}
	if int64(len(p)) > c.n {
		p = p[:c.n]
	}
	n, err := c.r.Read(p)
	c.n -= int64(n)
	return n, err
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut] + "..."
}
