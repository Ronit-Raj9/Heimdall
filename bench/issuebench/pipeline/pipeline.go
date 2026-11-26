// Package pipeline runs the issues-to-benchmark job: scan a repository for
// merged pull requests that close an issue, build one environment per
// repository version (reused across issues), validate each candidate, and
// emit the accepted ones into the org's private benchmark. Each stage sits
// behind a small interface so the scan, builder, validate and emit packages
// plug in without this package knowing them; tests use fakes.
//
// A run is resumable: its state is a JSON file written after every step, and
// Run skips work an earlier attempt finished.
package pipeline

import (
	"context"
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

	"github.com/maximhq/bifrost/bench/issuebench"
)

// ScanOptions narrow a scan.
type ScanOptions struct {
	Limit int    `json:"limit,omitempty"`
	Since string `json:"since,omitempty"` // YYYY-MM-DD; empty scans all
}

// Scanner finds candidates (lane G01). A scan is free: no model calls.
type Scanner interface {
	Scan(ctx context.Context, repo string, opts ScanOptions) ([]issuebench.Candidate, issuebench.Funnel, error)
}

// ModelChoice is the provider and model the person picked in the From
// issues form for the builder and the model checks; empty falls back to the
// operator's BENCH_ISSUEBENCH_PROVIDER and BENCH_ISSUEBENCH_MODEL.
type ModelChoice struct {
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	KeyID    string `json:"key_id,omitempty"` // which of the org's keys for the provider; empty picks one
}

// Valid is nil when both provider and model are set, or neither.
func (m ModelChoice) Valid() error {
	if (m.Provider == "") != (m.Model == "") {
		return fmt.Errorf("%w: pick both a provider and a model, or neither", ErrBadRequest)
	}
	return nil
}

type modelCtxKey struct{}

// WithModel carries the run's model choice to the stages (the validator's
// model checks read it with ModelFrom).
func WithModel(ctx context.Context, m ModelChoice) context.Context {
	return context.WithValue(ctx, modelCtxKey{}, m)
}

// ModelFrom is the model choice WithModel stored, or the zero choice.
func ModelFrom(ctx context.Context) ModelChoice {
	m, _ := ctx.Value(modelCtxKey{}).(ModelChoice)
	return m
}

// BuildOptions carry the one-click fixes a person chose for a retry, and
// the run's model choice.
type BuildOptions struct {
	ModelChoice
	Secrets       []string `json:"secrets,omitempty"`        // secret names made available to the build
	AllowServices []string `json:"allow_services,omitempty"` // services the build may start (postgres, redis)
	BudgetUSD     float64  `json:"budget_usd,omitempty"`     // dollar cap; 0 is the builder's default
}

// Builder builds one environment for a repository version (lane G02).
type Builder interface {
	Build(ctx context.Context, c issuebench.Candidate, opts BuildOptions, logf func(string, ...any)) (issuebench.BuildResult, error)
}

// Validator finds fail-to-pass and pass-to-pass tests (lane G04).
type Validator interface {
	Validate(ctx context.Context, c issuebench.Candidate, env issuebench.EnvSpec, logf func(string, ...any)) (issuebench.Validation, error)
}

// Emitter writes accepted tasks into the org's private benchmark (lane G05)
// and returns the benchmark id.
type Emitter interface {
	Emit(ctx context.Context, repo string, tasks []issuebench.Validation) (string, error)
}

// Stages are the four stage implementations; a nil stage stops the run with
// a plain reason instead of failing silently.
type Stages struct {
	Scanner   Scanner
	Builder   Builder
	Validator Validator
	Emitter   Emitter
	// Version maps a candidate to its repository version; candidates with the
	// same version share one environment. Nil: DefaultVersion.
	Version func(issuebench.Candidate) string
}

// DefaultVersion groups candidates by the month the fix was merged, so one
// environment serves the issues of one month.
func DefaultVersion(c issuebench.Candidate) string {
	if c.PRMerged.IsZero() {
		return "unknown"
	}
	return c.PRMerged.UTC().Format("2006-01")
}

