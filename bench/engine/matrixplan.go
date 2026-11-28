package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/maximhq/bifrost/bench/matrixarms"
	"github.com/maximhq/bifrost/bench/schemas"
)

// ExpandMatrix returns the experiment with its matrix expanded into arms
// (matrixarms.Expand, which holds the rules).
func ExpandMatrix(e *schemas.Experiment) (*schemas.Experiment, error) { return matrixarms.Expand(e) }

// WithModelPrices gives the planner the price snapshot's lookup, so a matrix
// that names a model with no price is refused unless it allows unpriced
// models (checkMatrixPrices). Without it every matrix model counts as
// unpriced.
func WithModelPrices(priced func(model string) bool) PlannerOption {
	return func(p *Planner) { p.prices = priced }
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
		exact, withImages := expandedFromMatrix(exp)
		if !exact && !withImages {
			return nil, fmt.Errorf("plan %s: matrix: set either matrix or arms, not both", exp.Name)
		}
		if !exact && p.prices != nil { // task-image builds added by the comparison handler (handlers/experiments_taskimage_f3.go): a fresh plan, so the price rule holds; a replan without a snapshot keeps the coordinator's decision
			if err := checkMatrixPrices(exp.Matrix, p.prices); err != nil {
				return nil, fmt.Errorf("plan %s: %w", exp.Name, err)
			}
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
// expands to, as in a manifest's frozen experiment, and otherwise whether
// they are that expansion plus only the task-image build overrides
// (matrixarms.TaskImageSetKeys) the comparison handler adds for a benchmark whose tasks
// bring their own image. Arms are compared in their JSON form, so a manifest
// that went through JSON still matches.
func expandedFromMatrix(exp *schemas.Experiment) (exact, withImages bool) {
	bare := *exp
	bare.Arms = nil
	expanded, err := ExpandMatrix(&bare)
	if err != nil {
		return false, false
	}
	want, err1 := json.Marshal(expanded.Arms)
	got, err2 := json.Marshal(exp.Arms)
	if err1 != nil || err2 != nil {
		return false, false
	}
	if bytes.Equal(want, got) {
		return true, false
	}
	stripped := make(map[string]schemas.ArmSpec, len(exp.Arms))
	for id, arm := range exp.Arms {
		if len(arm.Set) > 0 {
			set := make(map[string]any, len(arm.Set))
			for k, v := range arm.Set {
				if _, img := expanded.Arms[id].Set[k]; img || !matrixarms.TaskImageSetKeys[k] {
					set[k] = v
				}
			}
			if len(set) == 0 {
				set = nil
			}
			arm.Set = set
		}
		stripped[id] = arm
	}
	got, err = json.Marshal(stripped)
	return false, err == nil && bytes.Equal(want, got)
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
