package stats

import (
	"math"
)

// The anytime-valid paired test. A comparison that is looked at after every
// pair (live traffic, or an improvement loop's commit gate) cannot use a
// fixed-sample test without raising false alarms at every look. A betting
// e-process (testing by betting, Shafer and Vovk; Waudby-Smith and Ramdas
// 2023) stays valid however often it is looked at and whenever it is
// stopped:
//
//   - Each pair gives a difference d = candidate - baseline in [-1, 1]
//     (an ordinal signal uses the sign only). The pairing must depend only
//     on arrival or a fixed order, never on a value.
//   - Under the null (each d has mean 0 given the past), for a fixed bet
//     lambda in (-1, 1), W = prod(1 + lambda d) is a nonnegative martingale
//     with W_0 = 1, and so is the average over a fixed grid of bets.
//     Ville's inequality gives P(W ever reaches 1/a) <= a.
//   - The process keeps two one-sided processes, candidate higher (EUp) and
//     candidate lower (EDown); the caller picks the level of each.
//   - A crossing may count only after minPairs pairs; that only removes
//     chances to decide, so it keeps the bound. A crossing is kept: Ville's
//     bound covers the whole path, so a decision made at any look stays
//     valid.
//
// livescore (live traffic comparisons) and improve (the commit gate of an
// improvement run) share this one implementation.

// BetGrid is the fixed grid of bets mixed by each one-sided process. Small
// bets catch small effects slowly; large ones catch large effects fast. The
// largest, 0.5, keeps 1 + lambda d at or above 0.5.
var BetGrid = []float64{0.02, 0.05, 0.1, 0.2, 0.35, 0.5}

// Direction indexes of an e-process: EUp tests "candidate higher than
// baseline", EDown "candidate lower".
const (
	EUp   = 0
	EDown = 1
)

// EProcess is a pair of one-sided betting processes, kept as log wealth per
// bet.
type EProcess struct {
	LogW  [2][]float64 `json:"log_w"`
	Pairs int          `json:"pairs"`
	// MaxLog is the largest log e-value reached at or after minPairs pairs,
	// per direction; Crossed the pair count at the first crossing (0: none).
	MaxLog  [2]float64 `json:"max_log"`
	Crossed [2]int     `json:"crossed"`
}

// NewEProcess returns a process with wealth 1 on every bet.
func NewEProcess() *EProcess {
	e := &EProcess{}
	for d := range 2 {
		e.LogW[d] = make([]float64, len(BetGrid))
	}
	return e
}

// LogE is the log of the mixture e-value in direction dir.
func (e *EProcess) LogE(dir int) float64 {
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
func (e *EProcess) E(dir int) float64 { return math.Exp(e.LogE(dir)) }

// Add takes one paired difference d in [-1, 1]. logThreshold is log(1/a);
// minPairs the pairs before a crossing counts.
func (e *EProcess) Add(d float64, logThreshold float64, minPairs int) {
	d = math.Max(-1, math.Min(1, d))
	if len(e.LogW[0]) != len(BetGrid) {
		*e = *NewEProcess()
	}
	e.Pairs++
	for i, l := range BetGrid {
		e.LogW[EUp][i] += math.Log1p(l * d)
		e.LogW[EDown][i] += math.Log1p(-l * d)
	}
	if e.Pairs < minPairs {
		return
	}
	for dir := range 2 {
		le := e.LogE(dir)
		if le > e.MaxLog[dir] {
			e.MaxLog[dir] = le
		}
		if e.Crossed[dir] == 0 && le >= logThreshold {
			e.Crossed[dir] = e.Pairs
		}
	}
}

// EValueTest is the outcome of PairedEValue.
type EValueTest struct {
	Alpha     float64   `json:"alpha"`      // level of the "candidate higher" test
	Threshold float64   `json:"threshold"`  // 1/alpha
	MinPairs  int       `json:"min_pairs"`  // pairs before a crossing counts
	MaxPairs  int       `json:"max_pairs"`  // pairs read at most (0: all)
	Pairs     int       `json:"pairs"`      // pairs read
	EUp       float64   `json:"e_up"`       // final e-value for "candidate higher"
	EDown     float64   `json:"e_down"`     // final e-value for "candidate lower"
	MaxEUp    float64   `json:"max_e_up"`   // largest "higher" e-value at or after MinPairs
	Path      []float64 `json:"path"`       // "higher" e-value after each pair
	Better    bool      `json:"better"`     // "higher" crossed 1/alpha
	Worse     bool      `json:"worse"`      // "lower" crossed 1/alpha
	CrossedAt int       `json:"crossed_at"` // pair count of the "higher" crossing; 0 when none
}

// PairedEValue feeds paired differences, in the given order, into one
// e-process and stops at the first "candidate higher" crossing or after
// maxPairs (0: no limit). Both directions are tested at alpha each, so the
// chance of a false "better" is at most alpha however the caller stops.
// Non-finite differences are skipped.
func PairedEValue(d []float64, alpha float64, minPairs, maxPairs int) EValueTest {
	if !(alpha > 0 && alpha < 1) {
		alpha = 0.05
	}
	if minPairs < 1 {
		minPairs = 1
	}
	lt := -math.Log(alpha)
	e := NewEProcess()
	res := EValueTest{Alpha: alpha, Threshold: 1 / alpha, MinPairs: minPairs, MaxPairs: maxPairs, MaxEUp: 1, Path: []float64{}}
	for _, x := range d {
		if maxPairs > 0 && e.Pairs >= maxPairs {
			break
		}
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		e.Add(x, lt, minPairs)
		res.Path = append(res.Path, e.E(EUp))
		if e.Crossed[EUp] != 0 {
			break
		}
	}
	res.Pairs = e.Pairs
	res.EUp, res.EDown = e.E(EUp), e.E(EDown)
	res.MaxEUp = math.Max(1, math.Exp(e.MaxLog[EUp]))
	res.Better = e.Crossed[EUp] != 0
	res.Worse = e.Crossed[EDown] != 0
	res.CrossedAt = e.Crossed[EUp]
	return res
}