// Item statuses, in pipeline order.
const (
	StatusQueued    = "queued"
	StatusBuilding  = "building"
	StatusBuilt     = "built"
	StatusValidated = "validated"
	StatusReady     = "ready"
	StatusFailed    = "failed"
	StatusAccepted  = "accepted"
	StatusRejected  = "rejected"
)

// Run statuses.
const (
	RunScanned  = "scanned"
	RunRunning  = "running"
	RunDone     = "done"
	RunFailed   = "failed"
	RunCanceled = "canceled"
)

// Fix kinds offered next to a failed item.
const (
	FixAddSecret    = "add_secret"
	FixAllowService = "allow_service"
	FixRaiseBudget  = "raise_budget"
	FixRetry        = "retry"
	FixOpenWorkers  = "open_workers" // no worker takes the job: open Settings > Workers, then retry
)

// Item is one candidate's progress.
type Item struct {
	Candidate   issuebench.Candidate   `json:"candidate"`
	Version     string                 `json:"version"`
	Status      string                 `json:"status"`
	Reason      string                 `json:"reason,omitempty"` // plain words
	Fix         string                 `json:"fix,omitempty"`    // one-click fix for a failed item
	Validation  *issuebench.Validation `json:"validation,omitempty"`
	Instruction string                 `json:"instruction,omitempty"` // edited instruction; empty uses the issue text
	Options     BuildOptions           `json:"options,omitempty"`
	JobID       string                 `json:"job_id,omitempty"`   // the job running or last run for this issue
	Stage       string                 `json:"stage,omitempty"`    // build, validate or checks (issuebench.Stage*)
	CostUSD     float64                `json:"cost_usd,omitempty"` // builds run for this issue, a failed one included
}

// Key is the item's id: the issue number.
func (it Item) Key() int { return it.Candidate.Issue }

// LogLine is one line of the run's log.
type LogLine struct {
	Seq  int64     `json:"seq"`
	At   time.Time `json:"at"`
	Line string    `json:"line"`
}

// State is one run's saved state.
type State struct {
	ID        string                            `json:"id"`
	Repo      string                            `json:"repo"`
	Status    string                            `json:"status"`
	Consent   bool                              `json:"consent"`
	Model     ModelChoice                       `json:"builder_model,omitempty"` // picked at start; empty uses the operator's model
	Detail    string                            `json:"detail,omitempty"`
	Funnel    issuebench.Funnel                 `json:"funnel"`
	Items     []Item                            `json:"items"`
	Envs      map[string]issuebench.BuildResult `json:"envs,omitempty"` // version -> build
	Benchmark string                            `json:"benchmark,omitempty"`
	CostUSD   float64                           `json:"cost_usd"`
	Created   time.Time                         `json:"created"`
	Updated   time.Time                         `json:"updated"`
	Log       []LogLine                         `json:"log,omitempty"`
}

// Errors the routes map to status codes.
var (
	ErrNotFound   = errors.New("not found")
	ErrNoConsent  = errors.New("building sends your code to your model provider with your key; turn on consent first")
	ErrBusy       = errors.New("this run is already running")
	ErrBadRequest = errors.New("bad request")
)

const maxLogLines = 2000

var repoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// ValidRepo reports whether repo is owner/name.
func ValidRepo(repo string) bool { return repoRe.MatchString(repo) && !strings.Contains(repo, "..") }

// Manager keeps an org's runs under dir and runs them.
type Manager struct {
	dir    string
	stages Stages
	now    func() time.Time

	mu      sync.Mutex
	running map[string]context.CancelFunc
	wg      sync.WaitGroup
	seq     int
}

// NewManager stores runs under dir (created on first write).
func NewManager(dir string, stages Stages) *Manager {
	if stages.Version == nil {
		stages.Version = DefaultVersion
	}
	return &Manager{dir: dir, stages: stages, now: time.Now, running: map[string]context.CancelFunc{}}
}

// Wait blocks until every background run returns (tests, shutdown).
func (m *Manager) Wait() { m.wg.Wait() }

var idRe = regexp.MustCompile(`^ib-[0-9a-z-]+$`)

