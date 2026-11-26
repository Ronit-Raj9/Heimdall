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
	"os"
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

// Control arm ids (Spec.Controls). ControlRetries replaces the
// budget-matched band (A2.6); old specs may still name budget_matched_v0.
const (
	ControlBudgetMatched = "budget_matched_v0"
	ControlNullImprover  = "null_improver"
	ControlRetries       = "retries_v0"
)

// The improver task every run uses unless the spec names another, and the
// short form people write for it (stored expanded).
const (
	DefaultImproverTask = "heimdall-improver@1.1.0:reflect-edit"
	ImproverTaskAlias   = "improver@1.1.0:reflect-edit"
)

// InstructionsKey is the bundle key of the agent's standing instructions.
const InstructionsKey = "@instructions"

// EnvToolDescriptions switches on tool descriptions as an editable surface.
const EnvToolDescriptions = "HEIMDALL_IMPROVE_TOOL_DESCRIPTIONS"

// Limits of the new spec fields.
const (
	MaxCandidates     = 8
	MaxRetryAttempts  = 32
	MaxReplicateLoops = 5
	DefaultGateAlpha  = 0.10 // the gate alpha of every preset
)

// Presets (Spec.Preset).
const (
	PresetSmall  = "small"
	PresetMedium = "medium"
	PresetLarge  = "large"
	PresetCustom = "custom"
)

// presetValues are the counts and caps a preset fills into unset fields (A3.6).
type presetValues struct {
	rounds, candidates, keep int
	merge                    bool
	restart                  []int
	screen, gate, sealed     int
	curve                    bool
	improverUSD              float64
	extension                bool
	limitUSD                 float64
}

var presets = map[string]presetValues{
	PresetSmall:  {rounds: 3, candidates: 2, keep: 2, screen: 1, gate: 2, sealed: 2, improverUSD: 0.30, extension: true, limitUSD: 15},
	PresetMedium: {rounds: 5, candidates: 4, keep: 4, merge: true, restart: []int{3, 5}, screen: 1, gate: 3, sealed: 2, curve: true, improverUSD: 0.60, extension: true, limitUSD: 60},
	PresetLarge:  {rounds: 8, candidates: 8, keep: 8, merge: true, restart: []int{3, 6}, screen: 2, gate: 3, sealed: 3, curve: true, improverUSD: 1.50, limitUSD: 400},
}

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

	// Preset is small, medium, large or custom; it fills unset fields (A3.6).
	Preset       string          `json:"preset,omitempty" yaml:"preset"`
	Surfaces     Surfaces        `json:"surfaces" yaml:"surfaces"`
	Claim        ClaimSpec       `json:"claim" yaml:"claim"`
	Retries      RetriesSpec     `json:"retries" yaml:"retries"`
	Budget       BudgetSpec      `json:"budget" yaml:"budget"`
	Replicate    ReplicationPlan `json:"replicate" yaml:"replicate"`
	BundleSource *BundleSource   `json:"bundle_source,omitempty" yaml:"bundle_source"`
}

// Surfaces are editable surfaces beyond files and instructions. Tool
// descriptions are designed but switched off unless the server sets
// EnvToolDescriptions (A3.8).
type Surfaces struct {
	ToolDescriptions bool `json:"tool_descriptions,omitempty" yaml:"tool_descriptions"`
}

// ClaimSpec is the claim on the held-out tasks (A2.5): its alpha, the part
// of it reserved for one second look when Extension is on, and the gain the
// run is sized to confirm.
type ClaimSpec struct {
	Alpha          float64 `json:"alpha" yaml:"alpha"`
	ExtensionAlpha float64 `json:"extension_alpha" yaml:"extension_alpha"`
	TargetGain     float64 `json:"target_gain" yaml:"target_gain"`
	Extension      bool    `json:"extension" yaml:"extension"`
}

// RetriesSpec caps the retries control (A2.6).
type RetriesSpec struct {
	MaxAttempts int `json:"max_attempts" yaml:"max_attempts"`
}

// BudgetSpec is the whole run's hard stop in dollars; 0 means none (A2.10).
type BudgetSpec struct {
	TotalUSD float64 `json:"total_usd" yaml:"total_usd"`
}

