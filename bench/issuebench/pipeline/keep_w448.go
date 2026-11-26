package pipeline

import "slices"

// CapReachedReason is the reason an item gets when its run reached the
// weekly spending cap before the item was built.
const CapReachedReason = "the weekly spending cap is reached"

// Keep removes every item whose pull request is not in prs and adds their
// count to the funnel reason "already_a_task". The weekly sync calls it after
// it drops the fixes already seen or already turned into tasks.
func (m *Manager) Keep(id string, prs []int) (*State, error) {
	return m.update(id, func(s *State) error {
		kept := s.Items[:0]
		dropped := 0
		for _, it := range s.Items {
			if slices.Contains(prs, it.Candidate.PR) {
				kept = append(kept, it)
			} else {
				dropped++
			}
		}
		s.Items = kept
		if dropped > 0 {
			if s.Funnel.Reasons == nil {
				s.Funnel.Reasons = map[string]int{}
			}
			s.Funnel.Reasons["already_a_task"] += dropped
		}
		return nil
	})
}

// Limit sets a run's spending cap and the builder cap of each of its items.
// Zero leaves a value as it is.
func (m *Manager) Limit(id string, capUSD, perItemUSD float64) (*State, error) {
	return m.update(id, func(s *State) error {
		if capUSD > 0 {
			s.CapUSD = capUSD
		}
		if perItemUSD > 0 {
			for i := range s.Items {
				s.Items[i].Options.BudgetUSD = perItemUSD
			}
		}
		return nil
	})
}

// capReached marks item i skipped and reports true when the run has a cap
// and its spend reached it, so step starts no new build.
func (m *Manager) capReached(id string, i int) bool {
	s, err := m.Load(id)
	if err != nil || s.CapUSD <= 0 || s.CostUSD < s.CapUSD || i >= len(s.Items) {
		return false
	}
	switch s.Items[i].Status {
	case StatusQueued, StatusBuilding, StatusBuilt:
	default:
		return false
	}
	m.setItem(id, i, StatusFailed, CapReachedReason, "", nil)
	return true
}