func (m *Manager) path(id string) (string, error) {
	if !idRe.MatchString(id) {
		return "", ErrNotFound
	}
	return filepath.Join(m.dir, id+".json"), nil
}

// Load reads one run.
func (m *Manager) Load(id string) (*State, error) {
	p, err := m.path(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadLocked(p)
}

func (m *Manager) loadLocked(p string) (*State, error) {
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("read run: %w", err)
	}
	if s.Envs == nil {
		s.Envs = map[string]issuebench.BuildResult{}
	}
	return &s, nil
}

func (m *Manager) saveLocked(s *State) error {
	if err := os.MkdirAll(m.dir, 0o755); err != nil {
		return err
	}
	s.Updated = m.now().UTC()
	if n := len(s.Log); n > maxLogLines {
		s.Log = append([]LogLine(nil), s.Log[n-maxLogLines:]...)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	p, err := m.path(s.ID)
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// update loads, changes and saves one run under the lock.
func (m *Manager) update(id string, f func(s *State) error) (*State, error) {
	p, err := m.path(id)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.loadLocked(p)
	if err != nil {
		return nil, err
	}
	if err := f(s); err != nil {
		return nil, err
	}
	return s, m.saveLocked(s)
}

// List returns every run, newest first, without logs.
func (m *Manager) List() ([]State, error) {
	ents, err := os.ReadDir(m.dir)
	if errors.Is(err, os.ErrNotExist) {
		return []State{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []State{}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		s, err := m.Load(strings.TrimSuffix(name, ".json"))
		if err != nil {
			continue
		}
		s.Log = nil
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out, nil
}

// Scan runs a dry scan and saves it as a new run; no model calls.
func (m *Manager) Scan(ctx context.Context, repo string, opts ScanOptions) (*State, error) {
	if !ValidRepo(repo) {
		return nil, fmt.Errorf("%w: repo must be owner/name", ErrBadRequest)
	}
	if m.stages.Scanner == nil {
		return nil, errors.New("scanning is not available on this server yet")
	}
	cands, funnel, err := m.stages.Scanner.Scan(ctx, repo, opts)
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", repo, err)
	}
	now := m.now().UTC()
	m.mu.Lock()
	m.seq++
	id := fmt.Sprintf("ib-%s-%d", now.Format("20060102-150405"), m.seq)
	m.mu.Unlock()
	s := &State{ID: id, Repo: repo, Status: RunScanned, Funnel: funnel, Created: now, Envs: map[string]issuebench.BuildResult{}}
	for _, c := range cands {
		if c.Skip != "" {
			continue
		}
		s.Items = append(s.Items, Item{Candidate: c, Version: m.stages.Version(c), Status: StatusQueued})
	}
	s.Log = append(s.Log, LogLine{Seq: 1, At: now, Line: fmt.Sprintf("scanned %s: %d merged pull requests, %d linked to an issue, %d qualified", repo, funnel.MergedPRs, funnel.Linked, funnel.Qualified)})
	m.mu.Lock()
	defer m.mu.Unlock()
	return s, m.saveLocked(s)
}

// Start runs a scanned run in the background. consent must be true: the
// builder and validation send repository code to the org's model provider.
func (m *Manager) Start(id string, consent bool) (*State, error) {
	return m.StartWith(id, consent, ModelChoice{})
}

// StartWith is Start with the builder model the person picked; an empty
// choice keeps the run's earlier choice.
func (m *Manager) StartWith(id string, consent bool, model ModelChoice) (*State, error) {
	if !consent {
		return nil, ErrNoConsent
	}
	if err := model.Valid(); err != nil {
		return nil, err
	}
	if m.isRunning(id) {
		return nil, ErrBusy
	}
	s, err := m.update(id, func(s *State) error {
		s.Consent = true
		if model.Provider != "" {
			s.Model = model
		}
		s.Status = RunRunning
		s.Detail = ""
		return nil
	})
	if err != nil {
		return nil, err
	}
	m.launch(id)
	return s, nil
}

func (m *Manager) isRunning(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.running[id]
	return ok
}

func (m *Manager) launch(id string) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	m.running[id] = cancel
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer func() {
			m.mu.Lock()
			delete(m.running, id)
			m.mu.Unlock()
			cancel()
		}()
		_ = m.Run(ctx, id, nil)
	}()
}

// Cancel stops a running run; its state stays and Start resumes it.
func (m *Manager) Cancel(id string) (*State, error) {
	m.mu.Lock()
	cancel, ok := m.running[id]
	m.mu.Unlock()
	if ok {
		cancel()
	}
	return m.update(id, func(s *State) error {
		if s.Status == RunRunning {
			s.Status = RunCanceled
		}
		return nil
	})
}

// Run drives one run to the end, resuming where an earlier attempt stopped.
// extra, when set, also receives every log line (the job runner's log).
func (m *Manager) Run(ctx context.Context, id string, extra func(string)) error {
	logf := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		if extra != nil {
			extra(line)
		}
		_, _ = m.update(id, func(s *State) error {
			var seq int64 = 1
			if n := len(s.Log); n > 0 {
				seq = s.Log[n-1].Seq + 1
			}
			s.Log = append(s.Log, LogLine{Seq: seq, At: m.now().UTC(), Line: line})
			return nil
		})
	}
	s, err := m.Load(id)
	if err != nil {
		return err
	}
	if !s.Consent {
		return ErrNoConsent
	}
	logf("starting: %d issues", len(s.Items))
	// Passes repeat while items are queued: a retry made during a run joins it.
	for pass := 0; pass < 10; pass++ {
		s, err := m.Load(id)
		if err != nil {
			return err
		}
		pending := false
		for i, it := range s.Items {
			if err := ctx.Err(); err != nil {
				logf("stopped")
				return err
			}
			if it.Status == StatusQueued || it.Status == StatusBuilding || it.Status == StatusBuilt {
				pending = true
				m.step(ctx, id, i, logf)
			}
		}
		if !pending {
			break
		}
	}
	_, err = m.update(id, func(s *State) error {
		if ctx.Err() != nil {
			s.Status = RunCanceled
			return nil
		}
		s.Status = RunDone
		return nil
	})
	logf("finished")
	return err
}

