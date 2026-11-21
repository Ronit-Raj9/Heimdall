package schedule

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// DefaultFile is the schedules file name under the project (bench) root.
const DefaultFile = "schedules.yaml"

// namePattern is what a schedule name may look like: it names lock files and
// ledger keys, so it stays a plain token.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// File is schedules.yaml. Alerts apply to every schedule, in addition to a
// schedule's own.
type File struct {
	Alerts    []alerts.Config `json:"alerts,omitempty" yaml:"alerts,omitempty"`
	Schedules []Schedule      `json:"schedules" yaml:"schedules"`
}

// Schedule is one experiment on a timetable.
type Schedule struct {
	Name         string          `json:"name" yaml:"name"`
	Experiment   string          `json:"experiment" yaml:"experiment"`                               // experiment file, relative to the project root
	Cron         string          `json:"cron" yaml:"cron"`                                           // 5-field cron expression
	Timezone     string          `json:"timezone,omitempty" yaml:"timezone,omitempty"`               // IANA name the cron and the spend day use; empty is the host's local zone
	Policy       string          `json:"policy,omitempty" yaml:"policy,omitempty"`                   // release policy file overriding the experiment's, relative to the project root
	MaxUSDPerDay float64         `json:"max_usd_per_day,omitempty" yaml:"max_usd_per_day,omitempty"` // daily spend cap; required unless the experiment is a smoke experiment
	FailOn       []string        `json:"fail_on,omitempty" yaml:"fail_on,omitempty"`                 // verdict decisions recorded as a failed check; empty is regressed and invalid
	Alerts       []alerts.Config `json:"alerts,omitempty" yaml:"alerts,omitempty"`
	// Job is a built-in job the daemon runs in place of an experiment (one of
	// Jobs); such a schedule names no experiment and spends nothing. Failure
	// inbox area: inbox-pull (docs/failure-inbox.md).
	Job string `json:"job,omitempty" yaml:"job,omitempty"`

	// Approved is what the daemon's cap check approved for this trigger; the
	// daemon sets it on the copy it hands the Runner, never in the file. A
	// Runner checks the experiment it actually runs against it, so a file
	// replaced between the check and the run cannot spend past the cap the
	// daemon approved (QC2-15-05).
	Approved *Approval `json:"-" yaml:"-"`
}

// Approval is the daemon's cap decision for one trigger (Schedule.Approved).
type Approval struct {
	// CapUSD is the experiment spend cap (budget.total_usd, a canary's
	// per_run_usd) the daemon checked and reserved; 0 with Limited false.
	CapUSD float64
	// Limited is false only for a smoke schedule without max_usd_per_day.
	Limited bool
	// Reserve durably adds usd to today's reservation before more spend
	// starts (a canary's confirmation look) and returns why it may not, or
	// "". nil when nothing more may be reserved.
	Reserve func(usd float64) string
}

// CheckExperiment returns why exp may not run under the approval, or nil:
// with a daily cap its spend cap must be a finite, positive amount within
// the approved one; without one it must be a smoke experiment.
func (a *Approval) CheckExperiment(exp *schemas.Experiment) error {
	if a == nil || exp == nil {
		return nil
	}
	if !a.Limited {
		if !exp.Smoke {
			return errors.New("the experiment file changed since the daily cap check: it is no longer a smoke experiment, and the schedule has no max_usd_per_day")
		}
		return nil
	}
	return a.CheckCap(exp.Budget.TotalUSD)
}

// CheckCap returns why a run with spend cap usd may not start under the
// approval, or nil.
func (a *Approval) CheckCap(usd float64) error {
	if a == nil || !a.Limited {
		return nil
	}
	if !finitePositive(usd) || usd > a.CapUSD+1e-9 {
		return fmt.Errorf("the experiment file changed since the daily cap check: its spend cap %v USD is not within the approved %.2f USD", usd, a.CapUSD)
	}
	return nil
}

// finitePositive reports whether v is a finite number above zero.
func finitePositive(v float64) bool {
	return v > 0 && !math.IsInf(v, 0) && !math.IsNaN(v)
}

// Built-in jobs (Schedule.Job). The CLI's runner implements each.
const (
	JobInboxPull = "inbox-pull" // Failure inbox area: pull flagged production sessions
)