// BundleSource names where version 0's bundle comes from (A4.4, designed
// for a later wave).
type BundleSource struct {
	Kind     string   `json:"kind" yaml:"kind"` // bifrost_prompt | bifrost_skills
	PromptID string   `json:"prompt_id,omitempty" yaml:"prompt_id"`
	Skills   []string `json:"skills,omitempty" yaml:"skills"`
	Publish  string   `json:"publish,omitempty" yaml:"publish"`
}

// Editable is the agent bundle the improver may change: container paths in
// the agent config's files map. Files are exact paths; Dirs are prefixes
// (ending in "/") under which the improver may add, change or remove files,
// such as a skills folder.
type Editable struct {
	Files []string `json:"files" yaml:"files"`
	Dirs  []string `json:"dirs,omitempty" yaml:"dirs"`
	// Instructions makes the agent's standing instructions editable, as
	// the bundle key InstructionsKey.
	Instructions bool `json:"instructions,omitempty" yaml:"instructions"`
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
	// Agent is "" or reflector (the shipped reflector agent) or self (the
	// incumbent runs the improver task). Model and Provider set a teacher.
	Agent    string `json:"agent,omitempty" yaml:"agent"`
	Model    string `json:"model,omitempty" yaml:"model"`
	Provider string `json:"provider,omitempty" yaml:"provider"`
	// Candidates are the ideas per round, Keep the keep set size, Merge
	// adds a merge slot, RestartRounds are rounds with a restart slot (A3.4).
	Candidates    int   `json:"candidates" yaml:"candidates"`
	Keep          int   `json:"keep" yaml:"keep"`
	Merge         bool  `json:"merge,omitempty" yaml:"merge"`
	RestartRounds []int `json:"restart_rounds,omitempty" yaml:"restart_rounds"`
}

