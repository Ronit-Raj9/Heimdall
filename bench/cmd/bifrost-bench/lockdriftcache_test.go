package main

import (
	"errors"
	"testing"

	"github.com/maximhq/bifrost/bench/schemas"
)

// The lock drift of a pack is hashed once per stamp: a second call with the
// same key does not rehash, a changed stamp or lock does, errors are not
// kept, the oldest entry goes first and callers get their own copy.
func TestLockDriftCachedPerStamp(t *testing.T) {
	c := newLockDriftCache(2)
	hashes := 0
	compute := func() ([]schemas.SuiteDrift, error) {
		hashes++
		return []schemas.SuiteDrift{{Scope: "task", ID: "t1", Fields: []string{"files"}}}, nil
	}
	k := lockDriftKey{dir: "/lib/pack", stamp: "s1", lockHash: "h1"}
	first, err := c.get(k, compute)
	if err != nil || len(first) != 1 {
		t.Fatalf("first: %v %v", first, err)
	}
	first[0].ID, first[0].Fields[0] = "changed", "changed"
	again, _ := c.get(k, compute)
	if hashes != 1 {
		t.Fatalf("same stamp rehashed: %d hashes", hashes)
	}
	if again[0].ID != "t1" || again[0].Fields[0] != "files" {
		t.Fatalf("cached entry changed through a caller's copy: %+v", again)
	}
	if _, _ = c.get(lockDriftKey{dir: "/lib/pack", stamp: "s2", lockHash: "h1"}, compute); hashes != 2 {
		t.Fatalf("changed stamp not rehashed: %d", hashes)
	}
	if _, _ = c.get(lockDriftKey{dir: "/lib/pack", stamp: "s2", lockHash: "h1", fingerprinted: true}, compute); hashes != 3 {
		t.Fatalf("fingerprinted lock not rehashed: %d", hashes)
	}
	// Two entries at most: the s1 entry was dropped first.
	if _, _ = c.get(k, compute); hashes != 4 {
		t.Fatalf("oldest entry kept past the limit: %d", hashes)
	}

	fails := 0
	failing := func() ([]schemas.SuiteDrift, error) { fails++; return nil, errors.New("hash failed") }
	bad := lockDriftKey{dir: "/lib/bad", stamp: "s"}
	for range 2 {
		if _, err := c.get(bad, failing); err == nil {
			t.Fatal("error lost")
		}
	}
	if fails != 2 {
		t.Fatalf("error cached: %d calls", fails)
	}
}
