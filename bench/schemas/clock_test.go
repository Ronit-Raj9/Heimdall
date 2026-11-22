package schemas

import "testing"

func TestClockEnv(t *testing.T) {
	s := &Suite{Clock: &ClockSpec{Start: "2026-03-14"}}
	task := &Task{}
	env := ClockEnv(s, task)
	if env[EnvNow] != "2026-03-14T00:00:00Z" || env[EnvClockRate] != "1" || env[EnvFakeTime] != "@2026-03-14 00:00:00" {
		t.Fatalf("suite clock: %v", env)
	}
	task.Clock = &ClockSpec{Start: "2026-03-14T09:30:00+02:00", Rate: 60}
	env = ClockEnv(s, task)
	if env[EnvNow] != "2026-03-14T07:30:00Z" || env[EnvClockRate] != "60" || env[EnvFakeTime] != "@2026-03-14 07:30:00 x60" {
		t.Fatalf("task clock wins: %v", env)
	}
	if len(ClockEnv(&Suite{}, &Task{})) != 0 {
		t.Fatal("no clock, no env")
	}
	if len(ClockEnv(nil, &Task{Clock: &ClockSpec{Start: "soon"}})) != 0 {
		t.Fatal("invalid clock gives no env")
	}
}

func TestClockValidate(t *testing.T) {
	for _, c := range []ClockSpec{{}, {Start: "tomorrow"}, {Start: "2026-01-01", Rate: -1}, {Start: "2026-01-01", Rate: 5000}} {
		if c.Validate() == nil {
			t.Errorf("%+v accepted", c)
		}
	}
	var nilClock *ClockSpec
	if nilClock.Validate() != nil || nilClock.EffectiveRate() != 1 {
		t.Error("nil clock")
	}
	if (&ClockSpec{Start: "2026-01-01T00:00:00Z", Rate: 0.5}).Validate() != nil {
		t.Error("valid clock refused")
	}
}
