package improve

import (
	"math"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/stats"
)

func init() { DefaultScreener = halving{} }

// halving is the practice screen (A2.3): Sequential Halving over the round's
// ideas on the dev split, with chunks of dev tasks fixed by the seed before
// any idea runs.
type halving struct{}

// screenAlpha is the level below which an idea is dropped as clearly worse
// than the current version on the practice tasks.
const screenAlpha = 0.05

// tieEps is the difference below which two mean practice differences tie.
const tieEps = 1e-9

// Levels splits dev into ceil(log2(k)) chunks (one chunk holding all of dev
// for k = 1 or 2), shuffled by seed; the first len(dev) mod L chunks are one
// task larger.
func (halving) Levels(k int, dev []string, seed int64) [][]string {
	d := slices.Clone(dev)
	slices.Sort(d)
	if k <= 1 {
		return [][]string{d}
	}
	l := int(math.Ceil(math.Log2(float64(k))))
	l = max(1, min(l, len(d)))
	rng := rand.New(rand.NewPCG(uint64(seed), 0x5eed))
	rng.Shuffle(len(d), func(i, j int) { d[i], d[j] = d[j], d[i] })
	out := make([][]string, 0, l)
	size, extra := len(d)/l, len(d)%l
	at := 0
	for j := range l {
		n := size
		if j < extra {
			n++
		}
		out = append(out, slices.Clone(d[at:at+n]))
		at += n
	}
	return out
}

// Cut drops ideas that are clearly worse, ranks the rest by mean
// difference (ties: fewer bundle words, lower screen spend, lower slot,
// which is the id order) and keeps the first in.Keep.
func (halving) Cut(in CutInput) CutResult {
	out := CutResult{Keep: []string{}, Dropped: map[string]string{}}
	ids := make([]string, 0, len(in.Diffs))
	for id := range in.Diffs {
		ids = append(ids, id)
	}
	slices.SortFunc(ids, compareIDs)
	alive := []string{}
	for _, id := range ids {
		d := in.Diffs[id]
		neg := make([]float64, len(d))
		for i, x := range d {
			neg[i] = -x
		}
		if len(d) > 0 && stats.SignFlipUpper(neg).P < screenAlpha {
			out.Dropped[id] = "worse than the current version on the practice tasks"
			continue
		}
		alive = append(alive, id)
	}
	slices.SortStableFunc(alive, func(a, b string) int {
		ma, mb := mean(in.Diffs[a]), mean(in.Diffs[b])
		if math.Abs(ma-mb) > tieEps {
			if ma > mb {
				return -1
			}
			return 1
		}
		if wa, wb := in.Words[a], in.Words[b]; wa != wb {
			return wa - wb
		}
		if ca, cb := in.Cost[a], in.Cost[b]; ca != cb {
			if ca < cb {
				return -1
			}
			return 1
		}
		return compareIDs(a, b)
	})
	keep := max(1, in.Keep)
	for i, id := range alive {
		if i < keep {
			out.Keep = append(out.Keep, id)
		} else {
			out.Dropped[id] = "not among the best ideas on the practice tasks"
		}
	}
	return out
}

// compareIDs orders candidate ids c1, c2, ..., c10 by slot number.
func compareIDs(a, b string) int {
	if len(a) != len(b) {
		return len(a) - len(b)
	}
	return strings.Compare(a, b)
}

// wordCount is a bundle's word count, for the screen's tie rule.
func wordCount(b Bundle) int {
	n := 0
	for _, v := range b {
		n += len(strings.Fields(v))
	}
	return n
}
