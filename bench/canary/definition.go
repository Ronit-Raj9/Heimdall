// Package canary is the drift canary (docs/drift-canary.md): a small,
// pinned suite re-run on a schedule through the Bifrost gateway and compared
// with a known-good baseline, so an agent that gets worse with no change by
// its team (a provider updating a model behind an alias, a route degrading)
// is caught and the alert says what moved.
//
// The package holds the definition file, the comparison statistics, the
// state machine with its confirmation rule, the "what changed" evidence, the
// provider equivalence report and the state files. Planning and running an
// experiment are behind the Executor interface, which cmd/bifrost-bench
// implements with the engine, so everything here is tested with fakes.
package canary

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/maximhq/bifrost/bench/alerts"
	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// DefaultDir is the folder of canary definitions under the project root.
const DefaultDir = "canaries"

// Sensitivity levels. Each sets the per-look significance level of the
// Holm family (see Alpha); docs/drift-canary.md gives the simulated false
// alarm rate of each.
const (
	SensitivityLow    = "low"
	SensitivityNormal = "normal"
	SensitivityHigh   = "high"
)

// Sensitivities lists the levels in order.
var Sensitivities = []string{SensitivityLow, SensitivityNormal, SensitivityHigh}

// Default noise floors: the smallest change of a metric that may flag, so
// provider nondeterminism (latency and token counts move a little on every
// identical request) is never an alarm by itself. Success is absolute (a
// share of tasks), the others are relative to the baseline.
const (
	DefaultFloorSuccess = 0.0
	DefaultFloorCost    = 0.20
	DefaultFloorTokens  = 0.10
	DefaultFloorLatency = 0.25
)

// DefaultMaxBaselineAgeDays is how old a baseline may get before the state
// reads baseline_stale (runs still compare).
const DefaultMaxBaselineAgeDays = 30

// namePattern keeps a canary name a plain token: it names a folder, a lock
// file and a schedule.
var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,47}$`)

// Definition is canaries/<name>.yaml: what to run, what to watch, when, and
// how much it may spend.
type Definition struct {
	Name        string               `json:"name" yaml:"name"`
	Description string               `json:"description,omitempty" yaml:"description,omitempty"`
	Agent       string               `json:"agent" yaml:"agent"`                   // agent config, relative to the project root
	Suite       string               `json:"suite" yaml:"suite"`                   // id@version
	Tasks       schemas.TaskSelector `json:"tasks" yaml:"tasks"`                   // a small pinned list, or all (of the tier when one is named)
	Tier        string               `json:"tier,omitempty" yaml:"tier,omitempty"` // suite tier whose tasks `all` means
	Trials      int                  `json:"trials" yaml:"trials"`
	Conditions  []string             `json:"conditions,omitempty" yaml:"conditions,omitempty"` // default [clean]
	Targets     []Target             `json:"targets" yaml:"targets"`
	Schedule    *ScheduleSpec        `json:"schedule,omitempty" yaml:"schedule,omitempty"`
	Budget      Budget               `json:"budget" yaml:"budget"`
	Alerts      []alerts.Config      `json:"alerts,omitempty" yaml:"alerts,omitempty"`
	Sensitivity string               `json:"sensitivity,omitempty" yaml:"sensitivity,omitempty"`
	NoiseFloor  *NoiseFloor          `json:"noise_floor,omitempty" yaml:"noise_floor,omitempty"`
	// MaxBaselineAgeDays makes the state baseline_stale once the baseline is
	// older; 0 is DefaultMaxBaselineAgeDays.
	MaxBaselineAgeDays int `json:"max_baseline_age_days,omitempty" yaml:"max_baseline_age_days,omitempty"`
	// Smoke marks a canary that runs scripted parts at zero cost (the toy
	// agent against the fault proxy's scripted model). Its results are never
	// findings; caps are optional.
	Smoke       bool  `json:"smoke,omitempty" yaml:"smoke,omitempty"`
	Seed        int64 `json:"seed,omitempty" yaml:"seed,omitempty"`
	Concurrency int   `json:"concurrency,omitempty" yaml:"concurrency,omitempty"`
}

// Target is one route to watch: a provider and a model alias. Two targets
// with the same model through different providers make an equivalence
// check.
type Target struct {
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"` // empty keeps the agent config's provider
	Model    string `json:"model" yaml:"model"`
}

