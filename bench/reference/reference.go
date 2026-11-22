// Package reference keeps the Heimdall reference board (gap G4-03): scores
// of known agents on the built-in library benchmarks, measured by the
// operator in an operator-owned organization and published one evaluation
// at a time. Nothing here invents a number: every entry is a snapshot of a
// finished evaluation's report card, taken only after every sealed run of
// its experiments and each experiment's seal chain verified, and it names
// the chain head (the last seal hash) it was taken from.
//
// The board is one JSON file under the server's storage root,
// <storage root>/.reference/board.json. The leading dot keeps it out of the
// run store's experiment list. The CLI (bifrost-bench reference) writes it;
// the server only reads it, for the public board route.
package reference

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/maximhq/bifrost/bench/reportcard"
	"github.com/maximhq/bifrost/bench/schemas"
)

// The operator-owned organization the reference evaluations run in.
const (
	OrgSlug = "heimdall-reference"
	OrgName = "Heimdall reference"
)

// Entry is one agent version on one model on one library benchmark, as one
// published evaluation measured it.
type Entry struct {
	ID           string `json:"id"` // <evaluation>:<benchmark>:<arm label>
	EvaluationID string `json:"evaluation_id"`
	ExperimentID string `json:"experiment_id"`
	Benchmark    string `json:"benchmark"`
	// BenchmarkVersion and LockHash name the exact benchmark build the runs
	// used (the experiment's frozen suite lock).
	BenchmarkVersion string `json:"benchmark_version,omitempty"`
	LockHash         string `json:"lock_hash,omitempty"`
	Capability       string `json:"capability"`
	Agent            string `json:"agent"`
	// Version is the agent version saved in the reference organization (0
	// when the runs were imported from elsewhere); AgentBuild is the build
	// the agent config declares (agent_version, for example a CLI release).
	Version    int    `json:"version,omitempty"`
	AgentBuild string `json:"agent_build,omitempty"`
	Model      string `json:"model"`
	// Score is verified successes over counted clean runs, with the report
	// card's 95% task-clustered interval.
	Score  float64 `json:"score"`
	CILow  float64 `json:"ci_low"`
	CIHigh float64 `json:"ci_high"`
	Runs   int     `json:"runs"`
	Tasks  int     `json:"tasks"`
	Solved int     `json:"solved"`
	// CostUSD is the total cost of the counted runs and CostPerSolvedUSD
	// that over the solved runs; both nil unless every run's cost is known
	// (an unpriced run is never $0), and the latter nil with nothing solved.
	CostUSD          *float64 `json:"cost_usd"`
	CostPerSolvedUSD *float64 `json:"cost_per_solved_usd"`
	// WallS is the median wall time of one run, in seconds.
	WallS float64 `json:"wall_s"`
	// RanAt is when the newest counted run was sealed.
	RanAt time.Time `json:"ran_at"`
	// SealHash is the experiment's chain head when it was verified for
	// publishing; SealedRuns the sealed runs that chain covers.
	SealHash   string `json:"seal_hash"`
	SealedRuns int    `json:"sealed_runs"`
	// ReportPath is the path of the read-only report card page (a share
	// link of the reference organization), "" when none was made.
	ReportPath  string    `json:"report_path,omitempty"`
	PublishedAt time.Time `json:"published_at"`
	PublishedBy string    `json:"published_by,omitempty"`
}

// Board is the published reference board.
type Board struct {
	Entries []Entry `json:"entries"`
	// Shares maps each published evaluation to the share link id made for
	// it, so unpublishing revokes the link too.
	Shares    map[string]string `json:"shares,omitempty"`
	UpdatedAt *time.Time        `json:"updated_at,omitempty"`
}

// Evaluations lists the published evaluation ids, sorted.
func (b *Board) Evaluations() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range b.Entries {
		if !seen[e.EvaluationID] {
			seen[e.EvaluationID] = true
			out = append(out, e.EvaluationID)
		}
	}
	sort.Strings(out)
	return out
}

