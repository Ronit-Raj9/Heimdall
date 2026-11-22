package livemon

import (
	"strings"
	"testing"

	"github.com/maximhq/bifrost/bench/schedule"
)

func TestNormalize(t *testing.T) {
	ok := func() Monitor {
		return Monitor{ID: "mon_0123456789ab", Agent: "support-bot", Benchmark: "tau-retail", BudgetUSDPerDay: 5}
	}
	m := ok()
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	if m.Tasks != DefaultTasks || m.IntervalMin != DefaultIntervalMin || m.Name != "support-bot on tau-retail" {
		t.Fatalf("defaults: %+v", m)
	}
	tests := []struct {
		name string
		edit func(*Monitor)
		want string
	}{
		{"no agent", func(m *Monitor) { m.Agent = "" }, "pick an agent"},
		{"too many tasks", func(m *Monitor) { m.Tasks = 6 }, "1 to 5 tasks"},
		{"interval below minimum", func(m *Monitor) { m.IntervalMin = 5 }, "interval_min"},
		{"odd interval", func(m *Monitor) { m.IntervalMin = 45 }, "interval_min"},
		{"no budget", func(m *Monitor) { m.BudgetUSDPerDay = 0 }, "budget_usd_per_day"},
		{"budget below a cent per check", func(m *Monitor) { m.BudgetUSDPerDay = 0.5; m.IntervalMin = 15 }, "less than one cent"},
		{"bad rule", func(m *Monitor) { m.Rule.MinDrop = 2 }, "min_drop"},
		{"bad latency factor", func(m *Monitor) { m.Rule.LatencyFactor = 1.01 }, "latency_factor"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := ok()
			tc.edit(&m)
			err := m.Normalize()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCronFiresAtInterval(t *testing.T) {
	for _, iv := range Intervals {
		m := Monitor{ID: "mon_" + RandHex(6), IntervalMin: iv}
		c, err := schedule.ParseCron(m.Cron())
		if err != nil {
			t.Fatalf("interval %d: cron %q: %v", iv, m.Cron(), err)
		}
		_ = c
		f := &schedule.Schedule{Name: m.ScheduleName(), Cron: m.Cron(), Timezone: "UTC"}
		loc, _ := f.Location()
		from := mustTime("2026-11-22T00:00:00Z").In(loc)
		a := c.Next(from)
		b := c.Next(a)
		if got := int(b.Sub(a).Minutes()); got != iv {
			t.Fatalf("interval %d: cron %q fires %d minutes apart", iv, m.Cron(), got)
		}
	}
}

func TestBudgetPerCheck(t *testing.T) {
	m := Monitor{IntervalMin: 60, BudgetUSDPerDay: 5}
	if got := m.CheckBudgetUSD(); got != 0.2 {
		t.Fatalf("check budget = %v", got)
	}
	if m.ChecksPerDay() != 24 {
		t.Fatal(m.ChecksPerDay())
	}
}

func TestScheduleName(t *testing.T) {
	m := Monitor{ID: "mon_0123456789ab"}
	id, ok := MonitorIDOfSchedule(m.ScheduleName())
	if !ok || id != m.ID {
		t.Fatalf("%q -> %q %v", m.ScheduleName(), id, ok)
	}
	if _, ok := MonitorIDOfSchedule("nightly"); ok {
		t.Fatal("nightly is not a monitor schedule")
	}
	if len(m.ScheduleName()) > 64 {
		t.Fatal("schedule name too long")
	}
}

func TestSummarize(t *testing.T) {
	yes, no := true, false
	ms := func(v int64) *int64 { return &v }
	usd := func(v float64) *float64 { return &v }
	s := Sample{Runs: []RunSample{
		{RunID: "r1", Pass: &yes, LatencyMS: ms(100), CostUSD: usd(0.01)},
		{RunID: "r2", Pass: &no, LatencyMS: ms(300), CostUSD: usd(0.02)},
		{RunID: "r3", Infra: true, Error: "dial tcp: connection refused"},
	}}
	s.Summarize()
	if s.Status != SampleOK || s.Passed != 1 || s.Scored != 2 || s.Errors != 1 || s.CostUnknown != 1 {
		t.Fatalf("%+v", s)
	}
	if *s.P50MS != 100 || *s.P95MS != 300 || s.CostUSD != 0.03 {
		t.Fatalf("percentiles/cost %+v", s)
	}
	down := Sample{Runs: []RunSample{{Infra: true}, {Infra: true}, {Pass: &yes}}}
	down.Summarize()
	if down.Status != SampleInfra {
		t.Fatal(down.Status)
	}
	unreach := Sample{Reachable: &no, Runs: []RunSample{{Pass: &no}}}
	unreach.Summarize()
	if unreach.Status != SampleInfra {
		t.Fatal(unreach.Status)
	}
	empty := Sample{}
	empty.Summarize()
	if empty.Status != SampleError || empty.Errors != 1 {
		t.Fatalf("%+v", empty)
	}
}

func TestInfraError(t *testing.T) {
	for _, m := range []string{"dial tcp 10.0.0.1:443: connect: connection refused", "endpoint returned status 503", "Get x: i/o timeout"} {
		if !InfraError(m) {
			t.Fatalf("%q should be infra", m)
		}
	}
	for _, m := range []string{"", "wrong answer: expected 42"} {
		if InfraError(m) {
			t.Fatalf("%q should not be infra", m)
		}
	}
}
