package improve

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/maximhq/bifrost/bench/utils"
)

// Split names.
const (
	SplitDev    = "dev"
	SplitAudit  = "audit"
	SplitSealed = "sealed"
)

// Splits is a frozen partition of the task pool. Hash covers the three
// lists and the honeypots, so a later reader can prove the partition did
// not move.
type Splits struct {
	Dev       []string `json:"dev"`
	Audit     []string `json:"audit"`
	Sealed    []string `json:"sealed"`
	Honeypots []string `json:"honeypots,omitempty"`
	Hash      string   `json:"hash"`
}

// Partition splits tasks into dev, audit and sealed by the spec's shares.
// The order is a seeded hash of each task id, so the same seed and pool
// always give the same partition and adding a task never moves the others
// between splits by more than its own slot. Every split gets at least one
// task; honeypots are kept out of the pool.
func Partition(tasks []string, spec *Spec) (Splits, error) {
	pool := []string{}
	seen := map[string]bool{}
	for _, t := range tasks {
		if t == "" || seen[t] || slices.Contains(spec.Splits.Honeypots, t) {
			continue
		}
		seen[t] = true
		pool = append(pool, t)
	}
	if len(pool) < 3 {
		return Splits{}, fmt.Errorf("an improvement run needs at least 3 tasks to partition, the pool has %d", len(pool))
	}
	key := func(t string) string { return utils.SHA256Bytes(fmt.Appendf(nil, "%d\x00%s", spec.Seed, t)) }
	slices.SortFunc(pool, func(a, b string) int { return strings.Compare(key(a), key(b)) })
	n := len(pool)
	nd := max(1, int(math.Round(spec.Splits.Dev*float64(n))))
	na := max(1, int(math.Round(spec.Splits.Audit*float64(n))))
	ns := max(1, int(math.Round(spec.Splits.Sealed*float64(n))))
	for nd+na+ns > n { // rounding gave more than the pool: shrink the largest
		switch {
		case nd >= na && nd >= ns && nd > 1:
			nd--
		case na >= ns && na > 1:
			na--
		default:
			ns--
		}
	}
	s := Splits{Dev: slices.Clone(pool[:nd]), Audit: slices.Clone(pool[nd : nd+na]), Sealed: slices.Clone(pool[nd+na:]),
		Honeypots: slices.Clone(spec.Splits.Honeypots)}
	for _, l := range [][]string{s.Dev, s.Audit, s.Sealed} {
		slices.Sort(l)
	}
	h, err := utils.HashJSON([4][]string{s.Dev, s.Audit, s.Sealed, s.Honeypots})
	if err != nil {
		return Splits{}, err
	}
	s.Hash = h
	return s, nil
}

// Verify recomputes the partition hash.
func (s Splits) Verify() error {
	h, err := utils.HashJSON([4][]string{s.Dev, s.Audit, s.Sealed, s.Honeypots})
	if err != nil {
		return err
	}
	if h != s.Hash {
		return errors.New("the frozen partition does not match its hash")
	}
	return nil
}