// Label names a target in reports: "provider/model", or the model alone.
func (t Target) Label() string {
	if t.Provider == "" {
		return t.Model
	}
	return t.Provider + "/" + t.Model
}

// ScheduleSpec is when the daemon runs the canary.
type ScheduleSpec struct {
	Cron     string `json:"cron" yaml:"cron"`                             // 5-field cron expression
	Timezone string `json:"timezone,omitempty" yaml:"timezone,omitempty"` // IANA name; empty is the host's zone
}

// Budget caps spend. Both are required for a canary that can spend.
type Budget struct {
	PerRunUSD float64 `json:"per_run_usd" yaml:"per_run_usd"` // the frozen experiment's spend cap
	PerDayUSD float64 `json:"per_day_usd" yaml:"per_day_usd"` // every run of the canary on one day, confirmations included
}

// NoiseFloor overrides the default floors. Zero keeps a default.
type NoiseFloor struct {
	Success   float64 `json:"success,omitempty" yaml:"success,omitempty"`       // absolute share of tasks, 0..1
	CostUSD   float64 `json:"cost_usd,omitempty" yaml:"cost_usd,omitempty"`     // relative
	Tokens    float64 `json:"tokens,omitempty" yaml:"tokens,omitempty"`         // relative
	LatencyMS float64 `json:"latency_ms,omitempty" yaml:"latency_ms,omitempty"` // relative
}

// Floor returns the noise floor of a metric.
func (d *Definition) Floor(metric string) float64 {
	f := NoiseFloor{}
	if d.NoiseFloor != nil {
		f = *d.NoiseFloor
	}
	pick := func(v, def float64) float64 {
		if v > 0 {
			return v
		}
		return def
	}
	switch metric {
	case MetricSuccess:
		return pick(f.Success, DefaultFloorSuccess)
	case MetricCost:
		return pick(f.CostUSD, DefaultFloorCost)
	case MetricTokens:
		return pick(f.Tokens, DefaultFloorTokens)
	case MetricLatency:
		return pick(f.LatencyMS, DefaultFloorLatency)
	}
	return 0
}

// Alpha is the per-look significance level of the Holm family for the
// canary's sensitivity.
func (d *Definition) Alpha() float64 { return AlphaFor(d.Sensitivity) }

// AlphaFor maps a sensitivity to its per-look level. The confirmation rule
// (two consecutive flags on the same target and metric) is what keeps the
// false-alarm rate low; docs/drift-canary.md has the simulated rates.
func AlphaFor(sensitivity string) float64 {
	switch sensitivity {
	case SensitivityLow:
		return 0.05
	case SensitivityHigh:
		return 0.20
	}
	return 0.10
}

// SensitivityOr returns the sensitivity, normal when empty.
func (d *Definition) SensitivityOr() string {
	if d.Sensitivity == "" {
		return SensitivityNormal
	}
	return d.Sensitivity
}

// WatchedConditions returns the conditions, [clean] when none is listed.
func (d *Definition) WatchedConditions() []string {
	if len(d.Conditions) == 0 {
		return []string{schemas.ConditionClean}
	}
	return d.Conditions
}

// MaxBaselineAge returns how old a baseline may get.
func (d *Definition) MaxBaselineAge() time.Duration {
	days := d.MaxBaselineAgeDays
	if days <= 0 {
		days = DefaultMaxBaselineAgeDays
	}
	return time.Duration(days) * 24 * time.Hour
}

// ScheduleName is the name of the canary's entry in the schedule daemon.
func (d *Definition) ScheduleName() string { return "canary-" + d.Name }

