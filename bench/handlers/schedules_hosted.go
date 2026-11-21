package handlers

// Gap lane G14: hosted schedule guardrails. A hosted server runs every org's
// schedules on runners through the org launcher; a per-org cap on the number
// of schedules and a minimum interval between two fires keep one org from
// flooding the shared runners.

import (
	"errors"
	"fmt"
	"time"

	"github.com/maximhq/bifrost/bench/schedule"
)

const (
	// HostedMaxSchedules caps one org's schedules (web and scheduled
	// canaries together) on a hosted server.
	HostedMaxSchedules = 20
	// HostedMinInterval is the shortest gap between two fires of one
	// schedule on a hosted server.
	HostedMinInterval = 15 * time.Minute
)

// ScheduleLimits are the hosted guardrails, shown on the Schedules page.
type ScheduleLimits struct {
	MaxSchedules   int `json:"max_schedules"`
	MinIntervalMin int `json:"min_interval_min"`
}

// HostedScheduleLimits returns the hosted guardrails.
func HostedScheduleLimits() ScheduleLimits {
	return ScheduleLimits{MaxSchedules: HostedMaxSchedules, MinIntervalMin: int(HostedMinInterval / time.Minute)}
}

// MinCronInterval returns the shortest gap between two consecutive fires of
// cron spec in loc, looking at the fires of the next eight days from from. It
// stops early once a gap below stopBelow is seen. A cron that fires at most
// once in that window returns a gap of eight days.
func MinCronInterval(spec string, loc *time.Location, from time.Time, stopBelow time.Duration) (time.Duration, error) {
	c, err := schedule.ParseCron(spec)
	if err != nil {
		return 0, err
	}
	if loc == nil {
		loc = time.UTC
	}
	window := 8 * 24 * time.Hour
	end := from.Add(window)
	minGap := window
	prev := c.Next(from.In(loc))
	for i := 0; i < 20000 && !prev.IsZero() && prev.Before(end); i++ {
		next := c.Next(prev)
		if next.IsZero() {
			break
		}
		if gap := next.Sub(prev); gap < minGap {
			minGap = gap
			if gap < stopBelow {
				break
			}
		}
		prev = next
	}
	return minGap, nil
}

// CheckHostedSchedule refuses a schedule a hosted server will not fire: one
// that fires more often than HostedMinInterval, or a built-in job schedule
// (inbox-pull, livescore-pull), which calls a gateway from the server.
func CheckHostedSchedule(s schedule.Schedule, now time.Time) error {
	if s.Job != "" {
		return fmt.Errorf("schedule %s: built-in job schedules (%s) call a gateway and run only on a local server; schedule an experiment or a canary instead", s.Name, s.Job)
	}
	loc, err := s.Location()
	if err != nil {
		return fmt.Errorf("schedule %s: %w", s.Name, err)
	}
	gap, err := MinCronInterval(s.Cron, loc, now, HostedMinInterval)
	if err != nil {
		return fmt.Errorf("schedule %s: %w", s.Name, err)
	}
	if gap < HostedMinInterval {
		return fmt.Errorf("schedule %s fires every %s; hosted schedules fire at most every %d minutes (for example */15 * * * *)",
			s.Name, gap, int(HostedMinInterval/time.Minute))
	}
	return nil
}

// CheckHostedSchedules applies the hosted guardrails to a whole file (web
// schedules plus scheduled canaries).
func CheckHostedSchedules(f *schedule.File, now time.Time) error {
	if f == nil {
		return nil
	}
	if len(f.Schedules) > HostedMaxSchedules {
		return fmt.Errorf("at most %d schedules per organization on this server (scheduled canaries count too); remove one first", HostedMaxSchedules)
	}
	var errs []error
	for _, s := range f.Schedules {
		if err := CheckHostedSchedule(s, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
