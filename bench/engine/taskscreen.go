package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// WithIncludeQuarantined lets an explicit task list name a quarantined task
// (the CLI's -include-quarantined). `tasks: all` still skips quarantined
// tasks: including one is always a named, deliberate choice.
func WithIncludeQuarantined(include bool) PlannerOption {
	return func(p *Planner) { p.includeQuarantined = include }
}

// WithIgnoreQuarantine turns the quarantine screen off. A remote worker
// re-plans an experiment only to check that its own project reproduces the
// frozen manifest; a task quarantined after the freeze must not make that
// check fail, since quarantine only ever applies to new plans.
func WithIgnoreQuarantine() PlannerOption {
	return func(p *Planner) { p.ignoreQuarantine = true }
}

type frozenTasksKey struct{}

// WithFrozenTasks marks ctx as a replan of the frozen manifest m (a remote
// worker's admission check): with the quarantine screen off
// (WithIgnoreQuarantine), `tasks: all` keeps only the tasks m uses, since
// the coordinator dropped the rest before the freeze, whatever this
// machine's own quarantine sidecar says (2-C-11, QC2-11-05).
func WithFrozenTasks(ctx context.Context, m *schemas.Manifest) context.Context {
	used := map[string]bool{}
	for _, s := range m.Slots {
		used[s.Task] = true
	}
	return context.WithValue(ctx, frozenTasksKey{}, used)
}

func frozenTasks(ctx context.Context) map[string]bool {
	used, _ := ctx.Value(frozenTasksKey{}).(map[string]bool)
	return used
}

// screenTasks applies the task split and the quarantine sidecar
// (docs/suite-health.md, docs/held-out-and-canaries.md) to resolved
// conditions, for a new plan only; a manifest already written is never
// re-screened, so existing experiments and their analysis are unaffected.
//
// A condition with `tasks: all` keeps the tasks of its split (public unless
// the condition sets split: held_out) and drops quarantined tasks, logging
// one line per dropped task. An explicit task list is taken as written,
// whatever each task's split, but naming a quarantined task is refused
// unless WithIncludeQuarantined is set; under warnOnly (an agent check) it
// only warns. A condition left with no task is refused.
func (p *Planner) screenTasks(ctx context.Context, suite *schemas.Suite, specs []schemas.ConditionSpec, conds []condPlan, warnOnly bool) ([]condPlan, error) {
	var quarantined map[string]schemas.TaskQuarantine
	frozen := frozenTasks(ctx)
	if !p.ignoreQuarantine {
		q, err := readQuarantine(suite.Root)
		if err != nil {
			return nil, err
		}
		quarantined = q
	}
	// A frozen replan (QC2-11-05): `tasks: all` is rebuilt from the
	// coordinator's frozen selection, so what its quarantine dropped stays
	// out even when this machine has no sidecar, and a task quarantined here
	// after the freeze stays in. The local sidecar plays no part.
	replan := p.ignoreQuarantine && frozen != nil
	out := make([]condPlan, len(conds))
	for i, cp := range conds {
		spec := specFor(specs, cp.id)
		split := spec.Split
		if split == "" {
			split = schemas.SplitPublic
		}
		if split != schemas.SplitPublic && split != schemas.SplitHeldOut {
			return nil, fmt.Errorf("condition %s: split must be %s or %s, got %q", cp.id, schemas.SplitPublic, schemas.SplitHeldOut, spec.Split)
		}
		kept := make([]string, 0, len(cp.tasks))
		for _, id := range cp.tasks {
			q, isQ := quarantined[id]
			if !spec.Tasks.All {
				if isQ && !p.includeQuarantined {
					if !warnOnly {
						return nil, fmt.Errorf("condition %s names task %s, which is quarantined (%s); pass -include-quarantined to run it anyway, or `suite restore` it", cp.id, id, q.Reason)
					}
					p.logger.Warn("agent check runs a quarantined task", "condition", cp.id, "task", id, "reason", q.Reason)
				}
				kept = append(kept, id)
				continue
			}
			if taskSplit(suite.TaskByID(id)) != split {
				continue
			}
			if replan && !frozen[id] {
				continue
			}
			if isQ {
				p.logger.Warn("skipping quarantined task for this new plan", "condition", cp.id, "task", id, "reason", q.Reason, "since", q.At.Format("2006-01-02"))
				continue
			}
			kept = append(kept, id)
		}
		if len(kept) == 0 {
			return nil, fmt.Errorf("condition %s selects no tasks after the %s split and the quarantine", cp.id, split)
		}
		cp.tasks = kept
		out[i] = cp
	}
	return out, nil
}

// heldOutTasks returns the held-out tasks the conditions use, sorted, or nil
// for a public-only plan, so the manifest of a public plan encodes as before.
func heldOutTasks(suite *schemas.Suite, conds []condPlan) []string {
	var out []string
	for _, id := range usedTasks(conds) {
		if taskSplit(suite.TaskByID(id)) == schemas.SplitHeldOut {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

// readQuarantine reads the quarantined tasks of the suite sidecar. A missing
// file means none. The planner reads the JSON itself so the engine does not
// import the suite loader package.
func readQuarantine(root string) (map[string]schemas.TaskQuarantine, error) {
	if root == "" {
		return nil, nil
	}
	path := filepath.Join(root, schemas.SuiteHealthFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var f schemas.SuiteHealthFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return f.Quarantined, nil
}

// specFor returns the condition spec with id. Conditions were resolved from
// these specs, so one always matches; compatibility conditions share one
// selector and come through compatSpecs.
func specFor(specs []schemas.ConditionSpec, id string) schemas.ConditionSpec {
	for _, s := range specs {
		if s.ID == id {
			return s
		}
	}
	return schemas.ConditionSpec{ID: id, Tasks: schemas.TaskSelector{All: true}}
}

// taskSplit is a task's split, public when unset.
func taskSplit(t *schemas.Task) string {
	if t == nil || strings.TrimSpace(t.Split) == "" {
		return schemas.SplitPublic
	}
	return t.Split
}

// screenAll screens the experiment's conditions and its compatibility
// conditions (screenTasks).
func (p *Planner) screenAll(ctx context.Context, suite *schemas.Suite, e *schemas.Experiment, conds, compat []condPlan, warnOnly bool) ([]condPlan, []condPlan, error) {
	conds, err := p.screenTasks(ctx, suite, e.Conditions, conds, warnOnly)
	if err != nil {
		return nil, nil, err
	}
	if e.Compatibility != nil {
		if compat, err = p.screenTasks(ctx, suite, compatSpecs(e.Compatibility), compat, warnOnly); err != nil {
			return nil, nil, fmt.Errorf("compatibility: %w", err)
		}
	}
	return conds, compat, nil
}