// Validate checks a definition without touching the project: names, the
// task selection, targets, the schedule and the caps. A canary that can
// spend (not smoke) needs both caps, and the per-run cap must fit the day.
func (d *Definition) Validate() error {
	var errs []error
	if !namePattern.MatchString(d.Name) {
		errs = append(errs, fmt.Errorf("name %q must be lowercase letters, digits, - or _, starting with a letter or digit, at most 48 characters", d.Name))
	}
	if d.Agent == "" {
		errs = append(errs, errors.New("agent is empty"))
	}
	if d.Suite == "" {
		errs = append(errs, errors.New("suite is empty"))
	}
	if !d.Tasks.All && len(d.Tasks.IDs) == 0 {
		errs = append(errs, errors.New("tasks is empty: list task ids or say all"))
	}
	if d.Tier != "" && !d.Tasks.All {
		errs = append(errs, errors.New("tier needs tasks: all"))
	}
	if d.Trials < 1 {
		errs = append(errs, fmt.Errorf("trials must be >= 1, got %d", d.Trials))
	}
	seen := map[string]bool{}
	for _, c := range d.WatchedConditions() {
		if c == "" || seen[c] {
			errs = append(errs, fmt.Errorf("condition %q is empty or listed twice", c))
		}
		seen[c] = true
	}
	if len(d.Targets) == 0 {
		errs = append(errs, errors.New("targets is empty: name at least one {provider, model}"))
	}
	if len(d.Targets) > len(armIDs) {
		errs = append(errs, fmt.Errorf("at most %d targets, got %d", len(armIDs), len(d.Targets)))
	}
	tseen := map[string]bool{}
	for i, t := range d.Targets {
		if t.Model == "" {
			errs = append(errs, fmt.Errorf("target %d has no model", i+1))
		}
		if tseen[t.Label()] {
			errs = append(errs, fmt.Errorf("target %s is listed twice", t.Label()))
		}
		tseen[t.Label()] = true
	}
	if d.Schedule != nil {
		if _, err := schedule.ParseCron(d.Schedule.Cron); err != nil {
			errs = append(errs, fmt.Errorf("schedule: %w", err))
		}
		if d.Schedule.Timezone != "" {
			if _, err := time.LoadLocation(d.Schedule.Timezone); err != nil {
				errs = append(errs, fmt.Errorf("schedule timezone %q: %w", d.Schedule.Timezone, err))
			}
		}
	}
	if d.Budget.PerRunUSD < 0 || d.Budget.PerDayUSD < 0 {
		errs = append(errs, errors.New("budget caps must not be negative"))
	}
	if !d.Smoke {
		if d.Budget.PerRunUSD <= 0 || d.Budget.PerDayUSD <= 0 {
			errs = append(errs, errors.New("budget.per_run_usd and budget.per_day_usd are required for a canary that can spend (only a smoke canary may leave them out)"))
		} else if d.Budget.PerRunUSD > d.Budget.PerDayUSD {
			errs = append(errs, fmt.Errorf("budget.per_run_usd %.2f is above budget.per_day_usd %.2f, so no run could ever start", d.Budget.PerRunUSD, d.Budget.PerDayUSD))
		}
	}
	if d.Sensitivity != "" && !slices.Contains(Sensitivities, d.Sensitivity) {
		errs = append(errs, fmt.Errorf("sensitivity %q is not one of %s", d.Sensitivity, strings.Join(Sensitivities, ", ")))
	}
	if f := d.NoiseFloor; f != nil && (f.Success < 0 || f.Success > 1 || f.CostUSD < 0 || f.Tokens < 0 || f.LatencyMS < 0) {
		errs = append(errs, errors.New("noise_floor values must be >= 0 (success at most 1)"))
	}
	if d.MaxBaselineAgeDays < 0 {
		errs = append(errs, errors.New("max_baseline_age_days must not be negative"))
	}
	if d.Concurrency < 0 {
		errs = append(errs, errors.New("concurrency must not be negative"))
	}
	for _, a := range d.Alerts {
		if err := a.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("canary %s: %w", d.Name, err)
	}
	return nil
}

// armIDs are the arm ids targets take, in order.
var armIDs = []string{"A", "B", "C", "D", "E", "F"}

// ArmOf returns the arm id of target i.
func ArmOf(i int) string { return armIDs[i] }

// TargetOf returns the target of an arm id, and whether it exists.
func (d *Definition) TargetOf(arm string) (Target, bool) {
	i := slices.Index(armIDs, arm)
	if i < 0 || i >= len(d.Targets) {
		return Target{}, false
	}
	return d.Targets[i], true
}

// ArmLabel names an arm by its target.
func (d *Definition) ArmLabel(arm string) string {
	if t, ok := d.TargetOf(arm); ok {
		return t.Label()
	}
	return arm
}

