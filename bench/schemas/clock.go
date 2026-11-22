package schemas

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"
)

// ClockSpec is a task's controllable run clock (Z17): the wall time the
// services and the agent see when a run starts, and how fast it moves. It
// turns "schedule it for tomorrow" tasks into deterministic ones: the date a
// task was written for stays the date it runs at.
//
//	clock: {start: 2026-03-14T09:00:00Z, rate: 1}
//
// The clock reaches every service and the agent as environment variables:
// BENCH_NOW (the start, RFC 3339 UTC), BENCH_CLOCK_RATE, and FAKETIME in
// libfaketime's absolute form, so a service image that preloads libfaketime
// (LD_PRELOAD) gets the clock in every dynamically linked process. Statically
// linked binaries (Go) ignore FAKETIME and Linux time namespaces do not cover
// CLOCK_REALTIME, so such a service must read BENCH_NOW itself. The bench never
// sets LD_PRELOAD: whether to preload is the image's choice.
//
// A task's clock wins over its suite's. The effective clock is hashed into
// the task lock (suite/scenario.go), so two runs with different clocks are
// never compared as the same task.
type ClockSpec struct {
	Start string  `json:"start" yaml:"start"`                   // RFC 3339, or YYYY-MM-DD (midnight UTC)
	Rate  float64 `json:"rate,omitempty" yaml:"rate,omitempty"` // seconds of clock time per real second; 0 means 1
}

// Clock environment variables.
const (
	EnvNow       = "BENCH_NOW"
	EnvClockRate = "BENCH_CLOCK_RATE"
	EnvFakeTime  = "FAKETIME"
)

// maxClockRate bounds the clock rate: a faster clock is a typo, not a task.
const maxClockRate = 1000

// Validate checks the start and the rate.
func (c *ClockSpec) Validate() error {
	if c == nil {
		return nil
	}
	if c.Start == "" {
		return errors.New("clock.start is required")
	}
	if _, err := parseStart(c.Start); err != nil {
		return fmt.Errorf("clock: %w", err)
	}
	if math.IsNaN(c.Rate) || c.Rate < 0 || c.Rate > maxClockRate {
		return fmt.Errorf("clock.rate %v must be between 0 and %d (0 means 1)", c.Rate, maxClockRate)
	}
	return nil
}

// StartTime is the parsed start, in UTC.
func (c *ClockSpec) StartTime() (time.Time, error) {
	if c == nil {
		return time.Time{}, errors.New("no clock")
	}
	return parseStart(c.Start)
}

// EffectiveRate is the rate with 0 read as 1.
func (c *ClockSpec) EffectiveRate() float64 {
	if c == nil || c.Rate == 0 {
		return 1
	}
	return c.Rate
}

// EffectiveClock is the task's clock, else its suite's, else nil.
func EffectiveClock(s *Suite, t *Task) *ClockSpec {
	if t != nil && t.Clock != nil {
		return t.Clock
	}
	if s != nil && s.Clock != nil {
		return s.Clock
	}
	return nil
}

// ClockEnv is the environment the clock gives a service or the agent: empty
// without a clock or with a clock that does not validate (validation reports
// it before any run).
func ClockEnv(s *Suite, t *Task) map[string]string {
	c := EffectiveClock(s, t)
	if c == nil || c.Validate() != nil {
		return map[string]string{}
	}
	start, _ := c.StartTime()
	rate := c.EffectiveRate()
	rs := strconv.FormatFloat(rate, 'f', -1, 64)
	fake := "@" + start.Format(time.DateTime)
	if rate != 1 {
		fake += " x" + rs
	}
	return map[string]string{
		EnvNow:       start.Format(time.RFC3339),
		EnvClockRate: rs,
		EnvFakeTime:  fake,
	}
}
