// Package improve runs improvement runs: an agent improves its own prompt,
// notes and skills over N rounds, and every round is measured honestly.
//
// One round: the improver phase (a sandboxed run of the incumbent where
// only the agent bundle is writable, with dev evidence and no verifier,
// audit or sealed content, under a per-round spend cap) writes a candidate
// bundle; the candidate is frozen as an agent version (parent, author
// agent, round, bundle hash) on a hash-chained lineage; integrity gates
// read the bundle diff and pause the loop on a tripwire; the candidate is
// screened on the dev split, then committed only when an anytime-valid
// e-value test on the audit split says it is better; the incumbent is
// scored on the sealed split every round, and only the report reads those
// scores. Controls (version 0 given the loop's spend, and a null improver
// that edits without feedback) say what the gain is measured against.
//
// Execution goes through the Executor interface; cmd/bifrost-bench
// implements it with the engine, tests with scripted agents.
package improve

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/utils"
	"gopkg.in/yaml.v3"
)

// Kind is the kind of an improvement spec file.
const Kind = "improvement"

// Defaults and limits of a spec.
const (
	DefaultRounds    = 3
	MaxRounds        = 20
	DefaultBudgetUSD = 2.0
	DefaultWallS     = 1800
	DefaultAlpha     = 0.05
	DefaultMinPairs  = 4
	DefaultMaxPairs  = 60
	MaxTrials        = 10
	// TripSpendShare is the share of the round's improver cap that trips
	// the loop: an improver that spends near its cap is pushing its budget.
	TripSpendShare = 0.9
)

// Control arm ids (Spec.Controls).
const (
	ControlBudgetMatched = "budget_matched_v0"
	ControlNullImprover  = "null_improver"
)

// DefaultFrozen are the agent config fields the improver may never change.
var DefaultFrozen = []string{"adapter", "image", "build", "argv", "env", "model", "budgets", "gateway", "capabilities", "limits", "remote", "a2a", "openai", "team", "scripted"}

// Spec is an improvement run definition, frozen and hashed at creation.
type Spec struct {
	Kind     string       `json:"kind" yaml:"kind"`
	Name     string       `json:"name" yaml:"name"`
	Agent    string       `json:"agent" yaml:"agent"` // agents/<id>.yaml or agents/<id>.yaml@v<N>: version 0
	Suite    string       `json:"suite" yaml:"suite"` // benchmark whose tasks are partitioned
	Tasks    []string     `json:"tasks,omitempty" yaml:"tasks"`
	Rounds   int          `json:"rounds" yaml:"rounds"`
	Seed     int64        `json:"seed" yaml:"seed"`
	Editable Editable     `json:"editable" yaml:"editable"`
	Frozen   []string     `json:"frozen" yaml:"frozen"`
	Splits   SplitSpec    `json:"splits" yaml:"splits"`
	Improver ImproverSpec `json:"improver" yaml:"improver"`
	Screen   PhaseSpec    `json:"screen" yaml:"screen"`
	Commit   CommitSpec   `json:"commit" yaml:"commit"`
	Sealed   PhaseSpec    `json:"sealed" yaml:"sealed"`
	Controls []string     `json:"controls" yaml:"controls"`
}

// Editable is the agent bundle the improver may change: container paths in
// the agent config's files map. Files are exact paths; Dirs are prefixes
// (ending in "/") under which the improver may add, change or remove files,
// such as a skills folder.
type Editable struct {
	Files []string `json:"files" yaml:"files"`
	Dirs  []string `json:"dirs,omitempty" yaml:"dirs"`
}

// SplitSpec partitions the task pool: shares of dev (the improver and the
// screen see it), audit (only the commit gate reads it) and sealed (only
// the report reads it). Honeypots are impossible tasks added to every
// audit look; any candidate pass on one blocks the commit.
type SplitSpec struct {
	Dev       float64  `json:"dev" yaml:"dev"`
	Audit     float64  `json:"audit" yaml:"audit"`
	Sealed    float64  `json:"sealed" yaml:"sealed"`
	Honeypots []string `json:"honeypots,omitempty" yaml:"honeypots"`
}

// ImproverSpec is the improver phase: its per-round spend cap (enforced by
// the run's gateway key) and wall clock, and the improver task the engine
// runs it on (suite task id; the executor may supply a default).
type ImproverSpec struct {
	BudgetUSD float64 `json:"budget_usd" yaml:"budget_usd"`
	WallS     int     `json:"wall_s" yaml:"wall_s"`
	Task      string  `json:"task,omitempty" yaml:"task"`
}

// PhaseSpec is how many trials per task a scoring phase runs.
type PhaseSpec struct {
	Trials int `json:"trials" yaml:"trials"`
}

// CommitSpec is the commit gate on the audit split: the family level alpha
// (split over the rounds, so the chance of any false commit over the whole
// loop is at most alpha), pairs before a decision counts, pairs read at
// most, trials per task, and the audit look budget.
type CommitSpec struct {
	Alpha      float64 `json:"alpha" yaml:"alpha"`
	MinPairs   int     `json:"min_pairs" yaml:"min_pairs"`
	MaxPairs   int     `json:"max_pairs" yaml:"max_pairs"`
	Trials     int     `json:"trials" yaml:"trials"`
	AuditLooks int     `json:"audit_looks" yaml:"audit_looks"`
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ParseSpec reads a spec from YAML or JSON strictly, fills defaults and
// validates it.
func ParseSpec(data []byte) (*Spec, error) {
	var s Spec
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("parse improvement spec: %w", err)
	}
	s.Defaults()
	if err := s.Validate(); err != nil {
		return nil, err
	}
	return &s, nil
}

