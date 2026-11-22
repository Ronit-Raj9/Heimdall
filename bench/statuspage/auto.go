package statuspage

// Automatic incidents (fix wave 2, N148): a component that stays degraded or
// worse for AutoOpenAfter opens an incident in the same store an operator
// writes to, marked Auto; it is resolved after AutoResolveAfter back at
// operational. An operator's incident is never opened, changed or resolved
// here. The streaks live in memory, so a restart starts them again; the
// open automatic incidents are found in the store, so a restart never opens
// a second one for the same component.

import (
	"sync"
	"time"
)

// Automatic incident thresholds.
const (
	AutoOpenAfter    = 5 * time.Minute
	AutoResolveAfter = 5 * time.Minute
)

// Fixed public texts of automatic incidents.
const (
	autoOpenMessage    = "Found by an automatic check."
	autoResolveMessage = "Resolved: the automatic check passes again."
	autoNoDetail       = "not working normally"
)

// autoIncidents tracks each component's bad and good streaks.
type autoIncidents struct {
	store *Store

	mu      sync.Mutex
	streaks map[string]*autoStreak
}

type autoStreak struct {
	badSince, goodSince time.Time
}

func newAutoIncidents(store *Store) *autoIncidents {
	return &autoIncidents{store: store, streaks: map[string]*autoStreak{}}
}

// autoImpact is the incident impact of a check level.
func autoImpact(l Level) Impact {
	switch l {
	case MajorOutage:
		return ImpactCritical
	case PartialOutage:
		return ImpactMajor
	}
	return ImpactMinor
}

// bad reports a level that counts towards opening an incident.
func autoBad(l Level) bool { return rank[l] >= rank[Degraded] && l != Maintenance }

// observe feeds one fresh check run taken at at.
func (a *autoIncidents) observe(comps []Component, at time.Time) {
	if a == nil || a.store == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	var open map[string]Incident // open automatic incidents by component, read once when needed
	openAuto := func() map[string]Incident {
		if open != nil {
			return open
		}
		open = map[string]Incident{}
		list, err := a.store.List()
		if err != nil {
			return open
		}
		for _, in := range list {
			if in.Auto && in.Status != StatusResolved && len(in.Components) == 1 {
				if _, seen := open[in.Components[0]]; !seen {
					open[in.Components[0]] = in
				}
			}
		}
		return open
	}
	for _, c := range comps {
		s := a.streaks[c.ID]
		if s == nil {
			s = &autoStreak{}
			a.streaks[c.ID] = s
		}
		switch {
		case autoBad(c.Level):
			s.goodSince = time.Time{}
			if s.badSince.IsZero() {
				s.badSince = at
			}
			if at.Sub(s.badSince) < AutoOpenAfter {
				continue
			}
			if _, ok := openAuto()[c.ID]; ok {
				continue
			}
			detail := c.Detail
			if detail == "" {
				detail = autoNoDetail
			}
			if in, err := a.store.Open(Open{Title: c.Name + ": " + detail, Impact: autoImpact(c.Level), Components: []string{c.ID},
				Message: autoOpenMessage, Auto: true}); err == nil {
				open[c.ID] = in
			}
		case c.Level == Operational:
			s.badSince = time.Time{}
			if s.goodSince.IsZero() {
				s.goodSince = at
			}
			if at.Sub(s.goodSince) < AutoResolveAfter {
				continue
			}
			in, ok := openAuto()[c.ID]
			if !ok || len(in.Updates) > 1 { // an operator who added a note owns the incident from then on
				continue
			}
			if _, err := a.store.Update(in.ID, Change{Status: StatusResolved, Message: autoResolveMessage}); err == nil {
				delete(open, c.ID)
			}
		default: // unknown or maintenance: neither streak goes on
			s.badSince, s.goodSince = time.Time{}, time.Time{}
		}
	}
}
