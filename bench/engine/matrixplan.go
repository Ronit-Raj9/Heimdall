package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// maxMatrixArms caps a matrix at one arm per letter A to Z, so generated arm
// ids never collide with a reserved id (A2, compat) and stay readable.
const maxMatrixArms = 26

// WithModelPrices gives the planner the price snapshot's lookup, so a matrix
// that names a model with no price is refused unless it allows unpriced
// models (checkMatrixPrices). Without it every matrix model counts as
// unpriced.
func WithModelPrices(priced func(model string) bool) PlannerOption {
	return func(p *Planner) { p.prices = priced }
}

// ExpandMatrix returns the experiment with its matrix (docs/model-matrix.md)
// expanded into arms: one per combination of agent config and model, agents
// first, with ids A, B, C... and generated labels. A model sets the arm's
// model and, when given, its gateway provider; an agent path sets the arm's
// agent config. The matrix itself stays on the returned experiment, so the
// frozen manifest records where its arms came from. An experiment without a
// matrix is returned as it is, so its plan and its manifest hash never
// change. It refuses a matrix beside explicit arms, an empty or duplicated
// entry, fewer than two or more than 26 combinations, and agents left out
// with no base config. It is pure.
func ExpandMatrix(e *schemas.Experiment) (*schemas.Experiment, error) {
	if e == nil || e.Matrix == nil {
		return e, nil
	}
	mx := e.Matrix
	if len(e.Arms) > 0 {
		return nil, errors.New("matrix: set either matrix or arms, not both")
	}
	if len(mx.Models) == 0 && len(mx.Agents) == 0 {
		return nil, errors.New("matrix: list models, agents or both")
	}
	if err := matrixDuplicates(mx); err != nil {
		return nil, err
	}
	agents := mx.Agents
	if len(agents) == 0 {
		if e.Base == "" {
			return nil, errors.New("matrix: no agents listed and the experiment has no base config")
		}
		agents = []string{""}
	}
	models := mx.Models
	if len(models) == 0 {
		models = []schemas.MatrixModel{{}}
	}
	n := len(agents) * len(models)
	switch {
	case n < 2:
		return nil, fmt.Errorf("matrix: %d combination; a comparison needs at least 2", n)
	case n > maxMatrixArms:
		return nil, fmt.Errorf("matrix: %d combinations; at most %d arms are supported", n, maxMatrixArms)
	}
	out := *e
	out.Arms = make(map[string]schemas.ArmSpec, n)
	i := 0
	for _, agent := range agents {
		for _, model := range models {
			out.Arms[string(rune('A'+i))] = matrixArm(agent, model, len(mx.Agents) > 0, len(mx.Models) > 0)
			i++
		}
	}
	return &out, nil
}

// planMatrix expands the experiment's matrix, checks its models against the
// price snapshot and plans the result under mode. Without a matrix it is
// exactly plan.
func (p *Planner) planMatrix(ctx context.Context, exp *schemas.Experiment, mode planMode) (*schemas.Manifest, error) {
	if exp == nil || exp.Matrix == nil {
		return p.plan(ctx, exp, mode)
	}
	if len(exp.Arms) > 0 {
		// QC2-11-01: a frozen manifest keeps the matrix beside the arms it
		// expanded to. A replan of that experiment (a remote worker) plans
		// those arms as frozen, provided they are exactly the matrix's
		// expansion; the price check was the coordinator's planning decision.
		if !expandedFromMatrix(exp) {
			return nil, fmt.Errorf("plan %s: matrix: set either matrix or arms, not both", exp.Name)
		}
		return p.plan(ctx, exp, mode)
	}
	expanded, err := ExpandMatrix(exp)
	if err != nil {
		return nil, fmt.Errorf("plan %s: %w", exp.Name, err)
	}
	if err := checkMatrixPrices(exp.Matrix, p.prices); err != nil {
		return nil, fmt.Errorf("plan %s: %w", exp.Name, err)
	}
	return p.plan(ctx, expanded, mode)
}

// expandedFromMatrix reports whether exp's arms are exactly what its matrix
// expands to, as in a manifest's frozen experiment. Arms are compared in
// their JSON form, so a manifest that went through JSON still matches.
func expandedFromMatrix(exp *schemas.Experiment) bool {
	bare := *exp
	bare.Arms = nil
	expanded, err := ExpandMatrix(&bare)
	if err != nil {
		return false
	}
	want, err1 := json.Marshal(expanded.Arms)
	got, err2 := json.Marshal(exp.Arms)
	return err1 == nil && err2 == nil && bytes.Equal(want, got)
}

// checkMatrixPrices refuses a matrix model the price snapshot does not list,
// unless the matrix allows unpriced models: its runs' cost would be unknown,
// so cost per success could never be compared. priced nil means no snapshot
// was wired and every model is unpriced. Models an agent config names
// itself are not checked here; their cost is unknown at run time the same
// way any run's is.
func checkMatrixPrices(mx *schemas.MatrixSpec, priced func(string) bool) error {
	if mx == nil || mx.AllowUnpriced {
		return nil
	}
	var missing []string
	for _, m := range mx.Models {
		if priced == nil || !priced(m.Model) {
			missing = append(missing, m.Model)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("matrix: no price in the price snapshot for %s, so their cost would be unknown; "+
		"set matrix.allow_unpriced: true (or pass -allow-unpriced) to plan anyway", strings.Join(missing, ", "))
}

// matrixArm is one combination's arm. withAgent and withModel say which
// matrix lists were given, so a label names only what varies.
func matrixArm(agent string, model schemas.MatrixModel, withAgent, withModel bool) schemas.ArmSpec {
	arm := schemas.ArmSpec{Agent: agent}
	var label []string
	if withAgent {
		label = append(label, strings.TrimSuffix(filepath.Base(agent), filepath.Ext(agent)))
	}
	if withModel {
		arm.Set = map[string]any{"model": model.Model}
		name := model.Model
		if model.Provider != "" {
			arm.Set["gateway.provider"] = model.Provider
			name += " via " + model.Provider
		}
		label = append(label, name)
	}
	arm.Label = strings.Join(label, " / ")
	return arm
}

// matrixDuplicates refuses an empty or repeated model or agent entry: two
// arms with the same config would fail the plan later with a less useful
// message.
func matrixDuplicates(mx *schemas.MatrixSpec) error {
	seen := map[string]bool{}
	for _, m := range mx.Models {
		key := m.Model + "\x00" + m.Provider
		switch {
		case strings.TrimSpace(m.Model) == "":
			return errors.New("matrix: a model entry is empty")
		case seen[key]:
			return fmt.Errorf("matrix: model %s is listed twice", m.Model)
		}
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, a := range mx.Agents {
		switch {
		case strings.TrimSpace(a) == "":
			return errors.New("matrix: an agent entry is empty")
		case seen[a]:
			return fmt.Errorf("matrix: agent %s is listed twice", a)
		}
		seen[a] = true
	}
	return nil
}
