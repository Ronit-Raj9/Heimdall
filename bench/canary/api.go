package canary

import (
	"slices"
	"time"

	"github.com/maximhq/bifrost/bench/schedule"
	"github.com/maximhq/bifrost/bench/trends"
)

// recordsShown caps the records a detail response carries, newest first.
const recordsShown = 60

// Summary is one canary in GET /api/bench/canaries and `canary status`.
type Summary struct {
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	State         string    `json:"state"`
	Reason        string    `json:"reason"`
	Suite         string    `json:"suite"`
	Agent         string    `json:"agent"`
	Targets       []Target  `json:"targets"`
	Tasks         int       `json:"tasks"` // pinned task ids; 0 when the definition says all
	Trials        int       `json:"trials"`
	Cron          string    `json:"cron"`        // "" when not scheduled
	Next          string    `json:"next"`        // RFC3339 next scheduled look, "" when none
	Sensitivity   string    `json:"sensitivity"` // low | normal | high
	Alpha         float64   `json:"alpha"`       // per-look Holm family level
	Smoke         bool      `json:"smoke"`       // scripted, zero cost, never a finding
	PerRunUSD     float64   `json:"per_run_usd"` // 0 when not capped (smoke only)
	PerDayUSD     float64   `json:"per_day_usd"` // 0 when not capped (smoke only)
	SpentTodayUSD float64   `json:"spent_today_usd"`
	Baseline      *Baseline `json:"baseline"`  // current baseline; null before the first
	LastLook      *Record   `json:"last_look"` // newest record, without its run detail; null before the first
	Looks         int       `json:"looks"`     // records since the current baseline
	Paused        bool      `json:"paused,omitempty"`
}

// List is GET /api/bench/canaries.
type List struct {
	Canaries []Summary        `json:"canaries"`
	Errors   []DefinitionLoad `json:"errors"` // definitions that did not load
	States   []string         `json:"states"`
}

// DefinitionLoad names a definition file that did not load.
type DefinitionLoad struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

// Detail is GET /api/bench/canaries/{name}.
type Detail struct {
	Summary    Summary           `json:"summary"`
	Definition Definition        `json:"definition"`
	Baselines  []Baseline        `json:"baselines"` // every pinned baseline, newest first
	Records    []Record          `json:"records"`   // newest first, at most 60
	History    []trends.Lineage  `json:"history"`   // one lineage per target, for the trends chart
	Pairs      []EquivalencePair `json:"pairs"`     // targets that watch one model through two providers
}

// EquivalencePair names a provider pair of the definition.
type EquivalencePair struct {
	Model  string `json:"model"`
	ArmA   string `json:"arm_a"`
	ArmB   string `json:"arm_b"`
	LabelA string `json:"label_a"`
	LabelB string `json:"label_b"`
}

// RunAccepted is POST /api/bench/canaries/{name}/run.
type RunAccepted struct {
	Canary  string    `json:"canary"`
	Started time.Time `json:"started"`
	Message string    `json:"message"`
}

// Summarize builds a canary's summary from its state files.
func Summarize(root string, d *Definition, now time.Time) (Summary, []Record, []Baseline, error) {
	s := Summary{Name: d.Name, Description: d.Description, Suite: d.Suite, Agent: d.Agent, Targets: slices.Clone(d.Targets),
		Trials: d.Trials, Sensitivity: d.SensitivityOr(), Alpha: d.Alpha(), Smoke: d.Smoke,
		PerRunUSD: d.Budget.PerRunUSD, PerDayUSD: d.Budget.PerDayUSD, State: StateNoBaseline,
		Reason: "No baseline yet. Press Record baseline to measure the reference result."}
	if !d.Tasks.All {
		s.Tasks = len(d.Tasks.IDs)
	}
	if d.Schedule != nil {
		s.Cron = d.Schedule.Cron
		s.Paused = d.Schedule.Paused
		if c, err := schedule.ParseCron(d.Schedule.Cron); err == nil && !d.Schedule.Paused {
			if next := c.Next(now.In(d.Location())); !next.IsZero() {
				s.Next = next.Format(time.RFC3339)
			}
		}
	}
	records, err := ReadRecords(root, d.Name)
	if err != nil {
		return s, nil, nil, err
	}
	baselines, err := ReadBaselines(root, d.Name)
	if err != nil {
		return s, nil, nil, err
	}
	loc := d.Location()
	s.SpentTodayUSD = SpentOn(records, now.In(loc).Format(time.DateOnly), loc)
	if len(baselines) > 0 {
		b := baselines[len(baselines)-1]
		s.Baseline = &b
		for _, r := range records {
			if r.Baseline == b.ExpID && r.Kind != KindBaseline {
				s.Looks++
			}
		}
	}
	if len(records) > 0 {
		last := lightRecord(records[len(records)-1])
		s.LastLook = &last
		s.State, s.Reason = last.State, last.Reason
	}
	return s, records, baselines, nil
}

