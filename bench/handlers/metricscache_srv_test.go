package handlers

import "testing"

// wrappedIndex has a comparable type, but its value holds an index that
// cannot be a map key (clusterIndex has maps): hashing it would panic.
type wrappedIndex struct{ ReadIndex }

// TestSealedMetricsKeyNeverPanics: an index whose type is comparable but
// whose value is not is never used as a cache key (UI fix wave review).
func TestSealedMetricsKeyNeverPanics(t *testing.T) {
	w := wrappedIndex{newClusterIndex()}
	if _, ok := sealedMetrics.key(w, "r_t01_A", "sha256:seal"); ok {
		t.Fatal("an uncomparable value was accepted as a key")
	}
	if r := runMetrics(w, w.Runs(testExp)[:1])["r_t01_A"]; !r.found {
		t.Fatalf("read through the wrapper: %+v", r)
	}
}
