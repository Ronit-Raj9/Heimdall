// Package livemon is live synthetic monitoring (lane Z22): a monitor runs a
// few real tasks from a ready benchmark against a deployed agent on a
// timetable, through the same scheduled evaluation path as every other
// scheduled check, and tracks pass rate, latency, cost and errors over time.
//
// The package holds the monitor definition, its samples (one per check),
// the incident engine and a file store. It runs nothing itself: handlers
// turn a monitor into a schedule (an evaluation of Tasks tasks x 1 trial),
// read finished evaluations back into samples and send the notices Step
// returns. Every rule here is pure, so the same samples always give the
// same incidents.
//
// Incidents are raised by a statistical rule over a rolling window, never
// by one failed run:
//
//   - quality: the pass rate of the last Window checks is below the
//     baseline (the Baseline checks before them, frozen when the incident
//     opens) by at least MinDrop, with a one-sided Fisher exact p below
//     Alpha; or below the optional MinPassRate floor with a one-sided
//     binomial p below Alpha.
//   - latency: the median latency of the recent window is above
//     LatencyFactor times the baseline median, with a one-sided
//     Mann-Whitney p below Alpha.
//   - infra: OutageAfter checks in a row where the endpoint did not answer,
//     most runs ended for infrastructure reasons, or the check could not
//     start. Infra incidents never count as a quality drop.
//
// Each incident is one alert thread: opened, updated when it gets
// materially worse, resolved when the rule clears.
package livemon

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Defaults and limits.
const (
	DefaultTasks       = 3
	MaxTasks           = 5
	DefaultIntervalMin = 60
	MinIntervalMin     = 15
	MaxNameLen         = 80
	MaxBudgetUSDPerDay = 1000.0
	// SchedulePrefix starts the name of every monitor's schedule.
	SchedulePrefix = "live-"
)

// Intervals are the timetables a monitor may use, in minutes. Each divides
// an hour or a day evenly, so a check fires at the same minute every time.
var Intervals = []int{15, 30, 60, 120, 180, 240, 360, 720, 1440}

// Rule defaults.
const (
	DefaultWindow        = 3
	DefaultBaseline      = 24
	DefaultMinDrop       = 0.2
	DefaultAlpha         = 0.05
	DefaultOutageAfter   = 2
	DefaultLatencyFactor = 2.0
	// MinRecentRuns and MinBaselineRuns are the fewest scored runs a quality
	// test looks at; below them the monitor is still learning.
	MinRecentRuns   = 5
	MinBaselineRuns = 10
)

// ErrInvalid wraps every refused input.
var ErrInvalid = errors.New("invalid monitor")

// ErrNotFound is a monitor that does not exist.
var ErrNotFound = errors.New("monitor not found")

var (
	idRe    = regexp.MustCompile(`^mon_[a-f0-9]{12}$`)
	agentRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	benchRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,127}$`)
	modelRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@+-]{0,199}$`)
)

// Rule is a monitor's incident rule. Zero fields take the defaults.
type Rule struct {
	Window        int     `json:"window,omitempty"`          // checks in the recent window
	Baseline      int     `json:"baseline,omitempty"`        // checks in the baseline window before it
	MinDrop       float64 `json:"min_drop,omitempty"`        // smallest pass rate drop that alerts, 0 to 1
	Alpha         float64 `json:"alpha,omitempty"`           // significance level of every test
	MinPassRate   float64 `json:"min_pass_rate,omitempty"`   // optional floor, 0 to 1; 0 is off
	OutageAfter   int     `json:"outage_after,omitempty"`    // infra checks in a row that open an infra incident
	LatencyFactor float64 `json:"latency_factor,omitempty"`  // median latency growth that alerts; negative is off
	MinRecentRuns int     `json:"min_recent_runs,omitempty"` // scored runs the recent window needs
}

// Effective returns r with the defaults filled in.
func (r Rule) Effective() Rule {
	if r.Window <= 0 {
		r.Window = DefaultWindow
	}
	if r.Baseline <= 0 {
		r.Baseline = DefaultBaseline
	}
	if r.MinDrop <= 0 {
		r.MinDrop = DefaultMinDrop
	}
	if r.Alpha <= 0 {
		r.Alpha = DefaultAlpha
	}
	if r.OutageAfter <= 0 {
		r.OutageAfter = DefaultOutageAfter
	}
	if r.LatencyFactor == 0 {
		r.LatencyFactor = DefaultLatencyFactor
	}
	if r.MinRecentRuns <= 0 {
		r.MinRecentRuns = MinRecentRuns
	}
	return r
}

