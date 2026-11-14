package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Experiment is read from experiments/<name>.yaml (DESIGN.md 16.6).
type Experiment struct {
	ID            string             `json:"id" yaml:"id"` // e_<slug>; set by the planner when empty
	Name          string             `json:"name" yaml:"name"`
	Suite         string             `json:"suite" yaml:"suite"` // e.g. coding-core@1.0.0
	Base          string             `json:"base" yaml:"base"`   // agent config path for every arm
	Arms          map[string]ArmSpec `json:"arms" yaml:"arms"`
	Primary       Endpoint           `json:"primary" yaml:"primary"`
	Guards        []GuardSpec        `json:"guards" yaml:"guards"`
	Conditions    []ConditionSpec    `json:"conditions" yaml:"conditions"`
	Compatibility *CompatibilitySpec `json:"compatibility,omitempty" yaml:"compatibility"`
	Budget        BudgetSpec         `json:"budget" yaml:"budget"`
	Verdict       VerdictSpec        `json:"verdict" yaml:"verdict"`
	Metrics       []string           `json:"metrics,omitempty" yaml:"metrics"`         // subset; empty = all
	ExtraGates    []string           `json:"extra_gates,omitempty" yaml:"extra_gates"` // baseline gates are always on
	Seed          int64              `json:"seed" yaml:"seed"`
	Concurrency   int                `json:"concurrency" yaml:"concurrency"`
	ManifestMode  string             `json:"manifest_mode" yaml:"manifest_mode"` // agent_exposure | stack_resilience
	Policy        string             `json:"policy,omitempty" yaml:"policy"`     // release policy file, relative to the bench root
}

// ArmSpec is a base agent config plus dotted-path overrides, for example
// {"notes.user_file": "fixtures/notes/codewins.md"} or {"model": "..."}.
type ArmSpec struct {
	Label string         `json:"label" yaml:"label"`
	Agent string         `json:"agent,omitempty" yaml:"agent"` // overrides the experiment's base config path
	Set   map[string]any `json:"set,omitempty" yaml:"set"`
}

// Endpoint is one condition and one metric.
type Endpoint struct {
	Condition string `json:"condition" yaml:"condition"`
	Metric    string `json:"metric" yaml:"metric"`
}

// GuardSpec is a release guard. Rules: no_regression (the release effect on
// Condition must not be REGRESSED), no_new_integrity_violation, and
// timeout_parity (the arms' timeout rates on Condition, or on every condition
// when it is empty, must not differ by more than 10 pp while either is above
// 20%; a failure holds the release and never makes it REGRESSED or INVALID).
type GuardSpec struct {
	Condition string `json:"condition,omitempty" yaml:"condition"`
	Rule      string `json:"rule" yaml:"rule"`
	Metric    string `json:"metric,omitempty" yaml:"metric"` // defaults to the primary metric
}

// ConditionSpec is clean or one probe, on a task selection, with a trial count.
type ConditionSpec struct {
	ID     string       `json:"id" yaml:"id"` // "clean" or a probe id
	Tasks  TaskSelector `json:"tasks" yaml:"tasks"`
	Trials int          `json:"trials" yaml:"trials"`
}

// CompatibilitySpec runs a second agent at gateway-level evidence. Its runs
// never enter the verdict.
type CompatibilitySpec struct {
	Agent      string       `json:"agent" yaml:"agent"`
	Tasks      TaskSelector `json:"tasks" yaml:"tasks"`
	Conditions []string     `json:"conditions" yaml:"conditions"`
	Trials     int          `json:"trials" yaml:"trials"`
}

// BudgetSpec caps total spend. The scheduler stops leasing slots past it.
type BudgetSpec struct {
	TotalUSD float64 `json:"total_usd" yaml:"total_usd"`
}

// VerdictSpec configures the verdict policy.
type VerdictSpec struct {
	Policy    string  `json:"policy" yaml:"policy"` // paired_signflip
	Alpha     float64 `json:"alpha" yaml:"alpha"`
	AssumedSD float64 `json:"assumed_sd" yaml:"assumed_sd"`
}

// ConditionClean is the id of the unperturbed condition.
const ConditionClean = "clean"

// TaskSelector is either all tasks of the suite or an explicit list. In YAML it
// is the scalar `all` or a sequence of task ids.
type TaskSelector struct {
	All bool
	IDs []string
}

// UnmarshalYAML accepts `all` or a list of ids.
func (s *TaskSelector) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		if node.Value != "all" {
			return fmt.Errorf("tasks: want \"all\" or a list, got %q", node.Value)
		}
		*s = TaskSelector{All: true}
		return nil
	case yaml.SequenceNode:
		var ids []string
		if err := node.Decode(&ids); err != nil {
			return fmt.Errorf("tasks: %w", err)
		}
		*s = TaskSelector{IDs: ids}
		return nil
	default:
		return fmt.Errorf("tasks: want \"all\" or a list")
	}
}

// MarshalJSON writes "all" or the list, the same shape as the YAML form and the
// UI's `tasks: "all" | string[]`.
func (s TaskSelector) MarshalJSON() ([]byte, error) {
	if s.All {
		return []byte(`"all"`), nil
	}
	ids := s.IDs
	if ids == nil {
		ids = []string{}
	}
	return json.Marshal(ids)
}

