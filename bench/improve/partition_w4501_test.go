package improve

import (
	"fmt"
	"slices"
	"testing"
)

func TestPartitionWithSealed(t *testing.T) {
	var pool []string
	for i := range 40 {
		pool = append(pool, fmt.Sprintf("t%02d", i))
	}
	spec := &Spec{Seed: 7, Splits: SplitSpec{Dev: 0.4, Audit: 0.3, Sealed: 0.3, Honeypots: []string{"t39"}}}
	first, err := Partition(pool, spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, seed := range []int64{7, 1007, 2007} {
		s := *spec
		s.Seed = seed
		got, err := PartitionWithSealed(pool, first.Sealed, &s)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got.Sealed, first.Sealed) {
			t.Fatalf("seed %d: sealed moved", seed)
		}
		// 39 pool tasks (one honeypot) minus the sealed ones, split 4:3 (15 and 12).
		rest := 39 - len(first.Sealed)
		if len(got.Dev)+len(got.Audit) != rest || len(got.Dev) != 15 || len(got.Audit) != 12 {
			t.Fatalf("seed %d: dev %d audit %d of %d", seed, len(got.Dev), len(got.Audit), rest)
		}
		for _, x := range append(slices.Clone(got.Dev), got.Audit...) {
			if slices.Contains(got.Sealed, x) || x == "t39" {
				t.Fatalf("seed %d: %s in two places", seed, x)
			}
		}
		if err := got.Verify(); err != nil {
			t.Fatal(err)
		}
		if seed == 7 {
			// The same seed keeps the hash order: every dev task was a dev task of the first partition.
			for _, x := range got.Dev {
				if !slices.Contains(first.Dev, x) {
					t.Fatalf("same seed moved %s out of dev", x)
				}
			}
		}
	}
	if _, err := PartitionWithSealed(pool[:3], pool[:2], spec); err == nil {
		t.Fatal("accepted a pool with one task outside sealed")
	}
}