// BenchmarkKey is the key a benchmark id is matched on: no "@version" pin,
// lower case.
func BenchmarkKey(id string) string {
	id, _, _ = strings.Cut(strings.TrimSpace(id), "@")
	return strings.ToLower(id)
}

// ForBenchmark returns the entries of one benchmark (matched on
// BenchmarkKey), in board order; empty id returns every entry.
func (b *Board) ForBenchmark(id string) []Entry {
	out := []Entry{}
	key := BenchmarkKey(id)
	for _, e := range b.Entries {
		if key == "" || BenchmarkKey(e.Benchmark) == key {
			out = append(out, e)
		}
	}
	return out
}

// Benchmarks lists the benchmarks with at least one entry, sorted.
func (b *Board) Benchmarks() []string {
	seen := map[string]bool{}
	out := []string{}
	for _, e := range b.Entries {
		if k := BenchmarkKey(e.Benchmark); !seen[k] {
			seen[k] = true
			out = append(out, e.Benchmark)
		}
	}
	sort.Strings(out)
	return out
}

// sortEntries orders the board: by benchmark, then score (best first), then
// the lower interval bound, then newest, then id.
func sortEntries(es []Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if ka, kb := BenchmarkKey(a.Benchmark), BenchmarkKey(b.Benchmark); ka != kb {
			return ka < kb
		}
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if a.CILow != b.CILow {
			return a.CILow > b.CILow
		}
		if !a.RanAt.Equal(b.RanAt) {
			return a.RanAt.After(b.RanAt)
		}
		return a.ID < b.ID
	})
}

// Dir is the board folder under a storage root.
func Dir(storageRoot string) string { return filepath.Join(storageRoot, ".reference") }

const boardFile = "board.json"

var locks sync.Map // dir -> *sync.Mutex

