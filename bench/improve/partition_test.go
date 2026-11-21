package improve

import (
	"fmt"
	"slices"
	"testing"
)

func TestPartition(t *testing.T) {
	spec := &Spec{Seed: 3}
	spec.Defaults()
	spec.Splits.Honeypots = []string{"hp"}
	pool := []string{"hp"}
	for i := range 20 {
		pool = append(pool, fmt.Sprintf("t%02d", i))
	}
	a, err := Partition(pool, spec)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Partition(reversed(pool), spec)
	if a.Hash != b.Hash || len(a.Dev) != 8 || len(a.Audit) != 6 || len(a.Sealed) != 6 {
		t.Fatalf("partition %+v vs %+v", a, b)
	}
	all := slices.Concat(a.Dev, a.Audit, a.Sealed)
	slices.Sort(all)
	if len(slices.Compact(all)) != 20 || slices.Contains(all, "hp") {
		t.Errorf("not a disjoint cover: %v", all)
	}
	if err := a.Verify(); err != nil {
		t.Error(err)
	}
	a.Sealed[0] = "moved"
	if a.Verify() == nil {
		t.Error("moved task verified")
	}
	spec.Seed = 4
	if c, _ := Partition(pool, spec); c.Hash == b.Hash {
		t.Error("seed did not change the partition")
	}
	for n := 3; n < 6; n++ {
		s, err := Partition(pool[1:1+n], spec)
		if err != nil || len(s.Dev) == 0 || len(s.Audit) == 0 || len(s.Sealed) == 0 {
			t.Errorf("pool %d: %+v %v", n, s, err)
		}
	}
	if _, err := Partition(pool[1:3], spec); err == nil {
		t.Error("2 tasks accepted")
	}
}

func reversed(s []string) []string {
	out := slices.Clone(s)
	slices.Reverse(out)
	return out
}
