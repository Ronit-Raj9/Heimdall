package livescore

import (
	"github.com/maximhq/bifrost/bench/stats"
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

// The process itself lives in stats (stats/evalue.go), shared with the
// commit gate of an improvement run (bench/improve).

// Direction indexes of an e-process: up tests "candidate higher than
// baseline", down "candidate lower".
const (
	dirUp   = stats.EUp
	dirDown = stats.EDown
)

// EProcess is one signal's pair of one-sided betting processes, kept as
// log wealth per bet; its JSON in state.json is unchanged by the move.
type EProcess = stats.EProcess

// NewEProcess returns a process with wealth 1 on every bet.
func NewEProcess() *EProcess { return stats.NewEProcess() }

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
