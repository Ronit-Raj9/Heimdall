package livemon

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

// check builds one check of n runs with pass passing, each latency ms.
func check(at time.Time, n, pass int, ms int64) Sample {
	s := Sample{At: at, EvaluationID: fmt.Sprintf("ev_%d", at.Unix())}
	for i := 0; i < n; i++ {
		p := i < pass
		l := ms
		c := 0.01
		s.Runs = append(s.Runs, RunSample{RunID: fmt.Sprintf("r_%d_%d", at.Unix(), i), Task: fmt.Sprintf("t%d", i),
			Outcome: map[bool]string{true: "pass", false: "fail"}[p], Pass: &p, LatencyMS: &l, CostUSD: &c})
	}
	s.Summarize()
	return s
}

func down(at time.Time, n int) Sample {
	s := Sample{At: at, EvaluationID: fmt.Sprintf("ev_%d", at.Unix())}
	for i := 0; i < n; i++ {
		s.Runs = append(s.Runs, RunSample{RunID: fmt.Sprintf("r_%d_%d", at.Unix(), i), Outcome: "infra_error", Infra: true,
			Error: "dial tcp 1.2.3.4:443: connect: connection refused"})
	}
	no := false
	s.Reachable = &no
	s.Summarize()
	return s
}

type sim struct {
	m         Monitor
	samples   []Sample
	incidents []Incident
	notices   []Notice
	at        time.Time
}

func newSim() *sim {
	return &sim{m: Monitor{ID: "mon_0123456789ab", Tasks: 5, IntervalMin: 60}, at: mustTime("2026-11-01T00:00:00Z")}
}

func (s *sim) add(x Sample) []Notice {
	s.samples = append(s.samples, x)
	var n []Notice
	s.samples, s.incidents, n = Step(s.m, s.samples, s.incidents, x.At)
	s.notices = append(s.notices, n...)
	return n
}

func (s *sim) next() time.Time {
	s.at = s.at.Add(time.Hour)
	return s.at
}

func events(ns []Notice) string {
	var out []string
	for _, n := range ns {
		out = append(out, n.Incident.Kind+":"+n.Event)
	}
	return strings.Join(out, ",")
}

func TestOneFailedCheckRaisesNothing(t *testing.T) {
	s := newSim()
	for i := 0; i < 24; i++ {
		s.add(check(s.next(), 5, 5, 1000))
	}
	if n := s.add(check(s.next(), 5, 0, 1000)); len(n) != 0 {
		t.Fatalf("one bad check alerted: %s", events(n))
	}
	if n := s.add(down(s.next(), 5)); len(n) != 0 {
		t.Fatalf("one outage check alerted: %s", events(n))
	}
}

func TestQualityDropOpensUpdatesResolvesOnce(t *testing.T) {
	s := newSim()
	for i := 0; i < 24; i++ {
		s.add(check(s.next(), 5, 5, 1000))
	}
	var got []Notice
	for i := 0; i < 3; i++ {
		got = append(got, s.add(check(s.next(), 5, 2, 1000))...)
	}
	if events(got) != "quality:opened" {
		t.Fatalf("degrade: %q", events(got))
	}
	inc := got[0].Incident
	if inc.Baseline == nil || *inc.Baseline.PassRate != 1 || inc.PValue == nil || *inc.PValue >= 0.05 {
		t.Fatalf("incident numbers: %+v", inc)
	}
	if len(inc.FailingRuns) == 0 || !strings.Contains(inc.Title, "100%") {
		t.Fatalf("failing runs/title: %+v", inc)
	}
	// Still bad: no new alert. Much worse: one update.
	if n := s.add(check(s.next(), 5, 2, 1000)); len(n) != 0 {
		t.Fatalf("still bad alerted: %s", events(n))
	}
	n := s.add(check(s.next(), 5, 0, 1000))
	n = append(n, s.add(check(s.next(), 5, 0, 1000))...)
	if events(n) != "quality:updated" {
		t.Fatalf("worse: %q", events(n))
	}
	// Recovery resolves it.
	var rec []Notice
	for i := 0; i < 3; i++ {
		rec = append(rec, s.add(check(s.next(), 5, 5, 1000))...)
	}
	if events(rec) != "quality:resolved" {
		t.Fatalf("recover: %q", events(rec))
	}
	open := 0
	for _, inc := range s.incidents {
		if inc.State == StateOpen {
			open++
		}
	}
	if open != 0 || len(s.incidents) != 1 {
		t.Fatalf("incidents: %d open of %d", open, len(s.incidents))
	}
	// Checks taken during the incident never enter a baseline.
	for _, x := range s.samples[24:30] {
		if x.Incident == "" {
			t.Fatalf("check at %s not marked", x.At)
		}
	}
}