// step moves item i forward through build and validate.
func (m *Manager) step(ctx context.Context, id string, i int, logf func(string, ...any)) {
	s, err := m.Load(id)
	if err != nil || i >= len(s.Items) {
		return
	}
	it := s.Items[i]
	switch it.Status {
	case StatusQueued, StatusBuilding, StatusBuilt:
	default:
		return // validated, ready, failed, accepted, rejected: nothing to do
	}
	c := it.Candidate
	ctx = issuebench.WithJobNote(ctx, func(stage, jobID string) {
		m.setJob(id, i, stage, jobID)
		logf("issue #%d: %s runs as job %s", c.Issue, stage, jobID)
	})
	env, ok := s.Envs[it.Version]
	if !ok || env.Stop != issuebench.StopOK || env.Env == nil {
		if m.stages.Builder == nil {
			m.setItem(id, i, StatusFailed, "building environments is not available on this server yet", FixRetry, nil)
			return
		}
		m.setItem(id, i, StatusBuilding, "", "", nil)
		logf("issue #%d: building the environment for version %s", c.Issue, it.Version)
		opts := it.Options
		if opts.Provider == "" {
			opts.ModelChoice = s.Model
		}
		res, err := m.stages.Builder.Build(ctx, c, opts, logf)
		if err != nil {
			reason, fix := failReason("build failed: ", err)
			m.setItem(id, i, StatusFailed, reason, fix, nil)
			logf("issue #%d: %s", c.Issue, reason)
			return
		}
		_, _ = m.update(id, func(s *State) error {
			s.CostUSD += res.CostUSD
			if i < len(s.Items) {
				s.Items[i].CostUSD += res.CostUSD
			}
			if res.Stop == issuebench.StopOK && res.Env != nil {
				s.Envs[it.Version] = res
			}
			return nil
		})
		if res.Stop != issuebench.StopOK || res.Env == nil {
			reason, fix := StopReasonText(res.Stop, res.Detail)
			m.setItem(id, i, StatusFailed, reason, fix, nil)
			logf("issue #%d: %s", c.Issue, reason)
			return
		}
		env = res
		logf("issue #%d: environment ready (%s)", c.Issue, env.Env.BaseImage)
	} else {
		logf("issue #%d: reusing the environment for version %s", c.Issue, it.Version)
	}
	m.setItem(id, i, StatusBuilt, "", "", nil)
	if m.stages.Validator == nil {
		m.setItem(id, i, StatusFailed, "validation is not available on this server yet", FixRetry, nil)
		return
	}
	v, err := m.stages.Validator.Validate(WithModel(ctx, s.Model), c, *env.Env, logf)
	if err != nil {
		reason, fix := failReason("validation failed: ", err)
		m.setItem(id, i, StatusFailed, reason, fix, nil)
		logf("issue #%d: %s", c.Issue, reason)
		return
	}
	if reason := Unusable(v); reason != "" {
		m.setItem(id, i, StatusFailed, reason, "", &v)
		logf("issue #%d: %s", c.Issue, reason)
		return
	}
	m.setItem(id, i, StatusReady, "", "", &v)
	logf("issue #%d: ready, %d tests fail before the fix and pass after, %d keep passing", c.Issue, len(v.FailToPass), len(v.PassToPass))
}

