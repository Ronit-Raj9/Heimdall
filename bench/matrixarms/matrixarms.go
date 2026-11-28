// Package matrixarms expands a comparison's model matrix
// (docs/model-matrix.md) into arms. It is shared by the planner (engine) and
// the comparison handler, which adds task-image builds to the expanded arms.
package matrixarms

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// MaxArms caps a matrix at one arm per letter A to Z, so generated arm
// ids never collide with a reserved id (A2, compat) and stay readable.
const MaxArms = 26

// Expand returns the experiment with its matrix (docs/model-matrix.md)
// expanded into arms: one per combination of agent config and model, agents
// first, with ids A, B, C... and generated labels. A model sets the arm's
// model and, when given, its gateway provider; an agent path sets the arm's
// agent config. The matrix itself stays on the returned experiment, so the
// frozen manifest records where its arms came from. An experiment without a
// matrix is returned as it is, so its plan and its manifest hash never
// change. It refuses a matrix beside explicit arms, an empty or duplicated
// entry, fewer than two or more than 26 combinations, and agents left out
// with no base config. It is pure.
func Expand(e *schemas.Experiment) (*schemas.Experiment, error) {
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
	if err := duplicates(mx); err != nil {
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
	case n > MaxArms:
		return nil, fmt.Errorf("matrix: %d combinations; at most %d arms are supported", n, MaxArms)
	}
	out := *e
	out.Arms = make(map[string]schemas.ArmSpec, n)
	i := 0
	for _, agent := range agents {
		for _, model := range models {
			out.Arms[string(rune('A'+i))] = arm(agent, model, len(mx.Agents) > 0, len(mx.Models) > 0)
			i++
		}
	}
	return &out, nil
}

// arm is one combination's arm. withAgent and withModel say which
// matrix lists were given, so a label names only what varies.
func arm(agent string, model schemas.MatrixModel, withAgent, withModel bool) schemas.ArmSpec {
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

// duplicates refuses an empty or repeated model or agent entry: two
// arms with the same config would fail the plan later with a less useful
// message.
func duplicates(mx *schemas.MatrixSpec) error {
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

// TaskImageSetKeys are the arm overrides that build an agent on each task's
// own image (handlers deriveTaskImageAgent). A matrix arm may carry them.
var TaskImageSetKeys = map[string]bool{"build.base_arg": true, "build.dockerfile": true, "build.tag": true, "build.context": true, "image": true}