// Jobs lists the built-in jobs a schedule may name.
var Jobs = []string{JobInboxPull}

// X6 live scoring: livescore-pull reads the gateway's logs once and scores
// the closed sessions (docs/live-scoring.md); it spends nothing.
const JobLivescorePull = "livescore-pull"

func init() { Jobs = append(Jobs, JobLivescorePull) } // X6 live scoring

// DefaultFailOn is the verdicts a scheduled check fails on when FailOn is
// empty, the same default as `check -fail-on`.
var DefaultFailOn = []string{string(schemas.DecisionRegressed), string(schemas.DecisionInvalid)}

// ExperimentLoader reads an experiment file; the CLI passes its strict,
// schema-checked loader.
type ExperimentLoader func(path string) (*schemas.Experiment, error)

// Load reads a schedules file strictly: an unknown key is an error, so a
// misspelled max_usd_per_day can never pass as no cap. A missing file is an
// empty File, so the first `schedule add` creates it.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &File{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read schedules %s: %w", path, err)
	}
	f := &File{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(f); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse schedules %s: %w", path, err)
	}
	return f, nil
}

// Save writes the file atomically: a temporary file in the same folder, then
// a rename, so a reader never sees half a file.
func Save(path string, f *File) error {
	data, err := yaml.Marshal(f)
	if err != nil {
		return fmt.Errorf("encode schedules: %w", err)
	}
	header := []byte("# Scheduled experiments (docs/schedules-and-alerts.md). Alerts name environment\n# variables only; never put a webhook URL or a secret in this file.\n")
	tmp, err := os.CreateTemp(filepath.Dir(path), ".schedules-*.yaml")
	if err != nil {
		return fmt.Errorf("save schedules %s: %w", path, err)
	}
	_, werr := tmp.Write(append(header, data...))
	if werr == nil {
		werr = tmp.Sync()
	}
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return errors.Join(fmt.Errorf("save schedules %s: %w", path, err), os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return errors.Join(fmt.Errorf("save schedules %s: %w", path, err), os.Remove(tmp.Name()))
	}
	return nil
}

// Find returns the schedule named name.
func (f *File) Find(name string) (Schedule, bool) {
	for _, s := range f.Schedules {
		if s.Name == name {
			return s, true
		}
	}
	return Schedule{}, false
}

// Add appends s. A name already in the file is an error.
func (f *File) Add(s Schedule) error {
	if _, ok := f.Find(s.Name); ok {
		return fmt.Errorf("schedule %q already exists; remove it first", s.Name)
	}
	f.Schedules = append(f.Schedules, s)
	return nil
}

// Remove deletes the schedule named name.
func (f *File) Remove(name string) error {
	i := slices.IndexFunc(f.Schedules, func(s Schedule) bool { return s.Name == name })
	if i < 0 {
		return fmt.Errorf("schedule %q: %w", name, schemas.ErrNotFound)
	}
	f.Schedules = slices.Delete(slices.Clone(f.Schedules), i, i+1)
	return nil
}

// AlertsFor returns the receivers of one schedule: the file's alerts, then
// the schedule's own, then extra (the server config's), in that order.
func (f *File) AlertsFor(s Schedule, extra []alerts.Config) []alerts.Config {
	out := slices.Clone(f.Alerts)
	out = append(out, s.Alerts...)
	return append(out, extra...)
}