// Validate checks the rule's ranges.
func (r Rule) Validate() error {
	switch {
	case r.Window < 0 || r.Window > 48:
		return fmt.Errorf("%w: rule.window must be between 1 and 48 checks", ErrInvalid)
	case r.Baseline < 0 || r.Baseline > 500:
		return fmt.Errorf("%w: rule.baseline must be between 1 and 500 checks", ErrInvalid)
	case !inUnit(r.MinDrop):
		return fmt.Errorf("%w: rule.min_drop must be between 0 and 1", ErrInvalid)
	case math.IsNaN(r.Alpha) || r.Alpha < 0 || r.Alpha > 0.2:
		return fmt.Errorf("%w: rule.alpha must be between 0 and 0.2", ErrInvalid)
	case !inUnit(r.MinPassRate):
		return fmt.Errorf("%w: rule.min_pass_rate must be between 0 and 1", ErrInvalid)
	case r.OutageAfter < 0 || r.OutageAfter > 20:
		return fmt.Errorf("%w: rule.outage_after must be between 1 and 20 checks", ErrInvalid)
	case math.IsNaN(r.LatencyFactor) || math.IsInf(r.LatencyFactor, 0) || (r.LatencyFactor > 0 && r.LatencyFactor < 1.1) || r.LatencyFactor > 100:
		return fmt.Errorf("%w: rule.latency_factor must be between 1.1 and 100, or negative to turn latency alerts off", ErrInvalid)
	case r.MinRecentRuns < 0 || r.MinRecentRuns > 500:
		return fmt.Errorf("%w: rule.min_recent_runs must be between 1 and 500", ErrInvalid)
	}
	return nil
}

func inUnit(v float64) bool { return !math.IsNaN(v) && v >= 0 && v <= 1 }