func (m *Manager) setItem(id string, i int, status, reason, fix string, v *issuebench.Validation) {
	_, _ = m.update(id, func(s *State) error {
		if i >= len(s.Items) {
			return nil
		}
		s.Items[i].Status, s.Items[i].Reason, s.Items[i].Fix = status, reason, fix
		if v != nil {
			s.Items[i].Validation = v
		}
		return nil
	})
}

// setJob records which job runs a stage of item i now.
func (m *Manager) setJob(id string, i int, stage, jobID string) {
	_, _ = m.update(id, func(s *State) error {
		if i < len(s.Items) {
			s.Items[i].Stage, s.Items[i].JobID = stage, jobID
		}
		return nil
	})
}

// noWorkerPhrase starts the error a stage returns when no worker takes its
// job (the server's errNoJobRunner).
const noWorkerPhrase = "No worker online"

// failReason turns a stage error into plain words and a one-click fix: no
// worker online offers open_workers, anything else a retry.
func failReason(prefix string, err error) (reason, fix string) {
	msg := err.Error()
	if i := strings.Index(msg, noWorkerPhrase); i >= 0 {
		return msg[i:], FixOpenWorkers
	}
	return prefix + msg, FixRetry
}

// Unusable says why a validation cannot become a task, or "".
func Unusable(v issuebench.Validation) string {
	switch {
	case v.Stop != "":
		return "validation stopped: " + v.Stop
	case len(v.FailToPass) == 0:
		return "no test fails before the fix and passes after it"
	case v.Checks.Leak:
		return "the issue text gives away the fix"
	case !v.Checks.Clear:
		return "the issue text is too unclear to solve"
	}
	return ""
}

// StopReasonText turns a builder stop into plain words and a one-click fix.
func StopReasonText(stop issuebench.StopReason, detail string) (string, string) {
	var reason, fix string
	switch stop {
	case issuebench.StopNeedsSecret:
		reason, fix = "the tests need a secret", FixAddSecret
	case issuebench.StopNeedsService:
		reason, fix = "the tests need a service such as a database", FixAllowService
	case issuebench.StopBudget:
		reason, fix = "the build ran out of budget", FixRaiseBudget
	case issuebench.StopNeedsGPU:
		reason = "the tests need a GPU"
	case issuebench.StopPrivateRegistry:
		reason = "a dependency comes from a private package registry"
	case issuebench.StopUnsupported:
		reason = "this language is not supported yet (supported: Python with pytest, JavaScript and TypeScript with jest or vitest, Go)"
	case issuebench.StopLoop:
		reason, fix = "the builder kept repeating the same steps", FixRetry
	case issuebench.StopTimeout:
		reason, fix = "the build took too long", FixRetry
	default:
		reason, fix = "the build stopped", FixRetry
	}
	if detail != "" {
		reason += ": " + detail
	}
	return reason, fix
}

