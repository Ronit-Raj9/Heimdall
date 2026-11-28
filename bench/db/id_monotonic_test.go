package db

import (
	"sync"
	"testing"
)

// Ids made one after another sort in the order they were made, also when
// many fall in the same millisecond ("newest by id" queries rely on it).
func TestNewIDSortsInCreationOrder(t *testing.T) {
	prev := NewID("job")
	for i := 0; i < 20000; i++ {
		id := NewID("job")
		if id <= prev {
			t.Fatalf("id %d %q does not sort after %q", i, id, prev)
		}
		prev = id
	}
}

// Concurrent callers never get the same id.
func TestNewIDUniqueAcrossGoroutines(t *testing.T) {
	const workers, each = 8, 5000
	var wg sync.WaitGroup
	out := make([][]string, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			ids := make([]string, each)
			for i := range ids {
				ids[i] = NewID("x")
			}
			out[w] = ids
		}(w)
	}
	wg.Wait()
	seen := make(map[string]bool, workers*each)
	for _, ids := range out {
		for _, id := range ids {
			if seen[id] {
				t.Fatalf("duplicate id %q", id)
			}
			seen[id] = true
		}
	}
}
