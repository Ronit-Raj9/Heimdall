// Package annotate keeps an organization's annotation queues (lane Y15):
// runs or judge decisions that signed-in people label correct or
// incorrect. Each item wants K labels from K different people; in blind
// mode the agent and its version are hidden from them, and nobody sees a
// peer's label or the judge's verdict before giving their own. K agreeing
// labels resolve an item; a disagreement waits for the queue owner or an
// admin to decide. A resolved item of a queue tied to a benchmark becomes
// a judge calibration label (calibration/labels.jsonl).
//
// A queue is one JSON file under the org's project folder,
// .heimdall/annotation/<queue id>.json, written atomically under a lock.
package annotate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/datapolicy"
)

// Labels.
const (
	LabelCorrect   = "correct"
	LabelIncorrect = "incorrect"
)

// Labels lists the labels an item can get.
func Labels() []string { return []string{LabelCorrect, LabelIncorrect} }

// Queue kinds.
const (
	KindRuns  = "runs"  // evaluation runs: is the agent's answer correct?
	KindJudge = "judge" // judge decisions: was the judge right? (the judge's verdict is shown after labelling)
)

// Assignment modes.
const (
	AssignOpen  = "open"  // the next free item goes to whoever asks
	AssignFixed = "fixed" // only the people an item is assigned to label it
)

// Item states.
const (
	StateOpen      = "open"      // no label yet
	StateLabelling = "labelling" // some labels, fewer than K
	StateDisputed  = "disputed"  // K labels that disagree, waiting for a decision
	StateDone      = "done"      // resolved: K labels agree, or someone decided
)

// Limits.
const (
	MaxQueues          = 200
	MaxItems           = 5000
	MaxReviewers       = 200
	MaxReviewersPerItm = 5
	maxText            = 64 << 10
	maxNote            = 2000
	// ClaimTTL is how long a claimed item stays reserved for one person.
	ClaimTTL = 30 * time.Minute
)

// Errors.
var (
	ErrNotFound  = errors.New("not found")
	ErrInvalid   = errors.New("invalid request")
	ErrConflict  = errors.New("conflict")
	ErrForbidden = errors.New("forbidden")
)

// Queue is one annotation queue's settings.
type Queue struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	// Benchmark is the benchmark whose judge the labels calibrate; empty
	// keeps the labels in the queue only.
	Benchmark        string    `json:"benchmark,omitempty"`
	Kind             string    `json:"kind"`
	ReviewersPerItem int       `json:"reviewers_per_item"`
	Blind            bool      `json:"blind"`
	Assignment       string    `json:"assignment"`
	Reviewers        []string  `json:"reviewers,omitempty"` // user ids; empty: any member
	CreatedBy        string    `json:"created_by"`
	CreatedByEmail   string    `json:"created_by_email,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	Archived         bool      `json:"archived,omitempty"`
}

// Item is one thing to label.
type Item struct {
	ID           string   `json:"id"`
	Task         string   `json:"task"`
	Question     string   `json:"question,omitempty"`
	Answer       string   `json:"answer"`
	RunID        string   `json:"run_id,omitempty"`
	Experiment   string   `json:"experiment,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	AgentVersion string   `json:"agent_version,omitempty"`
	JudgeLabel   string   `json:"judge_label,omitempty"` // correct | incorrect | ""
	JudgeVotes   []string `json:"judge_votes,omitempty"`
	JudgeReason  string   `json:"judge_reason,omitempty"`
	Ref          string   `json:"ref,omitempty"`

	AddedAt  time.Time    `json:"added_at"`
	AddedBy  string       `json:"added_by,omitempty"`
	Assigned []Assignment `json:"assigned,omitempty"`
	Labels   []Label      `json:"labels,omitempty"`

	Resolution *Resolution `json:"resolution,omitempty"`
	// Calibration is the calibration label written for the resolved item,
	// and CalibrationError why none could be.
	Calibration      string `json:"calibration,omitempty"`
	CalibrationError string `json:"calibration_error,omitempty"`
}