func (m *Manager) findItem(s *State, issue int) (int, error) {
	for i := range s.Items {
		if s.Items[i].Candidate.Issue == issue {
			return i, nil
		}
	}
	return 0, ErrNotFound
}

// Review is a person's decision on one ready item.
type Review struct {
	Decision    string `json:"decision"` // accept, reject or edit
	Instruction string `json:"instruction,omitempty"`
}

// Review accepts, rejects or edits one item. Accepting emits every accepted
// item of the run into the org's private benchmark.
func (m *Manager) Review(ctx context.Context, id string, issue int, r Review) (*State, error) {
	s, err := m.update(id, func(s *State) error {
		i, err := m.findItem(s, issue)
		if err != nil {
			return err
		}
		it := &s.Items[i]
		switch r.Decision {
		case "accept":
			if it.Status != StatusReady && it.Status != StatusAccepted {
				return fmt.Errorf("%w: only a ready issue can be accepted", ErrBadRequest)
			}
			it.Status = StatusAccepted
		case "reject":
			it.Status = StatusRejected
		case "edit":
			if strings.TrimSpace(r.Instruction) == "" {
				return fmt.Errorf("%w: instruction is empty", ErrBadRequest)
			}
			it.Instruction = r.Instruction
		default:
			return fmt.Errorf("%w: decision must be accept, reject or edit", ErrBadRequest)
		}
		return nil
	})
	if err != nil || r.Decision != "accept" {
		return s, err
	}
	return m.emit(ctx, s)
}

func (m *Manager) emit(ctx context.Context, s *State) (*State, error) {
	if m.stages.Emitter == nil {
		return s, errors.New("adding tasks to a benchmark is not available on this server yet")
	}
	var tasks []issuebench.Validation
	for _, it := range s.Items {
		if it.Status != StatusAccepted || it.Validation == nil {
			continue
		}
		v := *it.Validation
		if it.Instruction != "" {
			v.Candidate.IssueBody = it.Instruction
		}
		tasks = append(tasks, v)
	}
	bench, err := m.stages.Emitter.Emit(ctx, s.Repo, tasks)
	if err != nil {
		return s, fmt.Errorf("add tasks: %w", err)
	}
	return m.update(s.ID, func(s *State) error { s.Benchmark = bench; return nil })
}

// Retry queues one failed item again with a fix applied and resumes the run.
func (m *Manager) Retry(id string, issue int, fix string, opts BuildOptions) (*State, error) {
	s, err := m.update(id, func(s *State) error {
		if !s.Consent {
			return ErrNoConsent
		}
		i, err := m.findItem(s, issue)
		if err != nil {
			return err
		}
		it := &s.Items[i]
		if it.Status != StatusFailed {
			return fmt.Errorf("%w: only a failed issue can be retried", ErrBadRequest)
		}
		switch fix {
		case FixAddSecret:
			if len(opts.Secrets) == 0 {
				return fmt.Errorf("%w: name the secret to add", ErrBadRequest)
			}
		case FixAllowService:
			if len(opts.AllowServices) == 0 {
				return fmt.Errorf("%w: name the service to allow", ErrBadRequest)
			}
		case FixRaiseBudget:
			if opts.BudgetUSD <= 0 {
				return fmt.Errorf("%w: budget_usd must be above 0", ErrBadRequest)
			}
		case FixRetry, FixOpenWorkers, "":
		default:
			return fmt.Errorf("%w: unknown fix %q", ErrBadRequest, fix)
		}
		if err := opts.ModelChoice.Valid(); err != nil {
			return err
		}
		if opts.Provider != "" {
			it.Options.ModelChoice = opts.ModelChoice
		}
		it.Options.Secrets = append(it.Options.Secrets, opts.Secrets...)
		it.Options.AllowServices = append(it.Options.AllowServices, opts.AllowServices...)
		if opts.BudgetUSD > 0 {
			it.Options.BudgetUSD = opts.BudgetUSD
		}
		it.Status, it.Reason, it.Fix, it.Validation = StatusQueued, "", "", nil
		s.Status = RunRunning
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !m.isRunning(id) {
		m.launch(id)
	}
	return s, nil
}
