package livescore

import (
	"math"
	"math/rand/v2"
	"slices"
	"time"
)

// SimConfig is a simulation of the comparison on synthetic session
// streams. With Effect zero both versions draw from one distribution (an
// A/A test), and every decision is a false alarm.
type SimConfig struct {
	Streams  int     // independent comparisons
	Sessions int     // sessions per version per stream
	Alpha    float64 // default DefaultAlpha
	MinPairs int     // default DefaultMinPairs
	Seed     uint64
	// Effect raises the candidate's error-driven failure rate by this much
	// (0 for A/A).
	Effect float64
	// Imbalance is the candidate's share of traffic (default 0.5); the
	// streams arrive interleaved at random in that proportion.
	Imbalance float64
	// LookEvery is how many sessions arrive between looks (default 10). The
	// result does not depend on it; it is there to show that.
	LookEvery int
}

// SimResult counts what the simulated comparisons decided.
type SimResult struct {
	Streams        int            `json:"streams"`
	Sessions       int            `json:"sessions_per_version"`
	Alpha          float64        `json:"alpha"`
	Signals        int            `json:"signals"`
	AnyDecision    int            `json:"any_decision"` // streams where some signal decided
	Rate           float64        `json:"rate"`         // AnyDecision / Streams
	StdErr         float64        `json:"std_err"`      // binomial standard error of Rate
	WorseDecisions int            `json:"worse"`        // streams whose result was worse
	BySignal       map[string]int `json:"by_signal"`    // streams in which each signal decided
	MedianPairs    int            `json:"median_pairs"` // pairs at the first decision, over streams that decided
	Effect         float64        `json:"effect"`       // the candidate's added failure rate
	LookEvery      int            `json:"look_every"`   // sessions between looks
	Description    string         `json:"description"`  // what the synthetic sessions look like
}

// SimDescription says what the synthetic stream is.
const SimDescription = "Each synthetic session draws a latent trouble level; request errors, tool errors, retries, fallbacks, " +
	"abandonment, loops and refusals are correlated through it, latency and tokens are lognormal and rise with it, cost follows tokens. " +
	"Both versions draw from the same distribution unless an effect is set."

// Simulate runs the comparison on synthetic streams. It uses the same code
// path as a live comparison (CompareState.consume).
func Simulate(c SimConfig) SimResult {
	if c.Alpha == 0 {
		c.Alpha = DefaultAlpha
	}
	if c.MinPairs == 0 {
		c.MinPairs = DefaultMinPairs
	}
	if c.Imbalance <= 0 || c.Imbalance >= 1 {
		c.Imbalance = 0.5
	}
	if c.LookEvery <= 0 {
		c.LookEvery = 10
	}
	cfg := &Config{Versions: []Version{{Name: "a"}, {Name: "b"}},
		Compare: &CompareConfig{Baseline: "a", Candidate: "b", Alpha: c.Alpha, MinPairs: c.MinPairs}}
	res := SimResult{Streams: c.Streams, Sessions: c.Sessions, Alpha: c.Alpha, BySignal: map[string]int{}, Effect: c.Effect,
		LookEvery: c.LookEvery, Description: SimDescription}
	res.Signals = len(cfg.compareSettings().Signals)
	rng := rand.New(rand.NewPCG(c.Seed, c.Seed^0x9e3779b97f4a7c15))
	var firstPairs []int
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for s := 0; s < c.Streams; s++ {
		st := newCompare(cfg, 0, base, 0)
		var seq int64
		na, nb := 0, 0
		var batch []SessionRecord
		first := 0
		for na < c.Sessions || nb < c.Sessions {
			cand := rng.Float64() < c.Imbalance
			if cand && nb >= c.Sessions {
				cand = false
			}
			if !cand && na >= c.Sessions {
				cand = true
			}
			v, eff := "a", 0.0
			if cand {
				v, eff = "b", c.Effect
				nb++
			} else {
				na++
			}
			seq++
			batch = append(batch, SessionRecord{Seq: seq, Version: v, Signals: synthSignals(rng, eff)})
			if len(batch) >= c.LookEvery {
				if d := st.consume(batch, base); len(d) > 0 && first == 0 {
					first = maxPairs(st)
				}
				batch = batch[:0]
			}
		}
		if d := st.consume(batch, base); len(d) > 0 && first == 0 {
			first = maxPairs(st)
		}
		any := false
		for name, t := range st.Tests {
			if t.Worse != nil || t.Better != nil {
				res.BySignal[name]++
				any = true
			}
		}
		if any {
			res.AnyDecision++
			firstPairs = append(firstPairs, first)
		}
		if st.Result == ResultWorse {
			res.WorseDecisions++
		}
	}
	if c.Streams > 0 {
		res.Rate = float64(res.AnyDecision) / float64(c.Streams)
		res.StdErr = math.Sqrt(res.Rate * (1 - res.Rate) / float64(c.Streams))
	}
	if len(firstPairs) > 0 {
		slices.Sort(firstPairs)
		res.MedianPairs = firstPairs[len(firstPairs)/2]
	}
	return res
}

func maxPairs(st *CompareState) int {
	m := 0
	for _, t := range st.Tests {
		m = max(m, t.E.Pairs)
	}
	return m
}

// synthSignals draws one synthetic session. effect raises the chance that
// the session's last request fails (and with it errors and abandonment).
func synthSignals(rng *rand.Rand, effect float64) Signals {
	trouble := rng.ExpFloat64() * 0.15 // latent, mean 0.15
	p := func(x float64) bool { return rng.Float64() < math.Min(1, x) }
	reqs := 1 + rng.IntN(6)
	var s Signals
	for range reqs {
		if p(0.03 + trouble*0.2) {
			s.RequestErrors++
		}
	}
	if p(effect) {
		s.RequestErrors++
		reqs++
		s.Abandoned = true
	} else if s.RequestErrors > 0 && p(0.4) {
		s.Abandoned = true
	}
	rate := float64(s.RequestErrors) / float64(reqs)
	s.RequestErrorRate = &rate
	if p(0.1 + trouble) {
		s.Retries = 1 + rng.IntN(3)
	}
	if p(0.02 + trouble*0.1) {
		s.Fallbacks = 1
	}
	calls := rng.IntN(8)
	errs := 0
	for range calls {
		if p(0.05 + trouble*0.3) {
			errs++
		}
	}
	s.ToolCalls, s.ToolErrors = &calls, &errs
	if calls > 0 {
		r := float64(errs) / float64(calls)
		s.ToolErrorRate = &r
	}
	loop := calls >= 3 && p(0.02+trouble*0.2)
	s.Loop = &loop
	rep := 0
	if loop {
		rep = 2
	}
	s.RepeatedCalls = &rep
	ref := p(0.02)
	s.Refusal = &ref
	inj := 0
	if p(0.01) {
		inj = 1
	}
	s.InjectionMarkers = &inj
	if p(0.005) {
		s.BudgetHits = 1
	}
	lat := math.Exp(rng.NormFloat64()*0.6+6.5) * (1 + trouble)
	s.TurnLatencyMS = &lat
	sess := lat*float64(reqs) + math.Exp(rng.NormFloat64()*0.8+7)
	s.SessionMS = &sess
	tok := int64(math.Exp(rng.NormFloat64()*0.7+8) * float64(reqs))
	s.Tokens = &tok
	cost := float64(tok) * 2e-6
	s.CostUSD = &cost
	return s
}
