package findings

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maximhq/bifrost/bench/schemas"
)

// System is the name history events carry when Heimdall made the change.
const System = "Heimdall"

// RunRow is one sealed run, as the findings rules need it. The handler fills
// it from the org's ReadIndex (no results index dependency).
type RunRow struct {
	Experiment        string    `json:"experiment"`
	ExperimentCreated time.Time `json:"experiment_created"`
	RunID             string    `json:"run_id"`
	Suite             string    `json:"suite"` // without "@version"
	Task              string    `json:"task"`
	Arm               string    `json:"arm"`
	Agent             string    `json:"agent"` // Manifest.Arms[arm].Config.ID
	ConfigHash        string    `json:"config_hash"`
	Passed            *bool     `json:"passed"` // nil for infrastructure outcomes that do not score
}

// Add stores f as a new open finding. An existing finding with the same Key
// is returned unchanged with created false (no duplicate).
func Add(st Store, f Finding, by string, now time.Time) (*Finding, bool, error) {
	if strings.TrimSpace(f.Key) == "" {
		return nil, false, fmt.Errorf("%w: a finding needs a key", ErrInvalid)
	}
	f.ID = IDOf(f.Key)
	f.State = StateOpen
	if len(f.Tasks) > MaxTasks {
		f.Tasks = f.Tasks[:MaxTasks]
	}
	if len(f.Runs) > MaxRuns {
		f.Runs = f.Runs[:MaxRuns]
	}
	if f.Tasks == nil {
		f.Tasks = []TaskRef{}
	}
	if f.Runs == nil {
		f.Runs = []string{}
	}
	f.Fix, f.Reruns = nil, nil
	f.History = []Event{{At: now, By: by, To: StateOpen, Reason: "Tracked as a finding."}}
	f.CreatedAt, f.UpdatedAt = now, now
	var out *Finding
	created := false
	err := st.Update(func(all []Finding) ([]Finding, error) {
		for i := range all {
			if all[i].Key == f.Key {
				c := all[i]
				out = &c
				return all, nil
			}
		}
		all = append(all, f)
		c := f
		out, created = &c, true
		return all, nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, created, nil
}

// Update runs fn on finding id under the store lock and saves it.
func Update(st Store, id string, fn func(f *Finding) error) (*Finding, error) {
	var out *Finding
	err := st.Update(func(all []Finding) ([]Finding, error) {
		for i := range all {
			if all[i].ID != id {
				continue
			}
			if err := fn(&all[i]); err != nil {
				return nil, err
			}
			c := all[i]
			out = &c
			return all, nil
		}
		return nil, ErrNotFound
	})
	return out, err
}

func move(f *Finding, ev Event) {
	ev.From = f.State
	if ev.To == "" {
		ev.To = f.State
	}
	f.State = ev.To
	f.History = append(f.History, ev)
	f.UpdatedAt = ev.At
}

func checkReason(reason string) (string, error) {
	r := strings.TrimSpace(reason)
	if n := utf8.RuneCountInString(r); n < 3 || n > 500 {
		return "", fmt.Errorf("%w: give a reason of 3 to 500 characters", ErrInvalid)
	}
	return r, nil
}

// MarkExpected needs a reason of 3 to 500 chars.
func MarkExpected(st Store, id, reason, by string, now time.Time) (*Finding, error) {
	r, err := checkReason(reason)
	if err != nil {
		return nil, err
	}
	return Update(st, id, func(f *Finding) error {
		move(f, Event{At: now, By: by, To: StateExpected, Reason: r})
		return nil
	})
}

// Reopen is a person reopening a fixed or expected finding.
func Reopen(st Store, id, reason, by string, now time.Time) (*Finding, error) {
	r, err := checkReason(reason)
	if err != nil {
		return nil, err
	}
	return Update(st, id, func(f *Finding) error {
		if f.State == StateOpen || f.State == StateReopened {
			return fmt.Errorf("%w: this finding is already open", ErrInvalid)
		}
		move(f, Event{At: now, By: by, To: StateReopened, Reason: r})
		return nil
	})
}

// FixedSentence is the line a fixed finding shows.
func FixedSentence(p *FixProof) string {
	return fmt.Sprintf("Fixed: the fixed agent passed %d of %d runs; the old agent passed %d of %d.", p.FixedPassed, p.FixedRuns, p.OldPassed, p.OldRuns)
}

// Judge decides a finished rerun. rows are the rerun experiment's run rows.
// Fixed when: every linked task has at least one fixed-arm run, every
// fixed-arm run passed (Passed true), the fixed arm has at least 3 runs in
// all, and the old arm failed at least one run. Old arm also passing
// everything: stays open, event "The old agent also passed these tasks, so
// they may not show the failure. Add a task that fails without the fix."
// Fixed arm failing any run: stays open, event "The fixed agent still fails
// {k} of {n} runs."
func Judge(st Store, id string, rerunExp string, oldArm, fixedArm string, rows []RunRow, now time.Time) (*Finding, error) {
	return Update(st, id, func(f *Finding) error {
		p := FixProof{Experiment: rerunExp, At: now}
		covered := map[string]bool{}
		for _, r := range rows {
			if r.Experiment != rerunExp || r.Passed == nil {
				continue
			}
			switch r.Arm {
			case fixedArm:
				p.FixedRuns++
				if *r.Passed {
					p.FixedPassed++
				}
				p.Agent, p.FixedHash = r.Agent, r.ConfigHash
				covered[TaskRef{r.Suite, r.Task}.String()] = true
			case oldArm:
				p.OldRuns++
				if *r.Passed {
					p.OldPassed++
				}
				p.OldHash = r.ConfigHash
			}
		}
		ev := Event{At: now, By: System, Experiment: rerunExp}
		missing := slices.ContainsFunc(f.Tasks, func(t TaskRef) bool { return !covered[t.String()] })
		switch {
		case p.FixedRuns > p.FixedPassed:
			ev.Reason = fmt.Sprintf("The fixed agent still fails %d of %d runs.", p.FixedRuns-p.FixedPassed, p.FixedRuns)
		case p.FixedRuns < 3 || missing:
			ev.Reason = fmt.Sprintf("Not enough runs to decide yet: the fixed agent finished %d runs, and it needs at least 3 that cover every task of this finding.", p.FixedRuns)
		case p.OldRuns == p.OldPassed:
			ev.Reason = "The old agent also passed these tasks, so they may not show the failure. Add a task that fails without the fix."
		default:
			ev.To = StateFixed
			proof := p
			f.Fix = &proof
			ev.Reason = FixedSentence(&proof)
		}
		move(f, ev)
		return nil
	})
}

// Refresh reopens fixed findings whose tasks failed again: a run row of a
// linked task, of the same agent (RunRow.Agent == Fix.Agent), in an
// experiment created after Fix.At and not in f.Reruns, with Passed false.
// Event reason "Task {suite}/{task} failed again in {experiment} (run
// {run})". For inbox findings, an item whose LastRecurrence is after Fix.At
// reopens with "The same failure came back in production on {date}."
// Refresh is idempotent and runs under the store lock. rows takes
// "suite/task".
func Refresh(st Store, rows func(task string) ([]RunRow, error), inbox func(itemID string) (*schemas.InboxItem, error), now time.Time) (changed int, err error) {
	err = st.Update(func(all []Finding) ([]Finding, error) {
		changed = 0
		for i := range all {
			f := &all[i]
			if f.State != StateFixed || f.Fix == nil {
				continue
			}
			ev, err := reopenEvent(f, rows, inbox)
			if err != nil {
				return nil, err
			}
			if ev == nil {
				continue
			}
			ev.At, ev.By, ev.To = now, System, StateReopened
			move(f, *ev)
			changed++
		}
		return all, nil
	})
	return changed, err
}

func reopenEvent(f *Finding, rows func(string) ([]RunRow, error), inbox func(string) (*schemas.InboxItem, error)) (*Event, error) {
	for _, t := range f.Tasks {
		rs, err := rows(t.String())
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			if r.Passed == nil || *r.Passed || r.Agent != f.Fix.Agent || !r.ExperimentCreated.After(f.Fix.At) || slices.Contains(f.Reruns, r.Experiment) || r.Experiment == f.Fix.Experiment {
				continue
			}
			return &Event{Reason: fmt.Sprintf("Task %s/%s failed again in %s (run %s)", r.Suite, r.Task, r.Experiment, r.RunID), Experiment: r.Experiment, RunID: r.RunID}, nil
		}
	}
	if item, ok := strings.CutPrefix(f.Key, "inbox:"); ok && inbox != nil {
		it, err := inbox(item)
		if err != nil {
			return nil, err
		}
		if it != nil && it.LastRecurrence != nil && it.LastRecurrence.After(f.Fix.At) {
			return &Event{Reason: "The same failure came back in production on " + it.LastRecurrence.UTC().Format("2 Jan 2006") + "."}, nil
		}
	}
	return nil, nil
}