// PhaseSpec is how many trials per task a scoring phase runs.
type PhaseSpec struct {
	Trials int `json:"trials" yaml:"trials"`
	// Curve scores the incumbent on sealed after each change (sealed only;
	// descriptive, never read by a decision).
	Curve bool `json:"curve,omitempty" yaml:"curve"`
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

// Defaults fills every unset field. A preset fills its values first, then
// today's defaults fill the rest, so a spec without a preset behaves as
// before (gate alpha 0.05, one idea per round, the held-out curve on, no
// hard stop, no reserve).
func (s *Spec) Defaults() {
	if s.Kind == "" {
		s.Kind = Kind
	}
	s.presetDefaults()
	if s.Improver.Candidates == 0 {
		s.Improver.Candidates = 1
	}
	if s.Improver.Keep == 0 {
		s.Improver.Keep = 1
	}
	if s.Preset == "" {
		s.Sealed.Curve = true
	}
	if t := strings.TrimSpace(s.Improver.Task); t == "" || t == ImproverTaskAlias {
		s.Improver.Task = DefaultImproverTask
	}
	if s.Claim.Alpha == 0 {
		s.Claim.Alpha = DefaultClaimAlpha
	}
	if s.Claim.ExtensionAlpha == 0 {
		s.Claim.ExtensionAlpha = DefaultExtensionAlpha
	}
	if s.Claim.TargetGain == 0 {
		s.Claim.TargetGain = DefaultTargetGain
	}
	if s.Retries.MaxAttempts == 0 {
		s.Retries.MaxAttempts = MaxRetryAttempts
	}
	if s.Replicate.Loops == 0 {
		s.Replicate.Loops = 1
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

// presetDefaults fills the preset's values into unset fields. Switches a
// preset turns on (merge, curve, the reserved second look) are turned on;
// a spec that wants one off sets the preset to custom.
func (s *Spec) presetDefaults() {
	if s.Preset == "" {
		return
	}
	if s.Commit.Alpha == 0 {
		s.Commit.Alpha = DefaultGateAlpha
	}
	if s.Controls == nil {
		s.Controls = []string{ControlNullImprover, ControlRetries}
	}
	p, ok := presets[s.Preset]
	if !ok {
		return
	}
	setInt := func(f *int, v int) {
		if *f == 0 {
			*f = v
		}
	}
	setInt(&s.Rounds, p.rounds)
	setInt(&s.Improver.Candidates, p.candidates)
	setInt(&s.Improver.Keep, p.keep)
	setInt(&s.Screen.Trials, p.screen)
	setInt(&s.Commit.Trials, p.gate)
	setInt(&s.Sealed.Trials, p.sealed)
	s.Improver.Merge = s.Improver.Merge || p.merge
	s.Sealed.Curve = s.Sealed.Curve || p.curve
	s.Claim.Extension = s.Claim.Extension || p.extension
	if s.Improver.RestartRounds == nil && p.restart != nil {
		// Restart rounds past the run's last round are dropped.
		for _, r := range p.restart {
			if r <= s.Rounds {
				s.Improver.RestartRounds = append(s.Improver.RestartRounds, r)
			}
		}
	}
	if s.Improver.BudgetUSD == 0 {
		s.Improver.BudgetUSD = p.improverUSD
	}
	if s.Budget.TotalUSD == 0 {
		s.Budget.TotalUSD = p.limitUSD
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
	if len(s.Editable.Files)+len(s.Editable.Dirs) == 0 && !s.Editable.Instructions {
		bad("editable needs at least one file or folder")
	}
	for _, f := range s.Editable.Files {
		if f == InstructionsKey {
			continue
		}
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
		if k != ControlBudgetMatched && k != ControlNullImprover && k != ControlRetries {
			bad("unknown control %q (retries_v0, null_improver, budget_matched_v0)", k)
		}
	}
	errs = append(errs, s.validateW4()...)
	return errors.Join(errs...)
}

// validateW4 checks the fields of presets, ideas per round, the claim,
// retries, replication, the improver agent and surfaces.
func (s *Spec) validateW4() []error {
	var errs []error
	bad := func(f string, a ...any) { errs = append(errs, fmt.Errorf(f, a...)) }
	switch s.Preset {
	case "", PresetSmall, PresetMedium, PresetLarge, PresetCustom:
	default:
		bad("preset must be small, medium, large or custom")
	}
	im := s.Improver
	if im.Candidates < 1 || im.Candidates > MaxCandidates || im.Keep < 1 || im.Keep > MaxCandidates {
		bad("improver candidates and keep must be 1 to %d", MaxCandidates)
	}
	for _, r := range im.RestartRounds {
		if r < 1 || r > s.Rounds {
			bad("improver restart_rounds must be rounds 1 to %d", s.Rounds)
			break
		}
	}
	if s.Budget.TotalUSD < 0 || (s.Preset != "" && !(s.Budget.TotalUSD > 0)) {
		bad("budget total_usd must be above 0 when a preset is set")
	}
	c := s.Claim
	if !(c.Alpha > 0 && c.Alpha < 0.5) {
		bad("claim alpha must be in (0, 0.5)")
	}
	if !(c.ExtensionAlpha > 0 && c.ExtensionAlpha < c.Alpha) {
		bad("claim extension_alpha must be above 0 and below the claim alpha")
	}
	if !(c.TargetGain > 0 && c.TargetGain < 1) {
		bad("claim target_gain must be in (0, 1)")
	}
	if s.Retries.MaxAttempts < 1 || s.Retries.MaxAttempts > MaxRetryAttempts {
		bad("retries max_attempts must be 1 to %d", MaxRetryAttempts)
	}
	rp := s.Replicate
	if rp.Loops < 1 || rp.Loops > MaxReplicateLoops {
		bad("replicate loops must be 1 to %d", MaxReplicateLoops)
	}
	if tr := rp.Transfer; tr != nil && (tr.Suite == "") == (tr.Model == "") {
		bad("replicate transfer needs exactly one of suite or model")
	}
	switch im.Agent {
	case "", "reflector", "self":
	default:
		bad("improver agent must be reflector or self")
	}
	if (im.Model == "") != (im.Provider == "") {
		bad("improver model and provider must be set together")
	}
	if s.Surfaces.ToolDescriptions && os.Getenv(EnvToolDescriptions) != "1" {
		bad("Editing tool descriptions is not switched on on this server.")
	}
	return errs
}

// Hash is the spec's content hash, frozen in the improvement record.
func (s *Spec) Hash() (string, error) { return utils.HashJSON(s) }

// RoundAlpha is the level of one round's commit test: the family level
// split evenly over the rounds (Bonferroni), so the whole loop commits a
// non-improvement with chance at most Commit.Alpha.
func (s *Spec) RoundAlpha() float64 { return s.Commit.Alpha / float64(max(1, s.Rounds)) }

// Allows reports whether a container path is in the bundle.
func (e Editable) Allows(path string) bool {
	if path == InstructionsKey {
		return e.Instructions
	}
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