// UnmarshalJSON accepts "all", a list of ids, or the object {"all", "ids"}.
func (s *TaskSelector) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) > 0 && b[0] == '"':
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return fmt.Errorf("tasks: %w", err)
		}
		if v != "all" {
			return fmt.Errorf("tasks: want \"all\" or a list, got %q", v)
		}
		*s = TaskSelector{All: true}
	case len(b) > 0 && b[0] == '[':
		var ids []string
		if err := json.Unmarshal(b, &ids); err != nil {
			return fmt.Errorf("tasks: %w", err)
		}
		*s = TaskSelector{IDs: ids}
	default:
		var obj struct {
			All bool     `json:"all"`
			IDs []string `json:"ids"`
		}
		if err := json.Unmarshal(b, &obj); err != nil {
			return fmt.Errorf("tasks: %w", err)
		}
		*s = TaskSelector{All: obj.All, IDs: obj.IDs}
	}
	return nil
}

// MarshalYAML writes `all` or the list, the inverse of UnmarshalYAML.
func (s TaskSelector) MarshalYAML() (any, error) {
	if s.All {
		return "all", nil
	}
	return s.IDs, nil
}

// Resolve returns the selected task ids given every task id of the suite.
func (s TaskSelector) Resolve(all []string) []string {
	if s.All {
		return append([]string(nil), all...)
	}
	return append([]string(nil), s.IDs...)
}

// Manifest is the frozen plan of an experiment, hashed before the first run.
// Changing anything makes a new experiment.
type Manifest struct {
	ExpID          string                 `json:"exp_id"`
	CreatedAt      time.Time              `json:"created_at"`
	Experiment     Experiment             `json:"experiment"`
	SuiteID        string                 `json:"suite_id"`
	SuiteVersion   string                 `json:"suite_version"`
	SuiteLockHash  string                 `json:"suite_lock_hash"`
	Arms           map[string]ArmManifest `json:"arms"`
	Compatibility  *ArmManifest           `json:"compatibility,omitempty"`
	Parts          map[string]string      `json:"parts"` // "<kind>/<name>" -> version
	Gateway        *GatewayManifest       `json:"gateway,omitempty"`
	ArmOrder       map[string][]string    `json:"arm_order"` // task id -> arm ids in run order, drawn once per task from Seed
	Slots          []Slot                 `json:"slots"`
	RandomizationU string                 `json:"randomization_unit"` // "task"
	BenchVersion   string                 `json:"bench_version"`
	PolicyHash     string                 `json:"policy_hash,omitempty"`  // sha256 of the policy file named by the experiment
	BudgetDiffs    []BudgetDiff           `json:"budget_diffs,omitempty"` // budgets that differ between arms with no arm setting them
	Hash           string                 `json:"hash"`                   // sha256 of the canonical JSON with Hash empty
}

// BudgetDiff is one agent budget that differs between arms although no arm
// declares it with `set:`, so it is not the experiment's variable. The
// planner warns and records it; it never refuses the plan, because a
// comparison of two agent configs can differ in budgets on purpose.
type BudgetDiff struct {
	Field  string            `json:"field"`   // dotted path, e.g. budgets.cpus
	PerArm map[string]string `json:"per_arm"` // arm -> value as written in the resolved config
}

// parityBudgets are the agent budgets that must match between arms unless an
// arm declares them with `set:` (DESIGN.md 9.4): container resources and
// limits alone can move a success rate.
var parityBudgets = []string{"wall_s", "turns", "cpus", "mem_gb"}

// BudgetDiffsOf returns every parity budget whose resolved value differs
// between the arms while no arm sets it (`budgets.<field>` or `budgets`). A
// budget an arm sets is the declared variable and may differ. The planner
// records the result in the manifest; the verdict recomputes it from the
// manifest's resolved arms, so manifests written before the field existed are
// checked too.
func BudgetDiffsOf(e *Experiment, arms map[string]ArmManifest) []BudgetDiff {
	ids := make([]string, 0, len(arms))
	for id := range arms {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var out []BudgetDiff
	for _, field := range parityBudgets {
		if budgetDeclared(e, field) {
			continue
		}
		per := map[string]string{}
		values := map[string]bool{}
		for _, id := range ids {
			v := budgetValue(arms[id].Config.Budgets, field)
			per[id] = v
			values[v] = true
		}
		if len(values) > 1 {
			out = append(out, BudgetDiff{Field: "budgets." + field, PerArm: per})
		}
	}
	return out
}

// budgetDeclared reports whether some arm sets the budget with `set:`.
func budgetDeclared(e *Experiment, field string) bool {
	if e == nil {
		return false
	}
	for _, a := range e.Arms {
		for path := range a.Set {
			if path == "budgets" || path == "budgets."+field {
				return true
			}
		}
	}
	return false
}

// budgetValue is one budget as text. An unset optional budget (turns) reads
// "unset", so 0 and "no limit" are told apart.
func budgetValue(b Budgets, field string) string {
	switch field {
	case "wall_s":
		return strconv.Itoa(b.WallS)
	case "turns":
		if b.Turns == 0 {
			return "unset"
		}
		return strconv.Itoa(b.Turns)
	case "cpus":
		return strconv.FormatFloat(b.CPUs, 'g', -1, 64)
	case "mem_gb":
		return strconv.FormatFloat(b.MemGB, 'g', -1, 64)
	default:
		return ""
	}
}

// ArmManifest is one arm's resolved config and the hashes of every file laid
// down for it, per condition.
type ArmManifest struct {
	Label      string                       `json:"label"`
	Config     AgentConfig                  `json:"config"`
	ConfigHash string                       `json:"config_hash"`
	Files      map[string]map[string]string `json:"files"` // condition -> path -> sha256
}
