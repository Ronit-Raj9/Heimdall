package emit

import (
	"sort"
	"time"

	"github.com/maximhq/bifrost/bench/issuebench"
)

// HeldOutPlan picks which issues are held out: those opened after After
// when it is set, else the newest Pct percent (rounded down, at least one
// once there are MinTasks or more).
type HeldOutPlan struct {
	Pct      int       `json:"pct"`
	After    time.Time `json:"after,omitempty"`
	MinTasks int       `json:"min_tasks"`
}

// DefaultHeldOut holds out the newest fifth once a repository has 5 tasks.
var DefaultHeldOut = HeldOutPlan{Pct: 20, MinTasks: 5}

// HeldOut returns the instance ids that go held out. Newer issues are the
// ones a model is least likely to have seen in training.
func HeldOut(cands []issuebench.Candidate, p HeldOutPlan) map[string]bool {
	out := map[string]bool{}
	if !p.After.IsZero() {
		for _, c := range cands {
			if c.IssueCreated.After(p.After) {
				out[InstanceID(c)] = true
			}
		}
		return out
	}
	if p.Pct <= 0 || len(cands) == 0 || len(cands) < p.MinTasks {
		return out
	}
	pct := p.Pct
	if pct > 90 {
		pct = 90
	}
	sorted := append([]issuebench.Candidate{}, cands...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if !a.IssueCreated.Equal(b.IssueCreated) {
			return a.IssueCreated.After(b.IssueCreated)
		}
		return InstanceID(a) < InstanceID(b)
	})
	n := len(sorted) * pct / 100
	if n == 0 {
		n = 1
	}
	for _, c := range sorted[:n] {
		out[InstanceID(c)] = true
	}
	return out
}