// Monitor is one live monitor of a deployed agent.
type Monitor struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	Agent           string    `json:"agent"`
	Version         int       `json:"version,omitempty"`  // 0 is the agent's latest version at each check
	Model           string    `json:"model,omitempty"`    // empty is the agent's own model
	Provider        string    `json:"provider,omitempty"` // gateway provider of Model
	Benchmark       string    `json:"benchmark"`          // ready benchmark the tasks come from
	Tasks           int       `json:"tasks"`              // tasks per check, 1 to MaxTasks
	IntervalMin     int       `json:"interval_min"`       // minutes between checks, one of Intervals
	BudgetUSDPerDay float64   `json:"budget_usd_per_day"` // daily spend cap of the monitor
	Worker          string    `json:"worker,omitempty"`   // connected worker; empty is the org default
	Paused          bool      `json:"paused"`
	Rule            Rule      `json:"rule"`
	Badge           bool      `json:"badge"`                  // a public read-only status badge is on
	BadgeSecret     string    `json:"badge_secret,omitempty"` // random part of the badge link; never sent to the UI
	CreatedAt       time.Time `json:"created_at"`
	CreatedBy       string    `json:"created_by,omitempty"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ValidID reports whether id looks like a monitor id.
func ValidID(id string) bool { return idRe.MatchString(id) }

// NewID returns a fresh monitor id.
func NewID() string { return "mon_" + RandHex(6) }

// RandHex returns 2n random hex characters.
func RandHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// ScheduleName is the name of the monitor's schedule.
func (m Monitor) ScheduleName() string { return SchedulePrefix + m.ID }

// MonitorIDOfSchedule returns the monitor id a schedule name belongs to.
func MonitorIDOfSchedule(name string) (string, bool) {
	id, ok := strings.CutPrefix(name, SchedulePrefix)
	return id, ok && ValidID(id)
}

// ChecksPerDay is how many checks the interval fires in a day.
func (m Monitor) ChecksPerDay() int {
	if m.IntervalMin <= 0 {
		return 24
	}
	return max(1, 1440/m.IntervalMin)
}

// CheckBudgetUSD is the spend cap of one check: the daily budget shared
// evenly by the day's checks, rounded down to a cent.
func (m Monitor) CheckBudgetUSD() float64 {
	return math.Floor(m.BudgetUSDPerDay/float64(m.ChecksPerDay())*100) / 100
}

// Cron is the monitor's 5-field cron expression (UTC). The minute (and for
// daily checks the hour) is spread by the monitor id, so monitors created
// together do not all fire at the same moment.
func (m Monitor) Cron() string {
	h := fnv.New32a()
	h.Write([]byte(m.ID))
	seed := int(h.Sum32() % 1440)
	iv := m.IntervalMin
	switch {
	case iv < 60:
		return fmt.Sprintf("%d/%d * * * *", seed%iv, iv)
	case iv == 60:
		return fmt.Sprintf("%d * * * *", seed%60)
	case iv < 1440:
		hours := iv / 60
		return fmt.Sprintf("%d %d/%d * * *", seed%60, (seed/60)%hours, hours)
	}
	return fmt.Sprintf("%d %d * * *", seed%60, (seed/60)%24)
}

// Normalize trims the fields, fills defaults and validates m. The minimum
// interval is the hosted schedule minimum, so local and hosted agree.
func (m *Monitor) Normalize() error {
	m.Name = strings.TrimSpace(m.Name)
	m.Agent = strings.TrimSpace(m.Agent)
	m.Model = strings.TrimSpace(m.Model)
	m.Provider = strings.TrimSpace(m.Provider)
	m.Benchmark = strings.TrimSpace(m.Benchmark)
	m.Worker = strings.TrimSpace(m.Worker)
	if m.Tasks == 0 {
		m.Tasks = DefaultTasks
	}
	if m.IntervalMin == 0 {
		m.IntervalMin = DefaultIntervalMin
	}
	if m.Name == "" {
		m.Name = m.Agent + " on " + m.Benchmark
	}
	switch {
	case len(m.Name) > MaxNameLen:
		return fmt.Errorf("%w: name is longer than %d characters", ErrInvalid, MaxNameLen)
	case !agentRe.MatchString(m.Agent):
		return fmt.Errorf("%w: pick an agent", ErrInvalid)
	case !benchRe.MatchString(m.Benchmark):
		return fmt.Errorf("%w: pick a benchmark with ready tasks", ErrInvalid)
	case m.Model != "" && !modelRe.MatchString(m.Model):
		return fmt.Errorf("%w: model %q is not a valid model name", ErrInvalid, m.Model)
	case m.Provider != "" && !agentRe.MatchString(m.Provider):
		return fmt.Errorf("%w: provider %q is not a valid provider name", ErrInvalid, m.Provider)
	case m.Version < 0:
		return fmt.Errorf("%w: version must be 1 or more, or 0 for the latest", ErrInvalid)
	case m.Tasks < 1 || m.Tasks > MaxTasks:
		return fmt.Errorf("%w: a check runs 1 to %d tasks", ErrInvalid, MaxTasks)
	case !slices.Contains(Intervals, m.IntervalMin):
		return fmt.Errorf("%w: interval_min must be one of %s minutes (at least %d)", ErrInvalid, intList(Intervals), MinIntervalMin)
	case math.IsNaN(m.BudgetUSDPerDay) || math.IsInf(m.BudgetUSDPerDay, 0) || m.BudgetUSDPerDay <= 0 || m.BudgetUSDPerDay > MaxBudgetUSDPerDay:
		return fmt.Errorf("%w: budget_usd_per_day must be above 0 and at most %.0f USD", ErrInvalid, MaxBudgetUSDPerDay)
	case m.CheckBudgetUSD() < 0.01:
		return fmt.Errorf("%w: a daily budget of %.2f USD over %d checks a day leaves less than one cent per check: raise the budget or check less often",
			ErrInvalid, m.BudgetUSDPerDay, m.ChecksPerDay())
	case m.Worker != "" && !agentRe.MatchString(m.Worker):
		return fmt.Errorf("%w: worker %q is not a valid worker name", ErrInvalid, m.Worker)
	}
	return m.Rule.Validate()
}

func intList(v []int) string {
	s := make([]string, len(v))
	for i, n := range v {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ", ")
}

// Sample statuses.
const (
	SampleOK      = "ok"      // runs scored; counts for quality and latency
	SampleInfra   = "infra"   // the endpoint did not answer or most runs failed for infrastructure reasons
	SampleError   = "error"   // the check could not start or finished with no run
	SampleSkipped = "skipped" // the check was refused (daily budget) or cancelled; ignored by every rule
)

// RunSample is one run of a check.
type RunSample struct {
	RunID     string   `json:"run_id"`
	Task      string   `json:"task"`
	Outcome   string   `json:"outcome"`
	Pass      *bool    `json:"pass,omitempty"` // nil when the run did not score
	Infra     bool     `json:"infra,omitempty"`
	LatencyMS *int64   `json:"latency_ms,omitempty"`
	CostUSD   *float64 `json:"cost_usd,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// Sample is one check of a monitor.