// Assignment reserves an item for one person. Claimed assignments (from
// Next in open mode) expire after ClaimTTL; fixed ones do not.
type Assignment struct {
	UserID string    `json:"user_id"`
	At     time.Time `json:"at"`
	Fixed  bool      `json:"fixed,omitempty"`
}

// Label is one person's label.
type Label struct {
	UserID string    `json:"user_id"`
	Email  string    `json:"email,omitempty"`
	Label  string    `json:"label"`
	Note   string    `json:"note,omitempty"`
	At     time.Time `json:"at"`
}

// Resolution is an item's final label.
type Resolution struct {
	Label  string    `json:"label"`
	Method string    `json:"method"` // consensus | decided
	UserID string    `json:"user_id,omitempty"`
	Email  string    `json:"email,omitempty"`
	Note   string    `json:"note,omitempty"`
	At     time.Time `json:"at"`
}

// Resolved is the item's final label, "" while unresolved.
func (it *Item) Resolved() string {
	if it.Resolution == nil {
		return ""
	}
	return it.Resolution.Label
}

// State is the item's state for a queue wanting k labels.
func (it *Item) State(k int) string {
	switch {
	case it.Resolution != nil:
		return StateDone
	case len(it.Labels) == 0:
		return StateOpen
	case len(it.Labels) >= k:
		return StateDisputed
	}
	return StateLabelling
}

func (it *Item) labelledBy(user string) bool {
	return slices.ContainsFunc(it.Labels, func(l Label) bool { return l.UserID == user })
}

func (it *Item) assignedTo(user string, now time.Time) bool {
	return slices.ContainsFunc(it.Assigned, func(a Assignment) bool { return a.UserID == user && (a.Fixed || now.Sub(a.At) < ClaimTTL) })
}

// openSlots is how many more people may take the item now: K minus the
// labels and the live reservations of people who have not labelled.
func (it *Item) openSlots(k int, now time.Time) int {
	n := k - len(it.Labels)
	for _, a := range it.Assigned {
		if !it.labelledBy(a.UserID) && (a.Fixed || now.Sub(a.At) < ClaimTTL) {
			n--
		}
	}
	return n
}

// ItemID is the content address of an item: the same run, task and answer
// added twice is one item.
func ItemID(task, answer, runID string) string {
	h := sha256.Sum256([]byte(task + "\x00" + answer + "\x00" + runID))
	return hex.EncodeToString(h[:])[:16]
}

var idRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// ValidID reports whether s is a valid queue or item id.
func ValidID(s string) bool { return idRE.MatchString(s) }

func oneLine(s string, n int) bool {
	return utf8.ValidString(s) && utf8.RuneCountInString(s) <= n && !strings.ContainsAny(s, "\r\n")
}

