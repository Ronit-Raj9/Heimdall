package livescore

import (
	"math"
)

// The sequential test. Live traffic is looked at after every pull, forever,
// so a fixed-sample test would raise false alarms at every look. Each
// signal is tested with a betting e-process (testing by betting, Shafer and
// Vovk; Waudby-Smith and Ramdas 2023), which stays valid however often it
// is looked at and whenever it is stopped:
//
//   - The two versions' sessions are paired in order of scoring (the i-th
//     scored baseline session with the i-th scored candidate session, per
//     signal, among sessions where the signal is known). The pairing
//     depends only on arrival order, never on a value.
//   - A rate signal gives d = candidate - baseline in [-1, 1]; an ordinal
//     signal gives d = sign(candidate - baseline) in {-1, 0, 1}.
//   - Under the null (the two versions' sessions are independent draws
//     with the same mean for a rate signal, the same distribution for an
//     ordinal one), each d has mean 0 given the past. For a fixed bet
//     lambda in (-1, 1), W = prod(1 + lambda d) is then a nonnegative
//     martingale with W_0 = 1, and so is the average over a fixed grid of
//     bets. Ville's inequality gives P(W ever reaches 1/a) <= a.
//   - Two one-sided processes per signal (candidate higher, candidate
//     lower) each get a = alpha / (2 m) over m compared signals
//     (Bonferroni), so the chance that any signal ever decides anything
//     when nothing differs is at most alpha, over every look. Bonferroni
//     needs no assumption about how the signals depend on each other.
//   - A signal may decide only after MinPairs pairs; that only removes
//     chances to decide, so it keeps the bound.
//
// The result for a signal is worse when the process for "candidate is
// worse" reached its threshold, better when the other one did, otherwise
// not yet known. A crossing is kept: Ville's bound covers the whole path,
// so a decision made at any look stays valid.

// betGrid is the fixed grid of bets mixed by each one-sided process. Small
// bets catch small effects slowly; large ones catch large effects fast.
// The largest, 0.5, keeps 1 + lambda d at or above 0.5.
var betGrid = []float64{0.02, 0.05, 0.1, 0.2, 0.35, 0.5}

// Direction indexes of an e-process: up tests "candidate higher than
// baseline", down "candidate lower".
const (
	dirUp   = 0
	dirDown = 1
)

// EProcess is one signal's pair of one-sided betting processes, kept as
// log wealth per bet.
type EProcess struct {
	LogW  [2][]float64 `json:"log_w"`
	Pairs int          `json:"pairs"`
	// MaxLog is the largest log e-value reached at or after MinPairs pairs,
	// per direction; Crossed the pair count at the first crossing (0: none).
	MaxLog  [2]float64 `json:"max_log"`
	Crossed [2]int     `json:"crossed"`
}

// NewEProcess returns a process with wealth 1 on every bet.
func NewEProcess() *EProcess {
	e := &EProcess{}
	for d := range 2 {
		e.LogW[d] = make([]float64, len(betGrid))
	}
	return e
}

// logE is the log of the mixture e-value in direction dir.
func (e *EProcess) logE(dir int) float64 {
	// log(mean(exp(w))) computed stably.
	mx := math.Inf(-1)
	for _, w := range e.LogW[dir] {
		mx = math.Max(mx, w)
	}
	if math.IsInf(mx, -1) {
		return 0
	}
	s := 0.0
	for _, w := range e.LogW[dir] {
		s += math.Exp(w - mx)
	}
	return mx + math.Log(s/float64(len(e.LogW[dir])))
}

// E is the current e-value in direction dir.
func (e *EProcess) E(dir int) float64 { return math.Exp(e.logE(dir)) }

// Add takes one paired difference d in [-1, 1]. logThreshold is log(1/a);
// minPairs the pairs before a crossing counts.
func (e *EProcess) Add(d float64, logThreshold float64, minPairs int) {
	d = math.Max(-1, math.Min(1, d))
	if len(e.LogW[0]) != len(betGrid) {
		*e = *NewEProcess()
	}
	e.Pairs++
	for i, l := range betGrid {
		e.LogW[dirUp][i] += math.Log1p(l * d)
		e.LogW[dirDown][i] += math.Log1p(-l * d)
	}
	if e.Pairs < minPairs {
		return
	}
	for dir := range 2 {
		le := e.logE(dir)
		if le > e.MaxLog[dir] {
			e.MaxLog[dir] = le
		}
		if e.Crossed[dir] == 0 && le >= logThreshold {
			e.Crossed[dir] = e.Pairs
		}
	}
}

// pairDiff is the paired difference of two values of a signal of kind.
func pairDiff(kind string, baseline, candidate float64) float64 {
	if kind == KindOrdinal {
		switch {
		case candidate > baseline:
			return 1
		case candidate < baseline:
			return -1
		}
		return 0
	}
	return candidate - baseline
}

// perSignalLevel is the level each one-sided process is tested at.
func perSignalLevel(alpha float64, signals int) float64 {
	if signals < 1 {
		signals = 1
	}
	return alpha / float64(2*signals)
}
