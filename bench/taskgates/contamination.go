package taskgates

import (
	"hash/fnv"
	"sort"
	"strings"
)

// Contamination checks a task's text against an index of public tasks (the
// built-in library): any shared run of 13 words means the task, or its
// answer, is already public. For a public task that is a warning; for a
// held-out task it is an error, because a held-out score is only worth
// something when no agent or model could have seen the task.

// NgramWords is the run length the contamination check matches (the
// 13-gram overlap used by GPT-3 and later decontamination studies).
const NgramWords = 13

// maxHitsPerTask caps the library tasks named per contaminated task.
const maxHitsPerTask = 5

// Index maps the 13-gram hashes of public tasks to the tasks that hold them.
// Texts shorter than 13 words are indexed whole, so a short question copied
// verbatim is still found. The zero value is not usable; call NewIndex.
type Index struct {
	grams map[uint64][]int32
	keys  []string
}

// NewIndex indexes items.
func NewIndex(items []Item) *Index {
	ix := &Index{grams: map[uint64][]int32{}}
	for _, it := range items {
		ix.Add(it)
	}
	return ix
}

// Add indexes one item.
func (ix *Index) Add(it Item) {
	id := int32(len(ix.keys))
	ix.keys = append(ix.keys, it.Key())
	for g := range grams(Words(it.Text())) {
		l := ix.grams[g]
		if len(l) > 0 && l[len(l)-1] == id {
			continue
		}
		ix.grams[g] = append(l, id)
	}
}

// Len is the number of indexed tasks.
func (ix *Index) Len() int {
	if ix == nil {
		return 0
	}
	return len(ix.keys)
}

func grams(words []string) map[uint64]struct{} {
	out := map[uint64]struct{}{}
	if len(words) == 0 {
		return out
	}
	if len(words) < NgramWords {
		out[shortKey(words)] = struct{}{}
		return out
	}
	for i := 0; i+NgramWords <= len(words); i++ {
		out[hashWords(words[i:i+NgramWords])] = struct{}{}
	}
	return out
}

// shortKey keeps a whole short text apart from any 13-gram hash.
func shortKey(words []string) uint64 {
	h := fnv.New64a()
	h.Write([]byte("short\x00"))
	for _, s := range words {
		h.Write([]byte(s))
		h.Write([]byte{0})
	}
	return h.Sum64()
}

// Contamination severities (ContaminationHit.Severity).
const (
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// ContaminationHit is one task whose text overlaps public tasks.
type ContaminationHit struct {
	Task     string   `json:"task"`     // suite/task checked
	Split    string   `json:"split"`    // its split: public | held_out
	Overlap  float64  `json:"overlap"`  // share of its 13-grams found in the library, 0..1
	Matches  []string `json:"matches"`  // library tasks sharing the most 13-grams, at most 5
	Severity string   `json:"severity"` // warning for a public task, error for a held-out one
}

// Check returns every item that shares at least one 13-gram with the index,
// sorted by task. An item never matches a task of its own suite (that is
// the dedup gate's job), so a library suite can be checked against the
// library. splitOf gives an
// item's split (nil reads every item as public).
func (ix *Index) Check(items []Item, splitOf func(Item) string) []ContaminationHit {
	if ix == nil || len(ix.keys) == 0 {
		return nil
	}
	var out []ContaminationHit
	for _, it := range items {
		gs := grams(Words(it.Text()))
		if len(gs) == 0 {
			continue
		}
		self := it.Key()
		own := it.Suite + "/"
		counts := map[int32]int{}
		hit := 0
		for g := range gs {
			found := false
			for _, id := range ix.grams[g] {
				if strings.HasPrefix(ix.keys[id], own) {
					continue
				}
				counts[id]++
				found = true
			}
			if found {
				hit++
			}
		}
		if hit == 0 {
			continue
		}
		ids := make([]int32, 0, len(counts))
		for id := range counts {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool {
			if counts[ids[i]] != counts[ids[j]] {
				return counts[ids[i]] > counts[ids[j]]
			}
			return ix.keys[ids[i]] < ix.keys[ids[j]]
		})
		if len(ids) > maxHitsPerTask {
			ids = ids[:maxHitsPerTask]
		}
		h := ContaminationHit{Task: self, Split: "public", Overlap: round3(float64(hit) / float64(len(gs))), Severity: SeverityWarning}
		if splitOf != nil {
			if s := splitOf(it); s != "" {
				h.Split = s
			}
		}
		if h.Split == "held_out" {
			h.Severity = SeverityError
		}
		for _, id := range ids {
			h.Matches = append(h.Matches, ix.keys[id])
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Task < out[j].Task })
	return out
}