func TestNoBaselineFloorRule(t *testing.T) {
	s := newSim()
	s.m.Rule.MinPassRate = 0.8
	var got []Notice
	for i := 0; i < 3; i++ {
		got = append(got, s.add(check(s.next(), 5, 1, 1000))...)
	}
	if events(got) != "quality:opened" || !got[0].Incident.Floor {
		t.Fatalf("floor: %q", events(got))
	}
}

func TestLearningWithoutBaseline(t *testing.T) {
	s := newSim()
	for i := 0; i < 3; i++ {
		if n := s.add(check(s.next(), 5, 0, 1000)); len(n) != 0 {
			t.Fatalf("no baseline yet but alerted: %s", events(n))
		}
	}
}

func TestOutageIsInfraNotQuality(t *testing.T) {
	s := newSim()
	for i := 0; i < 24; i++ {
		s.add(check(s.next(), 5, 5, 1000))
	}
	var got []Notice
	for i := 0; i < 2; i++ {
		got = append(got, s.add(down(s.next(), 5))...)
	}
	if events(got) != "infra:opened" {
		t.Fatalf("outage: %q", events(got))
	}
	if got[0].Incident.Cause != "unreachable" || !strings.Contains(got[0].Incident.Title, "not answering") {
		t.Fatalf("cause: %+v", got[0].Incident)
	}
	// Six more down checks: one reminder update.
	var more []Notice
	for i := 0; i < 6; i++ {
		more = append(more, s.add(down(s.next(), 5))...)
	}
	if events(more) != "infra:updated" {
		t.Fatalf("reminder: %q", events(more))
	}
	if n := s.add(check(s.next(), 5, 5, 1000)); events(n) != "infra:resolved" {
		t.Fatalf("recover: %q", events(n))
	}
	for _, inc := range s.incidents {
		if inc.Kind == KindQuality {
			t.Fatal("an outage opened a quality incident")
		}
	}
}

func TestSkippedChecksDoNotBreakAnOutage(t *testing.T) {
	s := newSim()
	s.add(down(s.next(), 3))
	s.add(Sample{At: s.next(), Status: SampleSkipped, Reason: "daily cap"})
	if n := s.add(down(s.next(), 3)); events(n) != "infra:opened" {
		t.Fatalf("got %q", events(n))
	}
}

func TestCannotRunIsInfra(t *testing.T) {
	s := newSim()
	var got []Notice
	for i := 0; i < 2; i++ {
		x := Sample{At: s.next(), Status: SampleError, Reason: "agent cannot run benchmark b"}
		x.Summarize()
		got = append(got, s.add(x)...)
	}
	if events(got) != "infra:opened" || got[0].Incident.Cause != "cannot_run" {
		t.Fatalf("got %q %+v", events(got), got)
	}
}

func TestLatencyIncident(t *testing.T) {
	s := newSim()
	for i := 0; i < 24; i++ {
		s.add(check(s.next(), 5, 5, 1000+int64(i%5)*10))
	}
	var got []Notice
	for i := 0; i < 3; i++ {
		got = append(got, s.add(check(s.next(), 5, 5, 5000+int64(i)*10))...)
	}
	if events(got) != "latency:opened" {
		t.Fatalf("latency: %q", events(got))
	}
	var rec []Notice
	for i := 0; i < 3; i++ {
		rec = append(rec, s.add(check(s.next(), 5, 5, 1000))...)
	}
	if events(rec) != "latency:resolved" {
		t.Fatalf("recover: %q", events(rec))
	}
	s2 := newSim()
	s2.m.Rule.LatencyFactor = -1
	for i := 0; i < 24; i++ {
		s2.add(check(s2.next(), 5, 5, 1000))
	}
	for i := 0; i < 3; i++ {
		if n := s2.add(check(s2.next(), 5, 5, 9000)); len(n) != 0 {
			t.Fatalf("latency alerts off but got %s", events(n))
		}
	}
}

func TestStepIsPureOnInputs(t *testing.T) {
	s := newSim()
	for i := 0; i < 24; i++ {
		s.add(check(s.next(), 5, 5, 1000))
	}
	samples := append([]Sample(nil), s.samples...)
	samples = append(samples, check(s.next(), 5, 0, 1000))
	before := samples[len(samples)-1].Incident
	Step(s.m, samples, nil, s.at)
	if samples[len(samples)-1].Incident != before {
		t.Fatal("Step changed its input")
	}
}