// Normalize fills a queue's defaults and checks it.
func (q *Queue) Normalize() error {
	q.Name = strings.TrimSpace(q.Name)
	q.Benchmark = strings.TrimSpace(q.Benchmark)
	switch {
	case q.Name == "" || !oneLine(q.Name, 120):
		return fmt.Errorf("%w: name is required, one line of at most 120 characters", ErrInvalid)
	case !utf8.ValidString(q.Description) || utf8.RuneCountInString(q.Description) > 2000:
		return fmt.Errorf("%w: description is at most 2000 characters", ErrInvalid)
	case !utf8.ValidString(q.Instructions) || utf8.RuneCountInString(q.Instructions) > 8000:
		return fmt.Errorf("%w: instructions are at most 8000 characters", ErrInvalid)
	case q.Benchmark != "" && !ValidID(q.Benchmark):
		return fmt.Errorf("%w: benchmark %q is not a benchmark id", ErrInvalid, q.Benchmark)
	}
	if q.Kind == "" {
		q.Kind = KindRuns
	}
	if q.Kind != KindRuns && q.Kind != KindJudge {
		return fmt.Errorf("%w: kind: want runs or judge", ErrInvalid)
	}
	if q.ReviewersPerItem == 0 {
		q.ReviewersPerItem = 1
	}
	if q.ReviewersPerItem < 1 || q.ReviewersPerItem > MaxReviewersPerItm {
		return fmt.Errorf("%w: reviewers_per_item: 1 to %d", ErrInvalid, MaxReviewersPerItm)
	}
	if q.Assignment == "" {
		q.Assignment = AssignOpen
	}
	if q.Assignment != AssignOpen && q.Assignment != AssignFixed {
		return fmt.Errorf("%w: assignment: want open or fixed", ErrInvalid)
	}
	seen := map[string]bool{}
	var rs []string
	for _, r := range q.Reviewers {
		r = strings.TrimSpace(r)
		if r == "" || seen[r] {
			continue
		}
		if !oneLine(r, 128) || strings.ContainsAny(r, " \t") {
			return fmt.Errorf("%w: reviewer %q is not a user id", ErrInvalid, r)
		}
		seen[r] = true
		rs = append(rs, r)
	}
	if len(rs) > MaxReviewers {
		return fmt.Errorf("%w: at most %d reviewers", ErrInvalid, MaxReviewers)
	}
	sort.Strings(rs)
	q.Reviewers = rs
	if q.Assignment == AssignFixed && len(rs) < q.ReviewersPerItem {
		return fmt.Errorf("%w: fixed assignment needs at least %d reviewers named (one per label an item wants)", ErrInvalid, q.ReviewersPerItem)
	}
	return nil
}

// MayLabel reports whether user may label in q.
func (q *Queue) MayLabel(user string) bool {
	return len(q.Reviewers) == 0 || slices.Contains(q.Reviewers, user)
}

// File is one queue on disk.
type File struct {
	Queue Queue  `json:"queue"`
	Items []Item `json:"items"`
}

// Counts are a queue's items per state.
type Counts struct {
	Total     int `json:"total"`
	Open      int `json:"open"`
	Labelling int `json:"labelling"`
	Disputed  int `json:"disputed"`
	Done      int `json:"done"`
	// Calibrated is how many resolved items became calibration labels.
	Calibrated int `json:"calibrated"`
}

// CountsOf counts items.
func CountsOf(items []Item, k int) Counts {
	c := Counts{Total: len(items)}
	for i := range items {
		switch items[i].State(k) {
		case StateOpen:
			c.Open++
		case StateLabelling:
			c.Labelling++
		case StateDisputed:
			c.Disputed++
		case StateDone:
			c.Done++
		}
		if items[i].Calibration != "" {
			c.Calibrated++
		}
	}
	return c
}

// Store is the queues of one project root.
type Store struct {
	dir string
	now func() time.Time
}

// Open returns the store of a project root.
func Open(root string) *Store {
	return &Store{dir: filepath.Join(root, ".heimdall", "annotation"), now: func() time.Time { return time.Now().UTC() }}
}

// WithClock returns s reading the time from now (tests).
func (s *Store) WithClock(now func() time.Time) *Store {
	c := *s
	c.now = now
	return &c
}

var locks sync.Map

func (s *Store) lock() func() {
	m, _ := locks.LoadOrStore(s.dir, &sync.Mutex{})
	mu := m.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (s *Store) path(id string) string { return filepath.Join(s.dir, id+".json") }

func (s *Store) read(id string) (*File, error) {
	if !ValidID(id) {
		return nil, fmt.Errorf("queue %q: %w", id, ErrNotFound)
	}
	b, err := os.ReadFile(s.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("queue %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("read queue %s: %w", id, err)
	}
	return &f, nil
}

func (s *Store) write(f *File) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return datapolicy.WriteAtomic(s.dir, s.path(f.Queue.ID), b, 0o600)
}

// List reads every queue, newest first.
func (s *Store) List() ([]File, error) {
	unlock := s.lock()
	defer unlock()
	ents, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []File
	for _, e := range ents {
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !ValidID(id) {
			continue
		}
		f, err := s.read(id)
		if err != nil {
			continue
		}
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Queue.CreatedAt.After(out[j].Queue.CreatedAt) })
	return out, nil
}