// Defaults fills every unset field.
func (s *Spec) Defaults() {
	if s.Kind == "" {
		s.Kind = Kind
	}
	if s.Rounds == 0 {
		s.Rounds = DefaultRounds
	}
	if s.Seed == 0 {
		s.Seed = 1
	}
	if len(s.Frozen) == 0 {
		s.Frozen = slices.Clone(DefaultFrozen)
	}
	if s.Splits.Dev == 0 && s.Splits.Audit == 0 && s.Splits.Sealed == 0 {
		s.Splits.Dev, s.Splits.Audit, s.Splits.Sealed = 0.4, 0.3, 0.3
	}
	if s.Improver.BudgetUSD == 0 {
		s.Improver.BudgetUSD = DefaultBudgetUSD
	}
	if s.Improver.WallS == 0 {
		s.Improver.WallS = DefaultWallS
	}
	for _, p := range []*int{&s.Screen.Trials, &s.Sealed.Trials, &s.Commit.Trials} {
		if *p == 0 {
			*p = 1
		}
	}
	if s.Commit.Alpha == 0 {
		s.Commit.Alpha = DefaultAlpha
	}
	if s.Commit.MinPairs == 0 {
		s.Commit.MinPairs = DefaultMinPairs
	}
	if s.Commit.MaxPairs == 0 {
		s.Commit.MaxPairs = DefaultMaxPairs
	}
	if s.Commit.AuditLooks == 0 {
		s.Commit.AuditLooks = s.Rounds
	}
	if s.Controls == nil {
		s.Controls = []string{ControlBudgetMatched, ControlNullImprover}
	}
}

// Validate checks a spec after Defaults.
func (s *Spec) Validate() error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	if s.Kind != Kind {
		bad("kind must be %q", Kind)
	}
	if !nameRe.MatchString(s.Name) {
		bad("name %q must be lowercase letters, digits and dashes", s.Name)
	}
	if !strings.HasPrefix(s.Agent, "agents/") {
		bad("agent must be agents/<id>.yaml, optionally pinned @v<N>")
	}
	if s.Suite == "" {
		bad("suite is required")
	}
	if s.Rounds < 1 || s.Rounds > MaxRounds {
		bad("rounds must be 1 to %d", MaxRounds)
	}
	if len(s.Editable.Files)+len(s.Editable.Dirs) == 0 {
		bad("editable needs at least one file or folder")
	}
	for _, f := range s.Editable.Files {
		if !strings.HasPrefix(f, "/") || strings.HasSuffix(f, "/") || strings.Contains(f, "..") {
			bad("editable file %q must be an absolute container path", f)
		}
	}
	for _, d := range s.Editable.Dirs {
		if !strings.HasPrefix(d, "/") || !strings.HasSuffix(d, "/") || strings.Contains(d, "..") || d == "/" {
			bad("editable folder %q must be an absolute container path ending in /", d)
		}
	}
	sp := s.Splits
	if sp.Dev <= 0 || sp.Audit <= 0 || sp.Sealed <= 0 || math.Abs(sp.Dev+sp.Audit+sp.Sealed-1) > 1e-6 {
		bad("splits dev, audit and sealed must be positive and sum to 1")
	}
	if s.Improver.BudgetUSD < 0 || s.Improver.WallS < 0 {
		bad("improver budget_usd and wall_s must not be negative")
	}
	for _, t := range []int{s.Screen.Trials, s.Sealed.Trials, s.Commit.Trials} {
		if t < 1 || t > MaxTrials {
			bad("trials must be 1 to %d", MaxTrials)
		}
	}
	c := s.Commit
	if !(c.Alpha > 0 && c.Alpha < 0.5) {
		bad("commit alpha must be in (0, 0.5)")
	}
	if c.MinPairs < 1 || c.MaxPairs < c.MinPairs || c.AuditLooks < 1 {
		bad("commit needs min_pairs >= 1, max_pairs >= min_pairs and audit_looks >= 1")
	}
	for _, k := range s.Controls {
		if k != ControlBudgetMatched && k != ControlNullImprover {
			bad("unknown control %q (budget_matched_v0, null_improver)", k)
		}
	}
	return errors.Join(errs...)
}

// Hash is the spec's content hash, frozen in the improvement record.
func (s *Spec) Hash() (string, error) { return utils.HashJSON(s) }

// RoundAlpha is the level of one round's commit test: the family level
// split evenly over the rounds (Bonferroni), so the whole loop commits a
// non-improvement with chance at most Commit.Alpha.
func (s *Spec) RoundAlpha() float64 { return s.Commit.Alpha / float64(max(1, s.Rounds)) }

// Allows reports whether a container path is in the bundle.
func (e Editable) Allows(path string) bool {
	if slices.Contains(e.Files, path) {
		return true
	}
	for _, d := range e.Dirs {
		if strings.HasPrefix(path, d) && len(path) > len(d) && !strings.Contains(path, "..") {
			return true
		}
	}
	return false
}
