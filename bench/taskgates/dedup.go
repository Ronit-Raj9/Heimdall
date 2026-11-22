package taskgates

import (
	"crypto/sha256"
	"encoding/hex"
	"hash/fnv"
	"math"
	"sort"
)

// Dedup finds exact and near duplicate tasks: the same normalised text, or
// word 3-gram sets whose Jaccard similarity is at least the threshold
// (default 0.8). Candidates come from MinHash with locality-sensitive
// banding, then each candidate pair's Jaccard is computed exactly on the
// shingle sets, so a reported score is never an estimate.

// DefaultNearThreshold is the Jaccard similarity at or above which two tasks
// are near duplicates.
const DefaultNearThreshold = 0.8

const (
	shingleWords = 3
	minhashBands = 32
	minhashRows  = 4
	minhashSize  = minhashBands * minhashRows
)

// Duplicate kinds (DuplicatePair.Kind).
const (
	DupExact = "exact"
	DupNear  = "near"
)

// DuplicatePair is two tasks with the same or nearly the same text.
type DuplicatePair struct {
	A       string  `json:"a"` // suite/task
	B       string  `json:"b"`
	Kind    string  `json:"kind"`    // exact | near
	Jaccard float64 `json:"jaccard"` // word 3-gram Jaccard similarity; 1 for exact
}

// Signature is a task's MinHash signature and shingle set.
type Signature struct {
	Key      string
	Exact    string // sha256 of the normalised text
	Shingles map[uint64]struct{}
	MinHash  [minhashSize]uint64
}

// Sign computes the signature of it.
func Sign(it Item) Signature {
	words := Words(it.Text())
	norm := joinWords(words)
	sum := sha256.Sum256([]byte(norm))
	sig := Signature{Key: it.Key(), Exact: hex.EncodeToString(sum[:]), Shingles: shingles(words)}
	for i := range sig.MinHash {
		sig.MinHash[i] = math.MaxUint64
	}
	for sh := range sig.Shingles {
		for i := range sig.MinHash {
			if h := mix(sh, uint64(i)); h < sig.MinHash[i] {
				sig.MinHash[i] = h
			}
		}
	}
	return sig
}

func joinWords(w []string) string {
	n := 0
	for _, s := range w {
		n += len(s) + 1
	}
	b := make([]byte, 0, n)
	for i, s := range w {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, s...)
	}
	return string(b)
}

// shingles hashes every run of shingleWords words; a text shorter than that
// is one shingle of all its words.
func shingles(words []string) map[uint64]struct{} {
	out := map[uint64]struct{}{}
	if len(words) == 0 {
		return out
	}
	if len(words) < shingleWords {
		out[hashWords(words)] = struct{}{}
		return out
	}
	for i := 0; i+shingleWords <= len(words); i++ {
		out[hashWords(words[i:i+shingleWords])] = struct{}{}
	}
	return out
}

func hashWords(w []string) uint64 {
	h := fnv.New64a()
	for _, s := range w {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// mix is the i-th hash of x (splitmix64 over x xor a per-function seed).
func mix(x, i uint64) uint64 {
	z := x ^ (i*0x9e3779b97f4a7c15 + 0x632be59bd9b4e019)
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// Jaccard is the exact Jaccard similarity of two shingle sets.
func Jaccard(a, b map[uint64]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

// Dedup reports the duplicate pairs among items, sorted by A then B. Only
// pairs with at least one item in scope are reported (nil scope means
// every item), so a new set is compared with itself and with what exists
// without listing old duplicates again. An item with empty text is never a
// duplicate. threshold <= 0 uses DefaultNearThreshold.
func Dedup(items []Item, scope map[string]bool, threshold float64) []DuplicatePair {
	if threshold <= 0 {
		threshold = DefaultNearThreshold
	}
	sigs := make([]Signature, 0, len(items))
	for _, it := range items {
		if len(Words(it.Text())) == 0 {
			continue
		}
		sigs = append(sigs, Sign(it))
	}
	inScope := func(k string) bool { return scope == nil || scope[k] }
	seen := map[[2]int]bool{}
	var out []DuplicatePair
	consider := func(i, j int) {
		if i == j {
			return
		}
		if i > j {
			i, j = j, i
		}
		p := [2]int{i, j}
		if seen[p] {
			return
		}
		seen[p] = true
		a, b := sigs[i], sigs[j]
		if !inScope(a.Key) && !inScope(b.Key) {
			return
		}
		if a.Exact == b.Exact {
			out = append(out, pair(a.Key, b.Key, DupExact, 1))
			return
		}
		if jac := Jaccard(a.Shingles, b.Shingles); jac >= threshold {
			out = append(out, pair(a.Key, b.Key, DupNear, round3(jac)))
		}
	}
	buckets := map[[2]uint64][]int{}
	for i, s := range sigs {
		for band := 0; band < minhashBands; band++ {
			h := fnv.New64a()
			for r := 0; r < minhashRows; r++ {
				v := s.MinHash[band*minhashRows+r]
				var b [8]byte
				for k := 0; k < 8; k++ {
					b[k] = byte(v >> (8 * k))
				}
				h.Write(b[:])
			}
			key := [2]uint64{uint64(band), h.Sum64()}
			for _, j := range buckets[key] {
				consider(i, j)
			}
			buckets[key] = append(buckets[key], i)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

func pair(a, b, kind string, jac float64) DuplicatePair {
	if b < a {
		a, b = b, a
	}
	return DuplicatePair{A: a, B: b, Kind: kind, Jaccard: jac}
}

func round3(f float64) float64 { return math.Round(f*1000) / 1000 }
