// Package statuspage is Heimdall's public status page (lane Z14): the
// health of each platform component, computed from live checks, and
// incident notes an operator writes with `bifrost-bench status incident`.
//
// The page is public, so it says only what a visitor may know: a level and
// a short fixed sentence per component, never an error string, an org, a
// host name or a count of anyone's runs. Checks run at most once per
// CacheFor however often the page is read, so the page cannot be used to
// load the database.
package statuspage

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Level is a component's or the platform's state, best first.
type Level string

// Levels, in order of severity (Worse).
const (
	Operational   Level = "operational"
	Maintenance   Level = "maintenance"
	Degraded      Level = "degraded"
	PartialOutage Level = "partial_outage"
	MajorOutage   Level = "major_outage"
	Unknown       Level = "unknown"
)

var rank = map[Level]int{Operational: 0, Unknown: 1, Maintenance: 2, Degraded: 3, PartialOutage: 4, MajorOutage: 5}

// Worse is the more severe of a and b.
func Worse(a, b Level) Level {
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// Valid reports a known level.
func (l Level) Valid() bool { _, ok := rank[l]; return ok }

// Component is one line of the page.
type Component struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Level  Level  `json:"level"`
	Detail string `json:"detail,omitempty"`
	// Incident is set when an open incident sets the level.
	Incident string `json:"incident,omitempty"`
}

// Page is GET /api/bench/status.
type Page struct {
	Level      Level       `json:"level"`
	Components []Component `json:"components"`
	// Incidents are open (not resolved) incidents and scheduled
	// maintenance, newest first.
	Incidents []Incident `json:"incidents"`
	// Recent are incidents resolved in the last RecentFor, newest first.
	Recent    []Incident `json:"recent"`
	Version   string     `json:"version,omitempty"`
	CheckedAt time.Time  `json:"checked_at"`
}

// Check is one live component check. Run returns the level and a short
// public sentence; it must honour ctx.
type Check struct {
	ID, Name string
	Run      func(ctx context.Context) (Level, string)
}

// Defaults.
const (
	CacheFor     = 15 * time.Second
	CheckTimeout = 4 * time.Second
	RecentFor    = 14 * 24 * time.Hour
)

// Service builds the page from checks and the incident store.
type Service struct {
	checks  []Check
	store   *Store
	version string
	now     func() time.Time

	mu        sync.Mutex
	cached    []Component
	checkedAt time.Time
	auto      *autoIncidents // nil: no automatic incidents (auto.go)
}

// WithAutoIncidents makes the service open and resolve automatic incidents
// from its fresh check runs (auto.go). It needs a store.
func (s *Service) WithAutoIncidents() *Service {
	if s.store != nil {
		s.auto = newAutoIncidents(s.store)
	}
	return s
}

// Watch runs the checks every interval until ctx ends, so automatic
// incidents open and resolve while nobody reads the page. The first run is
// one interval after the start: a server that just started has nothing to
// report yet, and a short-lived server (a test) never runs the checks in
// the background.
func (s *Service) Watch(ctx context.Context, every time.Duration) {
	if every < CacheFor {
		every = CacheFor
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.components(ctx)
	}
}

// New returns a service. store may be nil (no incidents).
func New(checks []Check, store *Store, version string) *Service {
	return &Service{checks: checks, store: store, version: version, now: time.Now}
}

// Store is the incident store (nil without one).
func (s *Service) Store() *Store { return s.store }

// Page computes the page: cached checks, then open incidents raise the
// level of the components they name.
func (s *Service) Page(ctx context.Context) Page {
	comps, at := s.components(ctx)
	p := Page{Level: Operational, Version: s.version, CheckedAt: at, Incidents: []Incident{}, Recent: []Incident{}}
	var incidents []Incident
	if s.store != nil {
		incidents, _ = s.store.List()
	}
	now := s.now()
	byID := map[string]int{}
	for i, c := range comps {
		byID[c.ID] = i
	}
	for _, in := range incidents {
		switch {
		case in.Status == StatusResolved:
			if in.ResolvedAt != nil && now.Sub(*in.ResolvedAt) <= RecentFor {
				p.Recent = append(p.Recent, in)
			}
			continue
		case in.Status == StatusScheduled && in.ScheduledFor != nil && in.ScheduledFor.After(now):
			p.Incidents = append(p.Incidents, in) // announced, not in effect yet
			continue
		}
		p.Incidents = append(p.Incidents, in)
		lvl := in.Impact.Level()
		for _, id := range in.Components {
			if i, ok := byID[id]; ok && rank[lvl] > rank[comps[i].Level] {
				comps[i].Level, comps[i].Incident = lvl, in.ID
			}
		}
		if len(in.Components) == 0 {
			p.Level = Worse(p.Level, lvl)
		}
	}
	for _, c := range comps {
		p.Level = Worse(p.Level, c.Level)
	}
	sort.SliceStable(p.Incidents, func(i, j int) bool { return p.Incidents[i].StartedAt.After(p.Incidents[j].StartedAt) })
	sort.SliceStable(p.Recent, func(i, j int) bool { return p.Recent[i].ResolvedAt.After(*p.Recent[j].ResolvedAt) })
	p.Components = comps
	if p.Components == nil {
		p.Components = []Component{}
	}
	return p
}

// components runs the checks at most once per CacheFor and returns a copy.
func (s *Service) components(ctx context.Context) ([]Component, time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if s.cached != nil && now.Sub(s.checkedAt) < CacheFor {
		return append([]Component(nil), s.cached...), s.checkedAt
	}
	out := make([]Component, len(s.checks))
	var wg sync.WaitGroup
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CheckTimeout)
	defer cancel()
	for i, c := range s.checks {
		out[i] = Component{ID: c.ID, Name: c.Name, Level: Unknown}
		wg.Add(1)
		go func(i int, c Check) {
			defer wg.Done()
			defer func() {
				if recover() != nil {
					out[i].Level, out[i].Detail = Unknown, "check failed"
				}
			}()
			lvl, detail := c.Run(cctx) // checks honour cctx, so the page waits at most CheckTimeout
			if !lvl.Valid() {
				lvl = Unknown
			}
			out[i].Level, out[i].Detail = lvl, detail
		}(i, c)
	}
	wg.Wait()
	s.cached, s.checkedAt = out, now
	s.auto.observe(out, now)
	return append([]Component(nil), out...), now
}
