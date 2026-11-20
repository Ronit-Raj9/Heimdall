package schemas

import (
	"bytes"
	"encoding/json"
	"fmt"

	"gopkg.in/yaml.v3"
)

// MatrixSpec is the experiment's `matrix:` sugar (docs/model-matrix.md): the
// planner expands it into one arm per combination of agent config and model,
// with generated labels, before anything else is planned. An experiment
// without it plans and encodes exactly as before.
type MatrixSpec struct {
	Models        []MatrixModel `json:"models,omitempty" yaml:"models"`
	Agents        []string      `json:"agents,omitempty" yaml:"agents"`                 // agent config paths; empty uses the experiment's base
	AllowUnpriced bool          `json:"allow_unpriced,omitempty" yaml:"allow_unpriced"` // plan a model the price snapshot does not list
}

// MatrixModel is one model of a matrix, with the gateway provider that
// serves it when it differs from the agent config's. In YAML it is the model
// name alone or {model, provider}.
type MatrixModel struct {
	Model    string `json:"model" yaml:"model"`
	Provider string `json:"provider,omitempty" yaml:"provider,omitempty"`
}

// UnmarshalYAML accepts a model name or {model, provider}.
func (m *MatrixModel) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		*m = MatrixModel{Model: node.Value}
		return nil
	}
	// node.Decode does not inherit the outer decoder's KnownFields, so a
	// misspelled key (provdier) is refused here instead of silently dropped.
	if node.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(node.Content); i += 2 {
			if k := node.Content[i].Value; k != "model" && k != "provider" {
				return fmt.Errorf("matrix model: line %d: unknown field %q", node.Content[i].Line, k)
			}
		}
	}
	var obj struct {
		Model    string `yaml:"model"`
		Provider string `yaml:"provider"`
	}
	if err := node.Decode(&obj); err != nil {
		return fmt.Errorf("matrix model: %w", err)
	}
	*m = MatrixModel{Model: obj.Model, Provider: obj.Provider}
	return nil
}

// UnmarshalJSON accepts a model name or {"model", "provider"}.
func (m *MatrixModel) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("matrix model: %w", err)
		}
		*m = MatrixModel{Model: s}
		return nil
	}
	type plain MatrixModel
	var p plain
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields() // json.Unmarshal here would drop a misspelled provider
	if err := dec.Decode(&p); err != nil {
		return fmt.Errorf("matrix model: %w", err)
	}
	*m = MatrixModel(p)
	return nil
}

// ModelMatrix compares every arm of one experiment on one condition
// (GET /api/bench/experiments/{id}/matrix). Rows reuse the leaderboard's
// entry numbers; Pairs holds every pair of arms, each tested by the exact
// sign-flip test on the tasks both ran, with Holm adjustment across all
// pairs.
type ModelMatrix struct {
	ExpID       string                 `json:"exp_id"`
	Suite       string                 `json:"suite"` // id@version
	Condition   string                 `json:"condition"`
	Conditions  []string               `json:"conditions"` // conditions of the experiment with a graded run
	GradeSource string                 `json:"grade_source"`
	Alpha       float64                `json:"alpha"`
	Tasks       int                    `json:"tasks"`             // tasks every arm has a graded run on
	Separable   bool                   `json:"separable"`         // false when no pair could reach alpha after Holm, whatever the results
	Note        string                 `json:"note"`              // plain words: why nothing can be separated, or what the tiers mean
	Synthetic   bool                   `json:"synthetic"`         // scripted agent, scripted model or smoke experiment: numbers are not findings
	Rows        []MatrixRow            `json:"rows"`              // in rank order
	Pairs       []MatrixPair           `json:"pairs"`             // every pair of rows, in rank order of the first then the second
	Excluded    []LeaderboardExclusion `json:"excluded"`          // runs left out, by reason
	Split       string                 `json:"split,omitempty"`   // held_out when the matrix reads only held-out tasks; empty: public tasks only
	Invalid     bool                   `json:"invalid,omitempty"` // the newest analysis decided INVALID: no run is ranked, Excluded says why
}

// MatrixRow is one arm of a matrix: the arm id and the gateway provider its
// config routes to, beside the leaderboard entry numbers of its runs.
type MatrixRow struct {
	Arm      string `json:"arm"`
	Provider string `json:"provider"`
	LeaderboardEntry
}

// MatrixPair is the paired comparison of two arms. MeanDiff is the mean over
// common tasks of ArmX's success rate minus ArmY's.
type MatrixPair struct {
	ArmX        string   `json:"arm_x"`
	ArmY        string   `json:"arm_y"`
	Tasks       int      `json:"tasks"`     // common tasks
	NNonzero    int      `json:"n_nonzero"` // common tasks where the rates differ
	MeanDiff    float64  `json:"mean_diff"`
	P           *float64 `json:"p"`          // raw two-sided sign-flip p; nil without common tasks
	PDraws      int      `json:"p_draws,omitempty"` // Monte Carlo sign vectors behind P; 0 when P is exact (audit 1-B-07)
	PAdjusted   *float64 `json:"p_adjusted"` // Holm-adjusted across all pairs; nil without common tasks
	Significant bool     `json:"significant"`
	Reason      string   `json:"reason"` // plain words, e.g. "cannot separate: too few differing tasks (2 of 6)"
}