// BuildList summarizes every definition.
func BuildList(root string, defs []*Definition, loadErrs map[string]string, now time.Time) (*List, error) {
	l := &List{Canaries: []Summary{}, Errors: []DefinitionLoad{}, States: slices.Clone(States)}
	for _, d := range defs {
		s, _, _, err := Summarize(root, d, now)
		if err != nil {
			return nil, err
		}
		l.Canaries = append(l.Canaries, s)
	}
	for _, name := range sortedKeys(loadErrs) {
		l.Errors = append(l.Errors, DefinitionLoad{Name: name, Error: loadErrs[name]})
	}
	return l, nil
}

// BuildDetail is one canary with its records, baselines and history.
func BuildDetail(root string, d *Definition, now time.Time) (*Detail, error) {
	s, records, baselines, err := Summarize(root, d, now)
	if err != nil {
		return nil, err
	}
	det := &Detail{Summary: s, Definition: *d, Baselines: []Baseline{}, Records: []Record{}, History: History(d, records), Pairs: []EquivalencePair{}}
	for i := len(baselines) - 1; i >= 0; i-- {
		det.Baselines = append(det.Baselines, baselines[i])
	}
	for i := len(records) - 1; i >= 0 && len(det.Records) < recordsShown; i-- {
		det.Records = append(det.Records, records[i])
	}
	for _, p := range EquivalencePairs(d) {
		det.Pairs = append(det.Pairs, EquivalencePair{Model: bareModel(d.Targets[p[0]].Model), ArmA: ArmOf(p[0]), ArmB: ArmOf(p[1]),
			LabelA: d.Targets[p[0]].Label(), LabelB: d.Targets[p[1]].Label()})
	}
	return det, nil
}

// History turns the records that ran an experiment into trends lineages,
// one per target, so the UI draws them with the trends chart: the value is
// success, the regression status of a point is the look's verdict on that
// target (first for a baseline, regressed for a flagged test on success or
// any metric of the target, no_change for a clear look), and the release
// flag marks looks whose canary state became drifted.
func History(d *Definition, records []Record) []trends.Lineage {
	byArm := map[string]*trends.Lineage{}
	order := []string{}
	for _, r := range records {
		if r.ExpID == "" || len(r.Points) == 0 {
			continue
		}
		for _, p := range r.Points {
			l := byArm[p.Arm]
			if l == nil {
				l = &trends.Lineage{Key: d.Name + "|" + p.Arm, Agent: d.Name, Model: d.ArmLabel(p.Arm), Synthetic: d.Smoke, Points: []trends.Point{}}
				byArm[p.Arm] = l
				order = append(order, p.Arm)
			}
			pt := trends.Point{Experiment: r.ExpID, Date: r.Started, Suite: d.Suite, Arm: p.Arm, ConfigHash: short(r.Fingerprint),
				Synthetic: r.Smoke || r.Injected != "", Tasks: p.Tasks, Runs: p.Runs, Value: p.Success, Success: p.Success, Decision: r.State}
			pt.ConfigChanged = len(l.Points) > 0 && l.Points[len(l.Points)-1].ConfigHash != pt.ConfigHash
			pt.Regression = regressionOf(r, p.Arm)
			if r.State == StateDrifted {
				pt.Release = trends.ReleaseRegressed
			}
			l.Points = append(l.Points, pt)
		}
	}
	slices.Sort(order)
	out := make([]trends.Lineage, 0, len(order))
	for _, arm := range order {
		l := byArm[arm]
		l.Regression = l.Points[len(l.Points)-1].Regression
		out = append(out, *l)
	}
	return out
}

// regressionOf is a record's verdict on one target as a trends regression.
func regressionOf(r Record, arm string) trends.Regression {
	reg := trends.Regression{Against: r.Baseline, Reason: r.Reason}
	switch {
	case r.Kind == KindBaseline:
		reg.Status, reg.Against, reg.Reason = trends.StatusFirst, "", "baseline"
		return reg
	case r.Comparison == nil:
		reg.Status = trends.StatusCannotTell
		return reg
	}
	reg.Status = trends.StatusNoChange
	for _, t := range r.Comparison.Tests {
		if t.Arm != arm || t.Metric != MetricSuccess {
			if t.Arm == arm && t.Status == TestWorse {
				reg.Status = trends.StatusRegressed
			}
			continue
		}
		reg.P, reg.Delta, reg.CommonTasks, reg.Discordant, reg.Reason = t.P, t.Delta, t.Tasks, t.Discordant, t.Reason
		switch t.Status {
		case TestWorse:
			reg.Status = trends.StatusRegressed
		case TestBetter:
			if reg.Status != trends.StatusRegressed {
				reg.Status = trends.StatusImproved
			}
		case TestCannotTell:
			if reg.Status != trends.StatusRegressed {
				reg.Status = trends.StatusCannotTell
			}
		}
	}
	return reg
}

// lightRecord drops a record's bulky parts for a list row.
func lightRecord(r Record) Record {
	r.Comparison, r.Changes, r.Equivalence = nil, nil, nil
	return r
}
