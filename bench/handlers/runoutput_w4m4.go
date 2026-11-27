package handlers

import (
	"cmp"
	"context"
	"strings"

	"github.com/maximhq/bifrost/bench/schemas"
)

// suiteFactsOf is what a run borrows from its suite: the track, the grading,
// the output kind of the suite and of each task that sets its own, and the
// fault name of each probe. The run list and the run page both use it, so a
// run shows the same output kind in both places.
func suiteFactsOf(s *schemas.Suite) suiteFacts {
	facts := suiteFacts{track: cmp.Or(s.Track, schemas.TrackCoding), output: "diff", grading: cmp.Or(s.Grading, "verified"), tasks: map[string]string{}, probes: map[string]string{}, titles: map[string]string{}}
	for _, p := range s.Probes {
		if name := familyFault(p.PS2Family); name != "" {
			facts.probes[p.ID] = name
		}
	}
	if s.Output != nil && s.Output.Kind != "" {
		facts.output = s.Output.Kind
	}
	for _, t := range s.Tasks {
		if t != nil && strings.TrimSpace(t.Title) != "" {
			facts.titles[t.ID] = strings.TrimSpace(t.Title)
		}
		if t != nil && t.Output != nil && t.Output.Kind != "" {
			facts.tasks[t.ID] = t.Output.Kind
		}
	}
	return facts
}

// runOutputKindFor is the output kind of a run of task: the task's own kind,
// else the suite's.
func runOutputKindFor(facts suiteFacts, task string) string {
	return cmp.Or(facts.tasks[task], facts.output)
}

// loadRunOutputKind loads the suite of one run and gives its output kind, or ""
// when there is no suite source or the suite does not load in time. An empty
// kind is left out of the answer, never guessed.
func loadRunOutputKind(ctx context.Context, suites SuiteSource, slot schemas.Slot) string {
	kind, _ := loadRunSuiteFacts(ctx, suites, slot)
	return kind
}

// loadRunSuiteFacts loads the suite of one run once and gives its output
// kind and the task's title; both "" when the suite does not load in time.
func loadRunSuiteFacts(ctx context.Context, suites SuiteSource, slot schemas.Slot) (kind, taskTitle string) {
	if suites == nil || slot.Suite == "" {
		return "", ""
	}
	one, cancel := context.WithTimeout(ctx, runListSuiteBudget)
	defer cancel()
	s, err := suites.LoadSuite(one, slot.Suite)
	if err != nil || s == nil {
		return "", ""
	}
	facts := suiteFactsOf(s)
	return runOutputKindFor(facts, slot.Task), facts.titles[slot.Task]
}
