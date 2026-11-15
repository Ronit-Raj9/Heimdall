// Package schedule runs experiments on a timetable: a schedules.yaml file in
// the project names an experiment file, a 5-field cron expression and a
// daily spend cap per schedule, and a daemon plans, runs and checks each one
// at its times, one at a time, then sends the alerts the result calls for
// (package alerts). Scheduling never changes how a run is measured: a
// scheduled run is an ordinary experiment, frozen, sealed and analysed like
// one started by hand.
package schedule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// cronSearchYears bounds Next: an expression that names no real time (the
// 30th of February) has no next time within it and Next returns zero.
const cronSearchYears = 5

// Cron is a parsed 5-field cron expression: minute, hour, day of month,
// month, day of week. Each field is a bit set of the values it allows.
type Cron struct {
	spec                          string
	minute, hour, dom, month, dow uint64
	domRestricted, dowRestricted  bool
}

// field describes one position of the expression.
type field struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	monthNames = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}
	dayNames   = map[string]int{"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}
	fields     = [5]field{
		{name: "minute", min: 0, max: 59},
		{name: "hour", min: 0, max: 23},
		{name: "day of month", min: 1, max: 31},
		{name: "month", min: 1, max: 12, names: monthNames},
		{name: "day of week", min: 0, max: 7, names: dayNames}, // 7 is Sunday too
	}
)

// ParseCron parses a standard 5-field expression. Each field is "*", a
// value, a range "a-b", a step "*/n" or "a-b/n" (or "a/n", from a to the
// end), or a comma list of those. Months and days of the week also take
// three-letter English names. Day of week 7 means Sunday, like 0. When both
// day of month and day of week are restricted (neither starts with "*"), a
// day matches when either one does, as in classic cron.
func ParseCron(spec string) (*Cron, error) {
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron %q: want 5 fields (minute hour day-of-month month day-of-week), got %d", spec, len(parts))
	}
	var sets [5]uint64
	for i, p := range parts {
		set, err := parseField(p, fields[i])
		if err != nil {
			return nil, fmt.Errorf("cron %q: %w", spec, err)
		}
		sets[i] = set
	}
	if sets[4]&(1<<7) != 0 {
		sets[4] = sets[4]&^(1<<7) | 1
	}
	return &Cron{
		spec: strings.Join(parts, " "), minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domRestricted: !strings.HasPrefix(parts[2], "*"), dowRestricted: !strings.HasPrefix(parts[4], "*"),
	}, nil
}

// String returns the expression with its fields separated by one space.
func (c *Cron) String() string { return c.spec }

// Next returns the first time strictly after after, to the minute, that the
// expression matches, in after's location. It returns the zero time when no
// time matches within cronSearchYears years. A local time skipped by a
// daylight saving change is never matched; one that repeats matches once.
func (c *Cron) Next(after time.Time) time.Time {
	loc := after.Location()
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.AddDate(cronSearchYears, 0, 0)
	for t.Before(limit) {
		switch {
		case c.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !c.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case c.hour&(1<<uint(t.Hour())) == 0:
			next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			if !next.After(t) { // a repeated hour at a daylight saving change
				next = t.Truncate(time.Hour).Add(time.Hour)
			}
			t = next
		case c.minute&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

// dayMatches applies the day-of-month and day-of-week fields.
func (c *Cron) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0
	if c.domRestricted && c.dowRestricted {
		return dom || dow
	}
	return dom && dow
}

// parseField parses one comma list into a bit set.
func parseField(s string, f field) (uint64, error) {
	var set uint64
	for _, item := range strings.Split(s, ",") {
		if item == "" {
			return 0, fmt.Errorf("%s: empty list item in %q", f.name, s)
		}
		lo, hi, step, err := parseItem(item, f)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", f.name, err)
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, nil
}

// parseItem parses "*", "v", "a-b", each optionally followed by "/n".
func parseItem(item string, f field) (lo, hi, step int, err error) {
	rangePart, stepPart, hasStep := strings.Cut(item, "/")
	step = 1
	if hasStep {
		step, err = strconv.Atoi(stepPart)
		if err != nil || step <= 0 {
			return 0, 0, 0, fmt.Errorf("step %q is not a positive integer", stepPart)
		}
	}
	switch {
	case rangePart == "*":
		lo, hi = f.min, f.max
		if f.max == 7 { // day of week: "*" means 0-6, 7 only repeats Sunday
			hi = 6
		}
	case strings.Contains(rangePart, "-"):
		a, b, _ := strings.Cut(rangePart, "-")
		if lo, err = value(a, f); err != nil {
			return 0, 0, 0, err
		}
		if hi, err = value(b, f); err != nil {
			return 0, 0, 0, err
		}
		if lo > hi {
			return 0, 0, 0, fmt.Errorf("range %q runs backwards", rangePart)
		}
	default:
		if lo, err = value(rangePart, f); err != nil {
			return 0, 0, 0, err
		}
		hi = lo
		if hasStep { // "a/n" runs from a to the end of the field
			hi = f.max
		}
	}
	return lo, hi, step, nil
}

// value parses one number or name and checks its range.
func value(s string, f field) (int, error) {
	if s == "" {
		return 0, errors.New("empty value")
	}
	if v, ok := f.names[strings.ToLower(s)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", s)
	}
	if v < f.min || v > f.max {
		return 0, fmt.Errorf("%d is outside %d-%d", v, f.min, f.max)
	}
	return v, nil
}
