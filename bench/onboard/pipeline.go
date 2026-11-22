package onboard

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/maximhq/bifrost/bench/suite"
	"gopkg.in/yaml.v3"
)

// ErrReadOnly is a draft aimed at a built-in library benchmark.
var ErrReadOnly = errors.New("built-in library benchmarks are read only")

// Draft makes a new draft set from in with plan. A set is saved even when
// no unit became a task, so its problems can be read.
func Draft(ctx context.Context, env *Env, in Input, plan Plan) (*DraftSet, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	plan.Suite = strings.TrimSpace(plan.Suite)
	if !ValidSuiteID(plan.Suite) {
		return nil, fmt.Errorf("benchmark id %q: use 1 to 64 lowercase letters, digits, '.', '_' or '-', starting with a letter or digit", plan.Suite)
	}
	if suite.IsLibrarySuite(env.Benchmarks(), plan.Suite) {
		return nil, fmt.Errorf("benchmark %s: %w; draft into a new benchmark id", plan.Suite, ErrReadOnly)
	}
	if plan.Hold.Pct < 0 || plan.Hold.Pct > 100 {
		return nil, errors.New("held_out.pct: 0 to 100")
	}
	if plan.Limit < 0 || plan.Limit > MaxDraftTasks {
		return nil, fmt.Errorf("limit: at most %d tasks per draft", MaxDraftTasks)
	}
	src, err := pick(ctx, env, in, plan.Source)
	if err != nil {
		return nil, err
	}
	set, err := NewSet(env.Root, plan.Suite)
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(filepath.Join(env.Benchmarks(), plan.Suite, suite.SuiteFile))
	set.NewSuite = statErr != nil
	set.Kind, set.Source, set.Plan, set.CreatedBy = src.Kind(), src.Name(), plan, env.Actor
	set.Plan.Source = src.Name()
	set.Input = Input{URL: in.URL, Upload: in.Upload, Name: in.Name, Hint: in.Hint}
	set.Title = plan.Title
	if set.Title == "" {
		set.Title = plan.Suite
	}
	if err := src.Draft(ctx, env, in, set.Plan, set); err != nil {
		os.RemoveAll(set.Dir())
		return nil, err
	}
	if err := set.Finish(ctx, env); err != nil {
		os.RemoveAll(set.Dir())
		return nil, err
	}
	if err := set.ApplySplit(plan.Hold.Pct, plan.Hold.Seed); err != nil {
		os.RemoveAll(set.Dir())
		return nil, err
	}
	if err := set.Finish(ctx, env); err != nil {
		os.RemoveAll(set.Dir())
		return nil, err
	}
	if err := set.Save(); err != nil {
		os.RemoveAll(set.Dir())
		return nil, err
	}
	return set, nil
}

// MaxDraftTasks caps one draft set.
const MaxDraftTasks = 500

// TaskEdit changes one draft task. Nil fields are left as they are.
type TaskEdit struct {
	Review  *string `json:"review,omitempty"`   // accepted, rejected or pending
	HeldOut *bool   `json:"held_out,omitempty"` // move it to the private split
	Drop    bool    `json:"drop,omitempty"`     // remove it from the draft
}

// EditTask applies e to task id and saves the set. A change to the task's
// files (split, drop) clears its readiness: the proof was for other files.
func (s *DraftSet) EditTask(ctx context.Context, env *Env, id string, e TaskEdit) error {
	t := s.Task(id)
	if t == nil {
		return fmt.Errorf("draft has no task %s", id)
	}
	if s.Published != nil {
		return errors.New("this draft was published; start a new draft to change its tasks")
	}
	if e.Drop {
		if err := s.dropTask(id); err != nil {
			return err
		}
	} else {
		if e.Review != nil {
			switch *e.Review {
			case ReviewAccepted, ReviewRejected, ReviewPending:
				t.Review = *e.Review
			default:
				return errors.New("review: accepted, rejected or pending")
			}
		}
		if e.HeldOut != nil && *e.HeldOut != t.HeldOut {
			if err := s.setSplit(id, *e.HeldOut); err != nil {
				return err
			}
			t.Readiness, t.CatchRate, t.Reason = nil, nil, ""
		}
	}
	if err := s.Finish(ctx, env); err != nil {
		return err
	}
	return s.Save()
}

func (s *DraftSet) dropTask(id string) error {
	p := filepath.Join(s.SuiteDir(), suite.SuiteFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	edited, err := removeTaskID(raw, id)
	if err != nil {
		return err
	}
	if err := os.WriteFile(p, edited, 0o644); err != nil {
		return err
	}
	if err := os.RemoveAll(filepath.Join(s.SuiteDir(), "tasks", id)); err != nil {
		return err
	}
	out := s.Tasks[:0]
	for _, t := range s.Tasks {
		if t.ID != id {
			out = append(out, t)
		}
	}
	s.Tasks = out
	return nil
}

// removeTaskID drops id from the tasks list of a suite.yaml.
func removeTaskID(src []byte, id string) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("suite.yaml is not a mapping")
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value != "tasks" || m.Content[i+1].Kind != yaml.SequenceNode {
			continue
		}
		seq := m.Content[i+1]
		var keep []*yaml.Node
		for _, n := range seq.Content {
			if n.Value != id {
				keep = append(keep, n)
			}
		}
		seq.Content = keep
		if len(keep) == 0 {
			seq.Style = yaml.FlowStyle
		}
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Publishable are the tasks publish copies: accepted, and ready unless
// includeUnready (then readiness runs again on the benchmark).
func (s *DraftSet) Publishable(ids []string, includeUnready bool) ([]*DraftTask, []Problem) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []*DraftTask
	var skipped []Problem
	for i := range s.Tasks {
		t := &s.Tasks[i]
		if len(want) > 0 && !want[t.ID] {
			continue
		}
		switch {
		case t.Review == ReviewRejected:
			skipped = append(skipped, Problem{Ref: t.ID, Message: "rejected in review"})
		case t.Review == ReviewPending:
			skipped = append(skipped, Problem{Ref: t.ID, Message: "pending review: accept it first"})
		case !t.Ready() && !includeUnready:
			why := "readiness has not run on it"
			if t.Readiness != nil {
				why = "not ready: " + t.Reason
			}
			skipped = append(skipped, Problem{Ref: t.ID, Message: why})
		default:
			out = append(out, t)
		}
	}
	return out, skipped
}