// Get reads one queue.
func (s *Store) Get(id string) (*File, error) {
	unlock := s.lock()
	defer unlock()
	return s.read(id)
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return "q-" + hex.EncodeToString(b)
}

// Create stores a new queue.
func (s *Store) Create(q Queue) (*Queue, error) {
	if err := q.Normalize(); err != nil {
		return nil, err
	}
	unlock := s.lock()
	defer unlock()
	if ents, _ := os.ReadDir(s.dir); len(ents) >= MaxQueues {
		return nil, fmt.Errorf("%w: an organization keeps at most %d queues; archive or delete one", ErrConflict, MaxQueues)
	}
	q.ID = newID()
	q.CreatedAt = s.now().Truncate(time.Second)
	q.UpdatedAt = q.CreatedAt
	if err := s.write(&File{Queue: q, Items: []Item{}}); err != nil {
		return nil, err
	}
	return &q, nil
}

// Update changes a queue under the lock: fn edits the file in place.
func (s *Store) Update(id string, fn func(f *File, now time.Time) error) (*File, error) {
	unlock := s.lock()
	defer unlock()
	f, err := s.read(id)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if err := fn(f, now); err != nil {
		return nil, err
	}
	f.Queue.UpdatedAt = now.Truncate(time.Second)
	if err := s.write(f); err != nil {
		return nil, err
	}
	return f, nil
}

// Delete removes a queue.
func (s *Store) Delete(id string) error {
	unlock := s.lock()
	defer unlock()
	if _, err := s.read(id); err != nil {
		return err
	}
	return os.Remove(s.path(id))
}

// CheckItem validates an item to add and fills its id.
func CheckItem(it *Item) error {
	it.Task = strings.TrimSpace(it.Task)
	switch {
	case it.Task == "" || !oneLine(it.Task, 200):
		return fmt.Errorf("%w: task is required, one line", ErrInvalid)
	case strings.TrimSpace(it.Answer) == "" || !utf8.ValidString(it.Answer) || len(it.Answer) > maxText:
		return fmt.Errorf("%w: answer is required, at most 64 KiB of text", ErrInvalid)
	case !utf8.ValidString(it.Question) || len(it.Question) > maxText:
		return fmt.Errorf("%w: question is at most 64 KiB of text", ErrInvalid)
	case it.JudgeLabel != "" && it.JudgeLabel != LabelCorrect && it.JudgeLabel != LabelIncorrect:
		return fmt.Errorf("%w: judge_label: want correct, incorrect or empty", ErrInvalid)
	case !oneLine(it.RunID, 128) || !oneLine(it.Experiment, 128) || !oneLine(it.Agent, 200) || !oneLine(it.AgentVersion, 200) || !oneLine(it.Ref, 300):
		return fmt.Errorf("%w: run_id, experiment, agent, agent_version and ref are one line each", ErrInvalid)
	case len(it.JudgeReason) > maxNote*2:
		return fmt.Errorf("%w: judge_reason is too long", ErrInvalid)
	}
	it.ID = ItemID(it.Task, it.Answer, it.RunID)
	it.Labels, it.Assigned, it.Resolution, it.Calibration, it.CalibrationError = nil, nil, nil, "", ""
	return nil
}