// Experiment builds the frozen experiment of one canary run: one arm per
// target over the same agent config, the watched conditions on the pinned
// tasks, the per-run cap as the experiment's spend cap. tasks are the
// resolved task ids (a tier already expanded). inject, when not empty, is a
// probe that replaces every watched condition: a test hook that forces a
// drift on a smoke canary (the caller refuses it on any other).
func (d *Definition) Experiment(tasks []string, inject string) *schemas.Experiment {
	sel := d.Tasks
	if tasks != nil {
		sel = schemas.TaskSelector{IDs: slices.Clone(tasks)}
	}
	e := &schemas.Experiment{
		Name:         "canary-" + d.Name,
		Suite:        d.Suite,
		Base:         d.Agent,
		Arms:         map[string]schemas.ArmSpec{},
		Budget:       schemas.BudgetSpec{TotalUSD: d.Budget.PerRunUSD},
		Verdict:      schemas.VerdictSpec{Policy: "paired_signflip", Alpha: 0.05, AssumedSD: 0.30},
		Seed:         d.Seed,
		Concurrency:  d.Concurrency,
		ManifestMode: "agent_exposure",
		Smoke:        d.Smoke,
	}
	if e.Seed == 0 {
		e.Seed = 1
	}
	for i, t := range d.Targets {
		set := map[string]any{"model": t.Model}
		if t.Provider != "" {
			set["gateway.provider"] = t.Provider
		}
		e.Arms[ArmOf(i)] = schemas.ArmSpec{Label: t.Label(), Set: set}
	}
	conds := d.WatchedConditions()
	if inject != "" {
		// The probe stands in for the first watched condition only; Rename
		// maps it back so the run compares with the baseline.
		conds = []string{inject}
	}
	for _, c := range conds {
		e.Conditions = append(e.Conditions, schemas.ConditionSpec{ID: c, Tasks: sel, Trials: d.Trials})
	}
	e.Primary = schemas.Endpoint{Condition: e.Conditions[0].ID, Metric: MetricSuccess}
	return e
}

// Rename maps the conditions of an injected run back to the watched ones,
// so it compares with the baseline; nil without injection.
func (d *Definition) Rename(inject string) map[string]string {
	if inject == "" {
		return nil
	}
	return map[string]string{inject: d.WatchedConditions()[0]}
}

// Load reads a definition strictly (an unknown key is an error, so a
// misspelled per_day_usd never passes as no cap) and validates it. The file
// name must be <name>.yaml.
func Load(path string) (*Definition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read canary %s: %w", path, err)
	}
	d := &Definition{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(d); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse canary %s: %w", path, err)
	}
	if want := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)); d.Name != want {
		return nil, fmt.Errorf("canary %s: name %q must match the file name %q", path, d.Name, want)
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return d, nil
}

// Save writes a definition atomically.
func Save(path string, d *Definition) error {
	if err := d.Validate(); err != nil {
		return err
	}
	data, err := yaml.Marshal(d)
	if err != nil {
		return fmt.Errorf("encode canary: %w", err)
	}
	header := []byte("# Drift canary (docs/drift-canary.md). Alerts name environment variables only;\n# never put a webhook URL or a secret in this file.\n")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, append(header, data...))
}

// LoadAll reads every canaries/*.yaml under dir. A file that does not load
// is reported in errs and left out; a missing folder is no canaries.
func LoadAll(dir string) (defs []*Definition, errs map[string]string) {
	errs = map[string]string{}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errs
	}
	if err != nil {
		errs[dir] = err.Error()
		return nil, errs
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		d, err := Load(filepath.Join(dir, e.Name()))
		if err != nil {
			errs[strings.TrimSuffix(e.Name(), ".yaml")] = err.Error()
			continue
		}
		defs = append(defs, d)
	}
	slices.SortFunc(defs, func(a, b *Definition) int { return strings.Compare(a.Name, b.Name) })
	return defs, errs
}

// Path returns canaries/<name>.yaml under the project root.
func Path(root, name string) string { return filepath.Join(root, DefaultDir, name+".yaml") }

// ValidName reports whether name can be a canary name.
func ValidName(name string) bool { return namePattern.MatchString(name) }

// writeAtomic writes data through a temporary file and a rename.
func writeAtomic(p string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(p), ".canary-*.tmp")
	if err != nil {
		return err
	}
	_, werr := tmp.Write(data)
	if werr == nil {
		werr = tmp.Sync()
	}
	if err := errors.Join(werr, tmp.Close()); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return errors.Join(err, os.Remove(tmp.Name()))
	}
	return nil
}
