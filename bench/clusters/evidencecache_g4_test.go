package clusters

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// sealAll gives every run of src a seal hash, as the store does for sealed runs.
func sealAll(src *fakeSource) {
	for exp, runs := range src.runs {
		for i := range runs {
			src.runs[exp][i].SealHash = "sha256:" + runs[i].Run.RunID
		}
	}
}

func resetEvidenceCache() {
	evidenceCache.Lock()
	evidenceCache.m, evidenceCache.bytes, evidenceCache.reads = map[evidenceKey][]byte{}, 0, 0
	evidenceCache.Unlock()
}

func evidenceReads() int64 {
	evidenceCache.Lock()
	defer evidenceCache.Unlock()
	return evidenceCache.reads
}

// mapSource is a Source that cannot be a map key.
type mapSource struct {
	*fakeSource
	tag map[string]bool
}

func TestSealedEvidenceCache(t *testing.T) {
	ctx := context.Background()
	q := Query{Synthetic: SyntheticInclude, Now: day.Add(96 * time.Hour)}
	build := func(src Source) *Report {
		t.Helper()
		rep, err := Build(ctx, src, q)
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}

	t.Run("second build reads nothing and answers the same", func(t *testing.T) {
		resetEvidenceCache()
		src := fixture()
		sealAll(src)
		first := build(src)
		cold := evidenceReads()
		second := build(src)
		if warm := evidenceReads() - cold; warm != 0 || cold == 0 {
			t.Fatalf("reads: first build %d, second build %d; want >0 and 0", cold, warm)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatal("cached build differs")
		}
		fresh := fixture() // unsealed: same answer read fresh
		if got := build(fresh); !reflect.DeepEqual(got, first) {
			t.Fatal("sealed build differs from an unsealed one over the same files")
		}
		t.Logf("evidence reads per build: %d cold, 0 warm", cold)
	})

	t.Run("unsealed runs are read fresh", func(t *testing.T) {
		resetEvidenceCache()
		src := fixture()
		before := build(src)
		n := evidenceReads()
		build(src)
		if evidenceReads() != 2*n {
			t.Fatalf("unsealed reads %d then %d", n, evidenceReads()-n)
		}
		src.files["r_new4"][FileStderr] = []byte("ValueError: changed\n")
		if reflect.DeepEqual(build(src), before) {
			t.Fatal("an unsealed run's changed stderr did not show")
		}
	})

	t.Run("another seal hash misses", func(t *testing.T) {
		resetEvidenceCache()
		src := fixture()
		sealAll(src)
		before := build(src)
		src.files["r_new4"][FileStderr] = []byte("ValueError: a different error\n")
		if got := build(src); !reflect.DeepEqual(got, before) {
			t.Fatal("same seal hash: the sealed bytes must be served")
		}
		for i, r := range src.runs["e_new"] {
			if r.Run.RunID == "r_new4" {
				src.runs["e_new"][i].SealHash = "sha256:resealed"
			}
		}
		n := evidenceReads()
		after := build(src)
		if evidenceReads() == n {
			t.Fatal("new seal hash served from the cache")
		}
		if reflect.DeepEqual(after, before) {
			t.Fatal("new seal hash: the changed stderr must show")
		}
	})

	t.Run("organizations never share", func(t *testing.T) {
		resetEvidenceCache()
		orgA, orgB := fixture(), fixture()
		sealAll(orgA)
		sealAll(orgB)
		orgB.files["r_new4"][FileStderr] = []byte("ValueError: org b only\n")
		a, b := build(orgA), build(orgB)
		if reflect.DeepEqual(a, b) {
			t.Fatal("org b answered with org a's evidence")
		}
		if !reflect.DeepEqual(build(orgA), a) || !reflect.DeepEqual(build(orgB), b) {
			t.Fatal("cached answers mixed")
		}
	})

	t.Run("source that is no map key, problems and errors", func(t *testing.T) {
		resetEvidenceCache()
		src := fixture()
		sealAll(src)
		ms := mapSource{fakeSource: src, tag: map[string]bool{}}
		build(ms)
		n := evidenceReads()
		build(ms)
		if evidenceReads() != 2*n {
			t.Fatal("a source that cannot be a key was cached")
		}
		evidenceCache.Lock()
		size := len(evidenceCache.m)
		evidenceCache.Unlock()
		if size != 0 {
			t.Fatalf("%d entries kept", size)
		}
	})

	t.Run("bounded", func(t *testing.T) {
		resetEvidenceCache()
		defer func(n int) { evidenceCacheMaxEntries = n }(evidenceCacheMaxEntries)
		evidenceCacheMaxEntries = 3
		src := fixture()
		sealAll(src)
		want := build(src)
		got := build(src)
		evidenceCache.Lock()
		size := len(evidenceCache.m)
		evidenceCache.Unlock()
		if size > 3 || !reflect.DeepEqual(got, want) {
			t.Fatalf("size %d over the bound, or answer changed", size)
		}
	})
}
