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

// WithIncludeQuarantined lets an explicit task list run a quarantined task
// (the CLI's -include-quarantined); without it the task is skipped alone.
// `tasks: all` still skips quarantined tasks: including one is always a
// named, deliberate choice.
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
// Every condition keeps only the tasks of its split (public unless the
// condition sets split: held_out), whether it says `tasks: all` or names its
// tasks: a held-out task runs only in a condition that asks for the held-out
// split, so an explicit list can never leak one into a public plan. A task of
// the other split is dropped with one warning line.
//
// A quarantined task is dropped alone, with a warning that carries its
// reason; the rest of the condition still runs. An explicit list may keep a
// quarantined task with WithIncludeQuarantined. Under warnOnly (an agent
// check on the scripted model, which exposes nothing to a real model) an
// explicit list is taken as written and only warned about. A condition left
// with no task is refused.
//
// keepPending (a readiness check: smoke on the scripted model) keeps a
// generated task waiting for review on an explicit list: readiness is what
// the reviewer reads before accepting it, so it must run first. A rejected
// one stays out.
func (p *Planner) screenTasks(ctx context.Context, suite *schemas.Suite, specs []schemas.ConditionSpec, conds []condPlan, warnOnly, keepPending bool) ([]condPlan, error) {
	var quarantined map[string]schemas.TaskQuarantine
	var unreviewed map[string]string // generated task id -> pending | rejected (taskreview.go)
	frozen := frozenTasks(ctx)
	if !p.ignoreQuarantine {
		q, err := readQuarantine(suite.Root)
		if err != nil {
			return nil, err
		}
		quarantined = q
		r, err := readReviews(suite)
		if err != nil {
			return nil, err
		}
		unreviewed = r
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
		var dropped []string
		for _, id := range cp.tasks {
			q, isQ := quarantined[id]
			taskOf := taskSplit(suite.TaskByID(id))
			if !spec.Tasks.All {
				if p.ignoreQuarantine {
					// A worker's replan reproduces the frozen list as
					// written: the coordinator screened it already.
					kept = append(kept, id)
					continue
				}
				rv, isU := unreviewed[id]
				if warnOnly {
					if isU {
						p.logger.Warn("agent check runs a generated task nobody accepted yet", "condition", cp.id, "task", id, "review", rv)
					}
					if isQ {
						p.logger.Warn("agent check runs a quarantined task", "condition", cp.id, "task", id, "reason", q.Reason)
					}
					if taskOf != split {
						p.logger.Warn("agent check runs a task of another split", "condition", cp.id, "task", id, "split", taskOf)
					}
					kept = append(kept, id)
					continue
				}
				if taskOf != split {
					p.logger.Warn("skipping task of another split: a held-out task runs only when the condition asks for split held_out", "condition", cp.id, "task", id, "task_split", taskOf, "condition_split", split)
					dropped = append(dropped, id+" ("+taskOf+" split)")
					continue
				}
				if isU && keepPending && rv == schemas.ReviewPending {
					p.logger.Warn("readiness check runs a generated task waiting for review", "condition", cp.id, "task", id)
				} else if isU {
					p.logger.Warn("skipping generated task: it counts only after a person accepts it", "condition", cp.id, "task", id, "review", rv)
					dropped = append(dropped, id+" (generated, review "+rv+")")
					continue
				}
				if isQ && !p.includeQuarantined {
					p.logger.Warn("skipping quarantined task for this new plan", "condition", cp.id, "task", id, "reason", q.Reason, "since", q.At.Format("2006-01-02"))
					dropped = append(dropped, id+" (quarantined: "+q.Reason+")")
					continue
				}
				kept = append(kept, id)
				continue
			}
			if taskOf != split {
				continue
			}
			if replan && !frozen[id] {
				continue
			}
			if isQ {
				p.logger.Warn("skipping quarantined task for this new plan", "condition", cp.id, "task", id, "reason", q.Reason, "since", q.At.Format("2006-01-02"))
				continue
			}
			if _, isU := unreviewed[id]; isU {
				continue
			}
			kept = append(kept, id)
		}
		if len(kept) == 0 {
			if len(dropped) > 0 {
				return nil, fmt.Errorf("condition %s selects no tasks after the %s split and the quarantine, with generated tasks waiting for review left out (dropped %s; pass -include-quarantined to run a quarantined task anyway, or `suite restore` it; accept a generated task on its benchmark's Review tab)", cp.id, split, strings.Join(dropped, ", "))
			}
			return nil, fmt.Errorf("condition %s selects no tasks after the %s split and the quarantine, with generated tasks waiting for review left out", cp.id, split)
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

// readReviews returns the generated tasks of suite that no person accepted
// (schemas.TaskReviewState is pending or rejected), by id. The planner reads
// the review sidecar itself, as it does the quarantine sidecar, so a
// generated task never reaches a new plan, and with it no gate, release or
// report card, until it is accepted. A suite with no generated task reads
// no file.
func readReviews(suite *schemas.Suite) (map[string]string, error) {
	var gen []*schemas.Task
	for _, t := range suite.Tasks {
		if t.IsGenerated() {
			gen = append(gen, t)
		}
	}
	if len(gen) == 0 {
		return nil, nil
	}
	var f *schemas.TaskReviewFile
	if suite.Root != "" {
		path := filepath.Join(suite.Root, schemas.TaskReviewFileName)
		data, err := os.ReadFile(path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return nil, fmt.Errorf("read %s: %w", path, err)
		default:
			f = &schemas.TaskReviewFile{}
			if err := json.Unmarshal(data, f); err != nil {
				return nil, fmt.Errorf("parse %s: %w", path, err)
			}
		}
	}
	out := map[string]string{}
	for _, t := range gen {
		if st := schemas.TaskReviewState(t, f); st != schemas.ReviewAccepted {
			out[t.ID] = st
		}
	}
	return out, nil
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
	keepPending := e.Smoke && e.ScriptedUpstream
	conds, err := p.screenTasks(ctx, suite, e.Conditions, conds, warnOnly, keepPending)
	if err != nil {
		return nil, nil, err
	}
	if e.Compatibility != nil {
		if compat, err = p.screenTasks(ctx, suite, compatSpecs(e.Compatibility), compat, warnOnly, keepPending); err != nil {
			return nil, nil, fmt.Errorf("compatibility: %w", err)
		}
	}
	return conds, compat, nil
}
