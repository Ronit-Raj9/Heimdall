package livescore

import (
	"math"
	"testing"
	"time"
)

// TestSimAAFalseAlarmRate is the A/A proof: two versions drawing from one
// distribution, looked at every 10 sessions up to 600 sessions each, across
// every compared signal. The family-wise false-alarm rate must stay under
// alpha (with three standard errors of Monte Carlo slack).
func TestSimAAFalseAlarmRate(t *testing.T) {
	streams := 400
	if testing.Short() {
		streams = 100
	}
	for _, imb := range []float64{0.5, 0.2} {
		r := Simulate(SimConfig{Streams: streams, Sessions: 600, Seed: 7, Imbalance: imb})
		t.Logf("A/A imbalance %.1f: %d of %d streams decided anything (rate %.4f, se %.4f), by signal %v, %d signals at alpha %.2f",
			imb, r.AnyDecision, r.Streams, r.Rate, r.StdErr, r.BySignal, r.Signals, r.Alpha)
		if r.Rate > r.Alpha+3*math.Max(r.StdErr, math.Sqrt(r.Alpha*(1-r.Alpha)/float64(r.Streams))) {
			t.Errorf("imbalance %.1f: false-alarm rate %.4f is over alpha %.2f", imb, r.Rate, r.Alpha)
		}
	}
}

// TestSimLooksDoNotMatter shows the decision does not depend on how often
// the comparison is looked at: looking after every session gives the same
// decisions as looking every 100, because the process is updated per pair.
func TestSimLooksDoNotMatter(t *testing.T) {
	a := Simulate(SimConfig{Streams: 60, Sessions: 300, Seed: 3, LookEvery: 1})
	b := Simulate(SimConfig{Streams: 60, Sessions: 300, Seed: 3, LookEvery: 100})
	if a.AnyDecision != b.AnyDecision || a.WorseDecisions != b.WorseDecisions {
		t.Errorf("look every 1: %d decisions, every 100: %d", a.AnyDecision, b.AnyDecision)
	}
}

// TestSimPower shows a real regression is found: the candidate abandons
// 15 more sessions in 100.
func TestSimPower(t *testing.T) {
	r := Simulate(SimConfig{Streams: 100, Sessions: 600, Seed: 11, Effect: 0.15})
	t.Logf("effect 0.15: %d of %d streams decided worse, median %d pairs at the first decision, by signal %v", r.WorseDecisions, r.Streams, r.MedianPairs, r.BySignal)
	if float64(r.WorseDecisions)/float64(r.Streams) < 0.8 {
		t.Errorf("power %.2f under 0.8 for a 15 point abandonment rise", float64(r.WorseDecisions)/float64(r.Streams))
	}
	if r.BySignal["turn_latency_ms"] > r.Streams/10 {
		t.Errorf("latency decided in %d streams although it did not change", r.BySignal["turn_latency_ms"])
	}
}

func TestEProcessMartingaleAndThreshold(t *testing.T) {
	e := NewEProcess()
	lt := math.Log(1 / 0.001)
	for range 200 {
		e.Add(0, lt, 30)
	}
	if e.E(dirUp) != 1 || e.E(dirDown) != 1 || e.Crossed[0] != 0 {
		t.Errorf("zero differences moved the process: %v %v", e.E(dirUp), e.E(dirDown))
	}
	e = NewEProcess()
	for range 100 {
		e.Add(1, lt, 30)
	}
	if e.Crossed[dirUp] == 0 || e.Crossed[dirUp] < 30 || e.Crossed[dirDown] != 0 {
		t.Errorf("all-up differences: crossed %v", e.Crossed)
	}
	// Ordinal pairs only bet on the sign.
	if pairDiff(KindOrdinal, 10, 1e9) != 1 || pairDiff(KindOrdinal, 5, 5) != 0 || pairDiff(KindBounded, 0.2, 0.5) != 0.3 {
		t.Error("pairDiff")
	}
}

func TestCompareJudgeUncalibratedNeverTested(t *testing.T) {
	cfg := &Config{Versions: []Version{{Name: "a"}, {Name: "b"}},
		Compare: &CompareConfig{Baseline: "a", Candidate: "b", Signals: []string{"judge_score"}, MinPairs: 1}}
	st := newCompare(cfg, 0, time.Now(), 0)
	var recs []SessionRecord
	for i := range 400 {
		v, score := "a", 1.0
		if i%2 == 1 {
			v, score = "b", 0.0
		}
		recs = append(recs, SessionRecord{Seq: int64(i + 1), Version: v, Signals: Signals{JudgeScore: &score}})
	}
	st.consume(recs, time.Now())
	if st.Tests["judge_score"].E.Pairs != 0 || st.Result != ResultUnknown {
		t.Errorf("uncalibrated judge scores were tested: %d pairs, %s", st.Tests["judge_score"].E.Pairs, st.Result)
	}
	for i := range recs {
		recs[i].Seq += 1000
		recs[i].Signals.JudgeCalibrated = true
	}
	st.consume(recs, time.Now())
	if st.Result != ResultWorse {
		t.Errorf("calibrated judge, candidate always 0: %s", st.Result)
	}
}