// AddItems appends items, skipping ones already in the queue. It returns
// how many were added and how many were already there.
func (s *Store) AddItems(id string, items []Item, actor string) (added, dup int, err error) {
	_, err = s.Update(id, func(f *File, now time.Time) error {
		have := map[string]bool{}
		for _, it := range f.Items {
			have[it.ID] = true
		}
		for _, it := range items {
			if err := CheckItem(&it); err != nil {
				return err
			}
			if have[it.ID] {
				dup++
				continue
			}
			if len(f.Items) >= MaxItems {
				return fmt.Errorf("%w: a queue holds at most %d items", ErrConflict, MaxItems)
			}
			it.AddedAt, it.AddedBy = now.Truncate(time.Second), actor
			f.Items = append(f.Items, it)
			have[it.ID] = true
			added++
		}
		return nil
	})
	return added, dup, err
}

// Assign gives every unresolved item that wants more people some of the
// queue's reviewers, round robin, so each item gets K different people.
// It needs reviewers named on the queue. It returns how many assignments
// it made.
func (s *Store) Assign(id string) (int, error) {
	n := 0
	_, err := s.Update(id, func(f *File, now time.Time) error {
		q := &f.Queue
		if len(q.Reviewers) == 0 {
			return fmt.Errorf("%w: name the reviewers on the queue first", ErrInvalid)
		}
		next := 0
		for i := range f.Items {
			it := &f.Items[i]
			if it.Resolution != nil {
				continue
			}
			for tries := 0; it.openSlots(q.ReviewersPerItem, now) > 0 && tries < len(q.Reviewers); tries++ {
				u := q.Reviewers[next%len(q.Reviewers)]
				next++
				if it.labelledBy(u) || slices.ContainsFunc(it.Assigned, func(a Assignment) bool { return a.UserID == u }) {
					continue
				}
				it.Assigned = append(it.Assigned, Assignment{UserID: u, At: now.Truncate(time.Second), Fixed: true})
				n++
			}
		}
		return nil
	})
	return n, err
}

