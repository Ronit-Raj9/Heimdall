package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
	"gopkg.in/yaml.v3"
)

// evaluationIDPattern is a saved evaluation id (evalstore ids).
var evaluationIDPattern = regexp.MustCompile(`^ev_[a-z0-9_]{1,64}$`)

// IsEvaluationID reports whether ref names a saved evaluation rather than a
// spec file.
func IsEvaluationID(ref string) bool { return evaluationIDPattern.MatchString(ref) }

// LoadEvaluationSpec reads the evaluation a schedule repeats: a saved
// evaluation id (ev_...), whose spec is read from
// <root>/.heimdall/evaluations/<id>.json, or a project-relative spec file
// (.json, .yaml or .yml) holding an evaluation spec, the body of POST
// /evaluations.
func LoadEvaluationSpec(root, ref string) (*schemas.EvaluationSpec, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, errors.New("evaluation is empty")
	}
	if IsEvaluationID(ref) {
		b, err := os.ReadFile(filepath.Join(root, ".heimdall", "evaluations", ref+".json"))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("evaluation %s does not exist", ref)
			}
			return nil, fmt.Errorf("read evaluation %s: %w", ref, err)
		}
		var ev schemas.Evaluation
		if err := json.Unmarshal(b, &ev); err != nil {
			return nil, fmt.Errorf("decode evaluation %s: %w", ref, err)
		}
		spec := ev.Spec
		return &spec, nil
	}
	if strings.Contains(ref, "..") {
		return nil, fmt.Errorf("evaluation spec %q must stay inside the project", ref)
	}
	b, err := os.ReadFile(Resolve(root, ref))
	if err != nil {
		return nil, fmt.Errorf("evaluation spec: %w", err)
	}
	var spec schemas.EvaluationSpec
	switch strings.ToLower(filepath.Ext(ref)) {
	case ".json":
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&spec); err != nil {
			return nil, fmt.Errorf("evaluation spec %s: %w", ref, err)
		}
	case ".yaml", ".yml":
		var raw any
		if err := yaml.Unmarshal(b, &raw); err != nil {
			return nil, fmt.Errorf("evaluation spec %s: %w", ref, err)
		}
		j, err := json.Marshal(raw)
		if err != nil {
			return nil, fmt.Errorf("evaluation spec %s: %w", ref, err)
		}
		dec := json.NewDecoder(strings.NewReader(string(j)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&spec); err != nil {
			return nil, fmt.Errorf("evaluation spec %s: %w", ref, err)
		}
	default:
		return nil, fmt.Errorf("evaluation spec %s: want a saved evaluation id (ev_...) or a .json or .yaml file", ref)
	}
	return &spec, nil
}

// EvaluationCap is the spend cap of one scheduled evaluation: its own
// budget_usd, else the schedule's max_usd_per_day (the runner then launches
// it with that budget).
func EvaluationCap(s Schedule, spec *schemas.EvaluationSpec) float64 {
	if spec != nil && finitePositive(spec.BudgetUSD) {
		return spec.BudgetUSD
	}
	return s.MaxUSDPerDay
}

// validateEvaluation checks an evaluation schedule: the evaluation loads,
// a daily cap is set (an evaluation always spends) and the evaluation's
// budget fits under it.
func (s Schedule) validateEvaluation(root string) error {
	wrap := func(format string, args ...any) error {
		return fmt.Errorf("schedule %s: "+format, append([]any{s.Name}, args...)...)
	}
	if s.Experiment != "" {
		return wrap("name an experiment or an evaluation, not both")
	}
	spec, err := LoadEvaluationSpec(root, s.Evaluation)
	if err != nil {
		return wrap("%w", err)
	}
	if !finitePositive(s.MaxUSDPerDay) {
		return wrap("max_usd_per_day is required for an evaluation, so a forgotten schedule cannot spend without limit")
	}
	if spec.BudgetUSD > s.MaxUSDPerDay {
		return wrap("the evaluation's budget %.2f USD is above max_usd_per_day %.2f USD, so it could never start", spec.BudgetUSD, s.MaxUSDPerDay)
	}
	if s.NewModels != nil {
		if err := s.validateNewModels(spec); err != nil {
			return wrap("%w", err)
		}
	}
	return nil
}

// approveEvaluation is approve for an evaluation schedule.
func (d *Daemon) approveEvaluation(s Schedule, day string) (*Approval, string) {
	spec, err := LoadEvaluationSpec(d.o.Root, s.Evaluation)
	if err != nil {
		return nil, "evaluation: " + err.Error()
	}
	if !finitePositive(s.MaxUSDPerDay) {
		return nil, "max_usd_per_day is required for an evaluation"
	}
	capUSD := EvaluationCap(s, spec)
	spent := d.spent(s.Name, day)
	if spent+capUSD > s.MaxUSDPerDay+1e-9 {
		return nil, fmt.Sprintf("daily cap: spent %.4f USD today plus the evaluation's budget %.2f USD would pass max_usd_per_day %.2f USD",
			spent, capUSD, s.MaxUSDPerDay)
	}
	return &Approval{CapUSD: capUSD, Limited: true}, ""
}