type Sample struct {
	At           time.Time   `json:"at"`
	EvaluationID string      `json:"evaluation_id,omitempty"`
	ExperimentID string      `json:"experiment_id,omitempty"`
	Status       string      `json:"status"`
	Reason       string      `json:"reason,omitempty"`
	Runs         []RunSample `json:"runs"`
	Passed       int         `json:"passed"`
	Scored       int         `json:"scored"`
	Errors       int         `json:"errors"` // runs that ended in an infrastructure error, or 1 for a check that could not run
	P50MS        *int64      `json:"p50_ms,omitempty"`
	P95MS        *int64      `json:"p95_ms,omitempty"`
	CostUSD      float64     `json:"cost_usd"`
	CostUnknown  int         `json:"cost_unknown,omitempty"` // runs whose cost was not measured
	// Reachable is what a direct reachability check of the endpoint said
	// when the check had failures; nil when it was not checked.
	Reachable *bool `json:"reachable,omitempty"`
	// Incident is the quality or latency incident open while this check
	// ran; such checks never enter a later baseline.
	Incident string `json:"incident,omitempty"`
}

// PassRate is passed / scored, or nil with no scored run.
func (s Sample) PassRate() *float64 {
	if s.Scored == 0 {
		return nil
	}
	v := float64(s.Passed) / float64(s.Scored)
	return &v
}

// Summarize fills the counts, percentiles and cost of s from its runs, and
// picks its status: error with no run, infra when the endpoint was
// unreachable or at least half the runs failed for infrastructure reasons,
// else ok. A status already set to skipped or error stays.
func (s *Sample) Summarize() {
	s.Passed, s.Scored, s.CostUSD, s.CostUnknown = 0, 0, 0, 0
	if s.Status != SampleError {
		s.Errors = 0
	}
	var lat []int64
	infra := 0
	for _, r := range s.Runs {
		if r.Pass != nil {
			s.Scored++
			if *r.Pass {
				s.Passed++
			}
		}
		if r.Infra {
			infra++
		}
		if r.LatencyMS != nil && !r.Infra {
			lat = append(lat, *r.LatencyMS)
		}
		if r.CostUSD != nil {
			s.CostUSD += *r.CostUSD
		} else {
			s.CostUnknown++
		}
	}
	if s.Status != SampleError {
		s.Errors = infra
	}
	s.CostUSD = math.Round(s.CostUSD*1e6) / 1e6
	s.P50MS, s.P95MS = nil, nil
	if len(lat) > 0 {
		p50, p95 := Percentile(lat, 50), Percentile(lat, 95)
		s.P50MS, s.P95MS = &p50, &p95
	}
	switch {
	case s.Status == SampleSkipped || s.Status == SampleError:
	case len(s.Runs) == 0:
		s.Status = SampleError
		s.Errors = 1
		if s.Reason == "" {
			s.Reason = "the check finished with no run"
		}
	case s.Reachable != nil && !*s.Reachable:
		s.Status = SampleInfra
	case infra*2 >= len(s.Runs):
		s.Status = SampleInfra
	default:
		s.Status = SampleOK
	}
}

// Percentile is the nearest-rank percentile p (0 to 100) of v.
func Percentile(v []int64, p float64) int64 {
	if len(v) == 0 {
		return 0
	}
	s := slices.Clone(v)
	slices.Sort(s)
	rank := int(math.Ceil(p / 100 * float64(len(s))))
	return s[min(max(rank, 1), len(s))-1]
}

// infraPatterns are error texts that mean the agent's endpoint or the
// infrastructure, not the agent's answer, failed.
var infraPatterns = []string{
	"connection refused", "connection reset", "no such host", "i/o timeout", "dial tcp", "tls handshake",
	"server misbehaving", "network is unreachable", "no route to host", "eof", "502 bad gateway",
	"503 service unavailable", "504 gateway timeout", "status 502", "status 503", "status 504",
	"unreachable", "could not reach", "name resolution", "temporary failure in name",
}

// InfraError reports whether an error text names an endpoint or
// infrastructure failure.
func InfraError(msg string) bool {
	m := strings.ToLower(msg)
	if m == "" {
		return false
	}
	for _, p := range infraPatterns {
		if strings.Contains(m, p) {
			return true
		}
	}
	return false
}