// Next reserves the next item user should label and returns it, or nil
// when nothing is left for them. Items already assigned to the user come
// first, then (open mode) the oldest item that still wants people.
func (s *Store) Next(id, user string) (*Item, *Queue, error) {
	var got *Item
	f, err := s.Update(id, func(f *File, now time.Time) error {
		q := &f.Queue
		if q.Archived {
			return fmt.Errorf("%w: the queue is archived", ErrConflict)
		}
		if !q.MayLabel(user) {
			return fmt.Errorf("%w: you are not a reviewer of this queue", ErrForbidden)
		}
		pick := -1
		for i := range f.Items {
			it := &f.Items[i]
			if it.Resolution == nil && !it.labelledBy(user) && it.assignedTo(user, now) {
				pick = i
				break
			}
		}
		if pick < 0 && q.Assignment == AssignOpen {
			for i := range f.Items {
				it := &f.Items[i]
				if it.Resolution == nil && !it.labelledBy(user) && it.openSlots(q.ReviewersPerItem, now) > 0 {
					pick = i
					break
				}
			}
			if pick >= 0 {
				it := &f.Items[pick]
				it.Assigned = slices.DeleteFunc(it.Assigned, func(a Assignment) bool { return a.UserID == user })
				it.Assigned = append(it.Assigned, Assignment{UserID: user, At: now.Truncate(time.Second)})
			}
		}
		if pick >= 0 {
			c := f.Items[pick]
			got = &c
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return got, &f.Queue, nil
}

// LabelResult is what a label did.
type LabelResult struct {
	Item     Item
	Queue    Queue
	Resolved bool // the label resolved the item by consensus
}

// AddLabel records user's label on an item. The K-th agreeing label
// resolves the item by consensus.
func (s *Store) AddLabel(id, itemID string, l Label) (*LabelResult, error) {
	l.Label = strings.ToLower(strings.TrimSpace(l.Label))
	l.Note = strings.TrimSpace(l.Note)
	if l.Label != LabelCorrect && l.Label != LabelIncorrect {
		return nil, fmt.Errorf("%w: label: want correct or incorrect", ErrInvalid)
	}
	if utf8.RuneCountInString(l.Note) > maxNote || !utf8.ValidString(l.Note) {
		return nil, fmt.Errorf("%w: note is at most %d characters", ErrInvalid, maxNote)
	}
	if l.UserID == "" {
		return nil, fmt.Errorf("%w: sign in to label", ErrForbidden)
	}
	var res LabelResult
	_, err := s.Update(id, func(f *File, now time.Time) error {
		q := &f.Queue
		if q.Archived {
			return fmt.Errorf("%w: the queue is archived", ErrConflict)
		}
		if !q.MayLabel(l.UserID) {
			return fmt.Errorf("%w: you are not a reviewer of this queue", ErrForbidden)
		}
		i := slices.IndexFunc(f.Items, func(it Item) bool { return it.ID == itemID })
		if i < 0 {
			return fmt.Errorf("item %s: %w", itemID, ErrNotFound)
		}
		it := &f.Items[i]
		switch {
		case it.Resolution != nil:
			return fmt.Errorf("%w: this item is already resolved", ErrConflict)
		case it.labelledBy(l.UserID):
			return fmt.Errorf("%w: you already labelled this item", ErrConflict)
		case q.Assignment == AssignFixed && !it.assignedTo(l.UserID, now):
			return fmt.Errorf("%w: this item is assigned to other reviewers", ErrForbidden)
		case !it.assignedTo(l.UserID, now) && it.openSlots(q.ReviewersPerItem, now) <= 0:
			return fmt.Errorf("%w: this item already has the %d reviewers it needs", ErrConflict, q.ReviewersPerItem)
		}
		l.At = now.Truncate(time.Second)
		it.Labels = append(it.Labels, l)
		if len(it.Labels) >= q.ReviewersPerItem {
			agree := true
			for _, x := range it.Labels {
				if x.Label != it.Labels[0].Label {
					agree = false
				}
			}
			if agree {
				it.Resolution = &Resolution{Label: l.Label, Method: "consensus", At: l.At}
				res.Resolved = true
			}
		}
		res.Item, res.Queue = *it, *q
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// Resolve sets an item's final label: a decision on a disputed item, or an
// override of any item by the queue owner or an admin (the caller checks
// who may).
func (s *Store) Resolve(id, itemID string, r Resolution) (*Item, *Queue, error) {
	r.Label = strings.ToLower(strings.TrimSpace(r.Label))
	r.Note = strings.TrimSpace(r.Note)
	if r.Label != LabelCorrect && r.Label != LabelIncorrect {
		return nil, nil, fmt.Errorf("%w: label: want correct or incorrect", ErrInvalid)
	}
	if utf8.RuneCountInString(r.Note) > maxNote {
		return nil, nil, fmt.Errorf("%w: note is at most %d characters", ErrInvalid, maxNote)
	}
	var out Item
	f, err := s.Update(id, func(f *File, now time.Time) error {
		i := slices.IndexFunc(f.Items, func(it Item) bool { return it.ID == itemID })
		if i < 0 {
			return fmt.Errorf("item %s: %w", itemID, ErrNotFound)
		}
		it := &f.Items[i]
		if len(it.Labels) == 0 {
			return fmt.Errorf("%w: label the item before deciding it", ErrConflict)
		}
		r.Method, r.At = "decided", now.Truncate(time.Second)
		it.Resolution = &r
		it.Calibration, it.CalibrationError = "", ""
		out = *it
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return &out, &f.Queue, nil
}

// SetCalibration records the calibration label written for an item.
func (s *Store) SetCalibration(id, itemID, calID, calErr string) error {
	_, err := s.Update(id, func(f *File, _ time.Time) error {
		i := slices.IndexFunc(f.Items, func(it Item) bool { return it.ID == itemID })
		if i < 0 {
			return fmt.Errorf("item %s: %w", itemID, ErrNotFound)
		}
		f.Items[i].Calibration, f.Items[i].CalibrationError = calID, calErr
		return nil
	})
	return err
}
