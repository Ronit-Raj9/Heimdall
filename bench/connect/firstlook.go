package connect

import (
	"errors"
	"fmt"
	"sort"
)

// First look (section 4.11): one benchmark, 5 tasks, 1 trial, clean, score
// mode, concurrency 5, on the fastest compatible benchmark. Its report card
// says it is too small for a verdict; it is never a release gate.

// First-look sizes.
const (
	FirstLookPreset      = "first_look"
	FirstLookTasks       = 5
	FirstLookTrials      = 1
	FirstLookConcurrency = 5
)

// FirstLookNote is the line every first-look report card shows.
const FirstLookNote = "First look: 5 tasks. Too small for a verdict; run the full pack for one."

// BenchmarkInfo is one benchmark the picker considers.
type BenchmarkInfo struct {
	ID         string `json:"id"`
	Compatible bool   `json:"compatible"`
	Reason     string `json:"reason,omitempty"` // why not compatible
	// NeedsModel is true when the grader or the simulated user calls a
	// model (a judge, a user simulator): it needs an org provider key.
	NeedsModel bool `json:"needs_model"`
	Tasks      int  `json:"tasks"`
	// EstimateS is the expected wall time of one task in seconds, 0 when
	// unknown; EstimateBasis says where it comes from.
	EstimateS     float64 `json:"estimate_s"`
	EstimateBasis string  `json:"estimate_basis,omitempty"`
	// Reference is true when the reference board has a row for it.
	Reference bool `json:"reference,omitempty"`
	// Practice is true for a small made-up practice benchmark (the Acme
	// examples, coding-core): picked only when nothing else fits.
	Practice bool `json:"practice,omitempty"`
}

// FirstLookPick is the picker's answer.
type FirstLookPick struct {
	Benchmark   string          `json:"benchmark"`
	Reason      string          `json:"reason"`
	Tasks       int             `json:"tasks"`
	Trials      int             `json:"trials"`
	Concurrency int             `json:"concurrency"`
	EstimateS   float64         `json:"estimate_s"` // whole first look, tasks run 5 at a time
	Note        string          `json:"note"`
	Considered  []BenchmarkInfo `json:"considered"`
	// Practice is true when the pick is a practice benchmark because no
	// other benchmark fits this agent.
	Practice bool `json:"practice,omitempty"`
}

// ErrNoFirstLook is the picker's error when no benchmark fits.
var ErrNoFirstLook = errors.New("no benchmark on this server fits this agent for a first look")

// PickFirstLook picks the first-look benchmark: compatible, with at least
// one task, graded without a model when the org has no provider key, then
// the fastest by estimate (unknown estimates last), a reference row as the
// tie breaker, then the id. Practice benchmarks are considered only when no
// other benchmark fits.
func PickFirstLook(infos []BenchmarkInfo, hasOrgKey bool) (FirstLookPick, error) {
	var ok, practice []BenchmarkInfo
	for _, b := range infos {
		if !b.Compatible || b.Tasks == 0 || (b.NeedsModel && !hasOrgKey) {
			continue
		}
		if b.Practice {
			practice = append(practice, b)
			continue
		}
		ok = append(ok, b)
	}
	usePractice := len(ok) == 0 && len(practice) > 0
	if usePractice {
		ok = practice
	}
	if len(ok) == 0 {
		if !hasOrgKey {
			for _, b := range infos {
				if b.Compatible && b.Tasks > 0 {
					return FirstLookPick{}, fmt.Errorf("%w: the compatible ones use a judge or simulated user model; add a provider key (Settings > Models)", ErrNoFirstLook)
				}
			}
		}
		return FirstLookPick{}, ErrNoFirstLook
	}
	sort.SliceStable(ok, func(i, j int) bool {
		a, b := ok[i], ok[j]
		if (a.EstimateS == 0) != (b.EstimateS == 0) {
			return a.EstimateS != 0
		}
		if a.EstimateS != b.EstimateS {
			return a.EstimateS < b.EstimateS
		}
		if a.Reference != b.Reference {
			return a.Reference
		}
		return a.ID < b.ID
	})
	best := ok[0]
	tasks := FirstLookTasks
	if best.Tasks < tasks {
		tasks = best.Tasks
	}
	reason := "compatible with this agent"
	if best.EstimateS > 0 {
		reason += fmt.Sprintf(", about %s per task (%s), the fastest of %d", humanS(best.EstimateS), best.EstimateBasis, len(ok))
	}
	if !best.NeedsModel {
		reason += ", graded without a model"
	}
	if best.Reference {
		reason += ", with a reference row"
	}
	if usePractice {
		reason += "; a practice benchmark: nothing else fits this agent"
	}
	est := 0.0
	if best.EstimateS > 0 {
		waves := (tasks + FirstLookConcurrency - 1) / FirstLookConcurrency
		est = best.EstimateS * float64(waves)
	}
	if len(ok) > 8 {
		ok = ok[:8]
	}
	return FirstLookPick{Benchmark: best.ID, Reason: reason, Tasks: tasks, Trials: FirstLookTrials, Concurrency: FirstLookConcurrency,
		EstimateS: est, Note: FirstLookNote, Considered: ok, Practice: usePractice}, nil
}

func humanS(s float64) string {
	if s < 90 {
		return fmt.Sprintf("%.0f s", s)
	}
	return fmt.Sprintf("%.0f min", s/60)
}