// Validate checks every schedule against the project at root: names unique
// and well formed, cron expressions that fire, a timezone that loads, the
// experiment file and the policy file present, and a daily spend cap on
// every experiment that can spend. load reads each experiment file.
func (f *File) Validate(root string, load ExperimentLoader) error {
	var errs []error
	for _, a := range f.Alerts {
		if err := a.Validate(); err != nil {
			errs = append(errs, fmt.Errorf("alerts: %w", err))
		}
	}
	seen := map[string]bool{}
	for _, s := range f.Schedules {
		if seen[s.Name] {
			errs = append(errs, fmt.Errorf("schedule %q appears more than once", s.Name))
		}
		seen[s.Name] = true
		if err := s.Validate(root, load); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Validate checks one schedule (see File.Validate).
//
// A non-smoke experiment needs max_usd_per_day and its own spend cap
// (budget.total_usd): the daemon refuses a run whose cap would take the
// day's spend past max_usd_per_day, so an experiment without a cap could not
// be bounded and a forgotten schedule could spend without limit. A smoke
// experiment runs scripted parts and is exempt; a cap set on one still holds.
func (s Schedule) Validate(root string, load ExperimentLoader) error {
	if !namePattern.MatchString(s.Name) {
		return fmt.Errorf("schedule %q: name must be lowercase letters, digits, - or _, starting with a letter or digit, at most 64 characters", s.Name)
	}
	wrap := func(format string, args ...any) error {
		return fmt.Errorf("schedule %s: "+format, append([]any{s.Name}, args...)...)
	}
	c, err := ParseCron(s.Cron)
	if err != nil {
		return wrap("%w", err)
	}
	loc, err := s.Location()
	if err != nil {
		return wrap("%w", err)
	}
	if c.Next(time.Now().In(loc)).IsZero() {
		return wrap("cron %q never fires", s.Cron)
	}
	if s.MaxUSDPerDay < 0 || math.IsNaN(s.MaxUSDPerDay) || math.IsInf(s.MaxUSDPerDay, 0) { // QC2-15-09: a non-finite cap never holds
		return wrap("max_usd_per_day %v is not a finite, non-negative number", s.MaxUSDPerDay)
	}
	for _, d := range s.FailOn {
		switch schemas.Decision(d) {
		case schemas.DecisionRegressed, schemas.DecisionInconclusive, schemas.DecisionInvalid, schemas.DecisionImproved:
		default:
			return wrap("fail_on %q is not a verdict decision", d)
		}
	}
	for _, a := range s.Alerts {
		if err := a.Validate(); err != nil {
			return wrap("%w", err)
		}
	}
	if s.Job != "" { // a built-in job runs no experiment and spends nothing
		if !slices.Contains(Jobs, s.Job) {
			return wrap("job %q: want one of %s", s.Job, strings.Join(Jobs, ", "))
		}
		if s.Experiment != "" || s.Policy != "" || s.MaxUSDPerDay != 0 {
			return wrap("a job schedule takes no experiment, policy or max_usd_per_day")
		}
		return nil
	}
	if s.Policy != "" {
		if _, err := os.Stat(Resolve(root, s.Policy)); err != nil {
			return wrap("policy file: %w", err)
		}
	}
	if s.Experiment == "" {
		return wrap("experiment file is empty")
	}
	if _, err := os.Stat(Resolve(root, s.Experiment)); err != nil {
		return wrap("experiment file: %w", err)
	}
	if load == nil {
		return nil
	}
	exp, err := load(Resolve(root, s.Experiment))
	if err != nil {
		return wrap("%w", err)
	}
	if exp.Smoke && s.MaxUSDPerDay <= 0 {
		return nil
	}
	if s.MaxUSDPerDay <= 0 {
		return wrap("max_usd_per_day is required for a non-smoke experiment, so a forgotten schedule cannot spend without limit")
	}
	if !finitePositive(exp.Budget.TotalUSD) {
		return wrap("experiment %s has no spend cap (budget.total_usd); a scheduled non-smoke experiment needs one so max_usd_per_day can hold", s.Experiment)
	}
	if exp.Budget.TotalUSD > s.MaxUSDPerDay {
		return wrap("experiment spend cap %.2f USD is above max_usd_per_day %.2f USD, so no run could ever start", exp.Budget.TotalUSD, s.MaxUSDPerDay)
	}
	return nil
}

// Location returns the schedule's timezone.
func (s Schedule) Location() (*time.Location, error) {
	if s.Timezone == "" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(s.Timezone)
	if err != nil {
		return nil, fmt.Errorf("timezone %q: %w", s.Timezone, err)
	}
	return loc, nil
}

// FailDecisions returns FailOn, or DefaultFailOn when it is empty.
func (s Schedule) FailDecisions() []string {
	if len(s.FailOn) == 0 {
		return DefaultFailOn
	}
	return s.FailOn
}

// Resolve joins a project-relative path to root; an absolute path stays. The
// CLI uses it to find a schedule's experiment and policy files.
func Resolve(root, p string) string {
	if p == "" || filepath.IsAbs(p) || root == "" {
		return p
	}
	return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(p, "./")))
}