func lockFor(dir string) *sync.Mutex {
	m, _ := locks.LoadOrStore(dir, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// Store reads and writes the board of one storage root.
type Store struct{ dir string }

// Open returns the board store of storageRoot.
func Open(storageRoot string) *Store { return &Store{dir: Dir(storageRoot)} }

// Path is the board file.
func (s *Store) Path() string { return filepath.Join(s.dir, boardFile) }

// Load reads the board; a missing file is an empty board.
func (s *Store) Load() (*Board, error) {
	mu := lockFor(s.dir)
	mu.Lock()
	defer mu.Unlock()
	return s.read()
}

func (s *Store) read() (*Board, error) {
	b := &Board{Entries: []Entry{}}
	data, err := os.ReadFile(s.Path())
	if errors.Is(err, os.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read reference board: %w", err)
	}
	if err := json.Unmarshal(data, b); err != nil {
		return nil, fmt.Errorf("read reference board %s: %w", s.Path(), err)
	}
	if b.Entries == nil {
		b.Entries = []Entry{}
	}
	return b, nil
}

func (s *Store) write(b *Board) error {
	sortEntries(b.Entries)
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, ".board-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), s.Path())
}

// Publish puts the entries of one evaluation on the board, replacing any
// it had before, and records its share link id ("" for none). It returns
// the share link id the evaluation had before, so the caller can revoke a
// replaced link.
func (s *Store) Publish(evalID string, entries []Entry, shareID string, now time.Time) (previousShare string, _ error) {
	if evalID == "" || len(entries) == 0 {
		return "", errors.New("publish: no entries")
	}
	for _, e := range entries {
		if e.EvaluationID != evalID {
			return "", fmt.Errorf("publish: entry %s belongs to evaluation %s, not %s", e.ID, e.EvaluationID, evalID)
		}
	}
	mu := lockFor(s.dir)
	mu.Lock()
	defer mu.Unlock()
	b, err := s.read()
	if err != nil {
		return "", err
	}
	kept := b.Entries[:0]
	for _, e := range b.Entries {
		if e.EvaluationID != evalID {
			kept = append(kept, e)
		}
	}
	b.Entries = append(kept, entries...)
	if b.Shares == nil {
		b.Shares = map[string]string{}
	}
	previousShare = b.Shares[evalID]
	if shareID != "" {
		b.Shares[evalID] = shareID
	} else {
		delete(b.Shares, evalID)
	}
	t := now.UTC()
	b.UpdatedAt = &t
	return previousShare, s.write(b)
}

// Unpublish takes one evaluation off the board. It returns the entries it
// removed and the evaluation's share link id ("" for none); removing an
// evaluation that is not on the board is not an error and removes nothing.
func (s *Store) Unpublish(evalID string, now time.Time) (removed []Entry, shareID string, _ error) {
	mu := lockFor(s.dir)
	mu.Lock()
	defer mu.Unlock()
	b, err := s.read()
	if err != nil {
		return nil, "", err
	}
	kept := []Entry{}
	for _, e := range b.Entries {
		if e.EvaluationID == evalID {
			removed = append(removed, e)
		} else {
			kept = append(kept, e)
		}
	}
	shareID = b.Shares[evalID]
	if len(removed) == 0 && shareID == "" {
		return nil, "", nil
	}
	b.Entries = kept
	delete(b.Shares, evalID)
	t := now.UTC()
	b.UpdatedAt = &t
	return removed, shareID, s.write(b)
}

// ChainHead reads the last line of an experiment's seals.log under a store
// root: the chain head hash and the number of sealed runs. Call it only
// after the chain verified (store.VerifyChain).
func ChainHead(storeRoot, expID string) (head string, runs int, _ error) {
	data, err := os.ReadFile(filepath.Join(storeRoot, expID, "seals.log"))
	if err != nil {
		return "", 0, fmt.Errorf("seals of %s: %w", expID, err)
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return "", 0, fmt.Errorf("seals of %s: malformed line %q", expID, line)
		}
		head = f[1]
		runs++
	}
	if runs == 0 {
		return "", 0, fmt.Errorf("experiment %s has no sealed run", expID)
	}
	return head, runs, nil
}

// Seal is what the verification of one experiment found.
type Seal struct {
	Head  string    // chain head hash
	Runs  int       // sealed runs in the chain
	RanAt time.Time // newest sealed time among its runs
}

// Input is what Entries turns into board entries.
type Input struct {
	Evaluation *schemas.Evaluation
	Report     *reportcard.Report
	// Seals holds the verification of every child experiment, by id. A
	// child experiment missing from it is refused: nothing goes on the board
	// unverified.
	Seals map[string]Seal
	// Manifests are the child experiments' manifests, by id, for the
	// benchmark lock and the agent build.
	Manifests map[string]*schemas.Manifest
	// Library reports that a benchmark id is a built-in library benchmark.
	Library     func(benchmark string) bool
	ReportPath  string
	PublishedBy string
	Now         time.Time
}

// Check refuses an evaluation that cannot be published: one that is not
// done, ran held-out tasks (publishing would expose them) or reruns another
// evaluation's failures (its numbers are not a score).
func Check(ev *schemas.Evaluation) error {
	switch {
	case ev == nil:
		return errors.New("no evaluation")
	case ev.Status != schemas.EvaluationDone:
		return fmt.Errorf("evaluation %s is %s, not done: only finished evaluations are published", ev.ID, ev.Status)
	case ev.Spec.Split == schemas.SplitHeldOut:
		return fmt.Errorf("evaluation %s ran held-out tasks: publishing would expose them", ev.ID)
	case ev.RerunOf != "":
		return fmt.Errorf("evaluation %s reruns the failures of %s: it is not a score", ev.ID, ev.RerunOf)
	}
	return nil
}

// Entries builds the board entries of one verified evaluation: one per
// benchmark and arm with counted runs. skipped says, in plain words, which
// benchmark rows were left out and why. It refuses the whole evaluation
// when any of its benchmarks is not a library benchmark or any child
// experiment did not verify.
func Entries(in Input) (entries []Entry, skipped []string, _ error) {
	ev, rep := in.Evaluation, in.Report
	if err := Check(ev); err != nil {
		return nil, nil, err
	}
	if rep == nil {
		return nil, nil, errors.New("no report card")
	}
	if in.Library == nil {
		return nil, nil, errors.New("the built-in benchmark library is not available")
	}
	var notLibrary []string
	for _, c := range ev.Children {
		if c.Skipped != "" || c.ExperimentID == "" {
			continue
		}
		if !in.Library(c.Benchmark) {
			notLibrary = append(notLibrary, c.Benchmark)
		}
		if _, ok := in.Seals[c.ExperimentID]; !ok {
			return nil, nil, fmt.Errorf("experiment %s of %s was not verified", c.ExperimentID, c.Benchmark)
		}
	}
	if len(notLibrary) > 0 {
		return nil, nil, fmt.Errorf("only built-in library benchmarks are published; %s is not", strings.Join(notLibrary, ", "))
	}
	now := in.Now.UTC()
	for _, b := range rep.Benchmarks {
		if b.ExperimentID == "" {
			skipped = append(skipped, b.Benchmark+": never ran")
			continue
		}
		seal, ok := in.Seals[b.ExperimentID]
		if !ok {
			return nil, nil, fmt.Errorf("experiment %s of %s was not verified", b.ExperimentID, b.Benchmark)
		}
		m := in.Manifests[b.ExperimentID]
		for _, arm := range rep.Arms {
			met, ok := b.Arms[arm.Label]
			if !ok || met.Runs == 0 {
				skipped = append(skipped, fmt.Sprintf("%s (%s): no counted run", b.Benchmark, arm.Label))
				continue
			}
			e := Entry{
				ID:           ev.ID + ":" + b.Benchmark + ":" + arm.Label,
				EvaluationID: ev.ID, ExperimentID: b.ExperimentID,
				Benchmark: b.Benchmark, Capability: b.Capability,
				Agent: arm.Agent, Version: arm.Version, Model: arm.Model,
				Score: met.Score, CILow: met.CILow, CIHigh: met.CIHigh,
				Runs: met.Runs, Tasks: met.Tasks, Solved: int(math.Round(met.Score * float64(met.Runs))),
				WallS: met.WallS, RanAt: seal.RanAt, SealHash: seal.Head, SealedRuns: seal.Runs,
				ReportPath: in.ReportPath, PublishedAt: now, PublishedBy: in.PublishedBy,
			}
			if m != nil {
				e.BenchmarkVersion, e.LockHash = m.SuiteVersion, m.SuiteLockHash
				e.AgentBuild = agentBuild(m, arm)
			}
			if met.CostUSD != nil {
				c := *met.CostUSD
				e.CostUSD = &c
				if e.Solved > 0 {
					per := math.Round(c/float64(e.Solved)*1e4) / 1e4
					e.CostPerSolvedUSD = &per
				}
			}
			entries = append(entries, e)
		}
	}
	if len(entries) == 0 {
		return nil, skipped, fmt.Errorf("evaluation %s has no counted run on any benchmark: nothing to publish", ev.ID)
	}
	return entries, skipped, nil
}

// agentBuild is the agent_version the manifest's arm for label froze, ""
// when the config declares none.
func agentBuild(m *schemas.Manifest, arm reportcard.Arm) string {
	ids := make([]string, 0, len(m.Arms))
	for id := range m.Arms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		a := m.Arms[id]
		if strings.EqualFold(id, arm.Label) || strings.EqualFold(a.Label, arm.Label) {
			return a.Config.AgentVersion
		}
	}
	if len(ids) == 1 {
		return m.Arms[ids[0]].Config.AgentVersion
	}
	return ""
}
